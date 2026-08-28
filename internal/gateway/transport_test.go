package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LightHaru/codex-relay/internal/state"
)

func pointer(value bool) *bool { return &value }

type cancelAfterEventBody struct {
	reader *bytes.Reader
	cancel context.CancelFunc
	sent   bool
}

func (body *cancelAfterEventBody) Read(target []byte) (int, error) {
	if body.sent {
		return 0, context.Canceled
	}
	read, _ := body.reader.Read(target)
	body.sent = true
	body.cancel()
	return read, nil
}

func (*cancelAfterEventBody) Close() error { return nil }

func gatewayTestStore(t *testing.T, count int) (*state.Store, []string) {
	t.Helper()
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	accounts := store.Accounts()
	for index := 1; index < count; index++ {
		account, addErr := store.AddAccount(fmt.Sprintf("Source %d", index+1))
		if addErr != nil {
			t.Fatal(addErr)
		}
		accounts = append(accounts, account)
	}
	ids := make([]string, 0, len(accounts))
	for index, account := range accounts {
		ids = append(ids, account.ID)
		auth := fmt.Sprintf(`{"tokens":{"access_token":"token-%d","account_id":"upstream-%d"}}`, index+1, index+1)
		if err := os.MkdirAll(account.CodexHome, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(account.CodexHome, "auth.json"), []byte(auth), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = store.UpdateCredentialSource(account.ID, func(source *state.CredentialSourceState) error {
			source.Connected = true
			source.AuthState = "authenticated"
			source.MembershipState = state.SourceAvailable
			source.QuotaEvidence = state.QuotaEvidence{Allowed: pointer(true), ObservedAt: time.Now().UnixMilli(), Source: "test"}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return store, ids
}

func successfulSSE() string {
	return "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
}

func TestSSETerminalClassifierIgnoresNestedCompletedItemStatus(t *testing.T) {
	event := []byte("event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"status\":\"completed\"}}\n\n")
	if got := classifySSETerminal(event); got != "" {
		t.Fatalf("output-item boundary was misclassified as %q terminal", got)
	}
	if !isSSETerminalCandidate(event) {
		t.Fatal("output-item boundary was not retained as a terminal candidate")
	}
	completed := []byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	if got := classifySSETerminal(completed); got != "completed" {
		t.Fatalf("response.completed was not recognized: %q", got)
	}
}

func sendGatewayRequest(t *testing.T, server *httptest.Server, id string) (int, string) {
	return sendGatewayRequestBody(t, server, id, `{"stream":true,"input":[]}`)
}

func sendGatewayRequestBody(t *testing.T, server *httptest.Server, id, requestBody string) (int, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/responses", bytes.NewBufferString(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Client-Request-Id", id)
	request.Header.Set("Session-Id", "session-one")
	request.Header.Set("Thread-Id", "thread-one")
	request.Header.Set("Authorization", "Bearer public-relay-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(body)
}

func activeGatewayLease(t *testing.T, store *state.Store, clientRequestID string) state.PoolLease {
	t.Helper()
	prefix := clientRequestID + "-"
	for leaseID, lease := range store.PoolState().ActiveLeases {
		if strings.HasPrefix(leaseID, prefix) {
			return lease
		}
	}
	t.Fatalf("no active Gateway lease for client request %q", clientRequestID)
	return state.PoolLease{}
}

func sendModelsRequest(t *testing.T, server *httptest.Server, bearer string) (int, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/v1/models?client_version=0.150.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(body)
}

func TestTransportProxiesAndCachesModelsWithoutQuotaLease(t *testing.T) {
	store, _ := gatewayTestStore(t, 2)
	var mu sync.Mutex
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		if request.Method != http.MethodGet || request.URL.Path != "/models" {
			t.Fatalf("unexpected models request: %s %s", request.Method, request.URL.Path)
		}
		if request.URL.Query().Get("client_version") != "0.150.0" {
			t.Fatalf("client_version was not forwarded: %q", request.URL.RawQuery)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("local bearer leaked or source bearer missing: %q", got)
		}
		if got := request.Header.Get("ChatGPT-Account-ID"); got != "upstream-1" {
			t.Fatalf("controller account header=%q", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"models":[{"slug":"gpt-test","visibility":"list"}]}`)
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{
		Store: store, ModelsURL: upstream.URL + "/models", Client: upstream.Client(), LocalBearerToken: "relay-local",
	})
	defer gateway.Close()
	for index := 0; index < 2; index++ {
		status, body := sendModelsRequest(t, gateway, "relay-local")
		if status != http.StatusOK || !strings.Contains(body, "gpt-test") {
			t.Fatalf("models request %d status=%d body=%q", index, status, body)
		}
	}
	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 1 {
		t.Fatalf("models cache made %d upstream calls, want 1", gotCalls)
	}
	if leases := len(store.PoolState().ActiveLeases); leases != 0 {
		t.Fatalf("model discovery acquired %d quota leases", leases)
	}
}

func TestTransportModelsFallsBackFromInvalidControllerCredentials(t *testing.T) {
	store, _ := gatewayTestStore(t, 2)
	var mu sync.Mutex
	used := make([]string, 0, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		account := request.Header.Get("ChatGPT-Account-ID")
		mu.Lock()
		used = append(used, account)
		mu.Unlock()
		if account == "upstream-1" {
			http.Error(writer, `{"detail":"private upstream auth detail"}`, http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"models":[{"slug":"gpt-fallback","visibility":"list"}]}`)
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, ModelsURL: upstream.URL, Client: upstream.Client()})
	defer gateway.Close()
	status, body := sendModelsRequest(t, gateway, "unused")
	if status != http.StatusOK || !strings.Contains(body, "gpt-fallback") {
		t.Fatalf("status=%d body=%q", status, body)
	}
	mu.Lock()
	got := fmt.Sprint(used)
	mu.Unlock()
	if got != "[upstream-1 upstream-2]" {
		t.Fatalf("model catalog credentials=%s", got)
	}
}

func TestTransportModelsRejectsInvalidCatalogWithoutLeakingBody(t *testing.T) {
	store, _ := gatewayTestStore(t, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, `{"secret":"private catalog diagnostic"}`)
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, ModelsURL: upstream.URL, Client: upstream.Client()})
	defer gateway.Close()
	status, body := sendModelsRequest(t, gateway, "unused")
	if status != http.StatusBadGateway || !strings.Contains(body, "models_refresh_failed") {
		t.Fatalf("status=%d body=%q", status, body)
	}
	if strings.Contains(body, "private catalog diagnostic") {
		t.Fatalf("upstream models body leaked: %q", body)
	}
}

func TestTransportRequiresItsLocalBearerToken(t *testing.T) {
	store, _ := gatewayTestStore(t, 1)
	gateway := httptest.NewServer(&Transport{Store: store, LocalBearerToken: "private-local-token"})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "unauthorized")
	if status != http.StatusUnauthorized || strings.Contains(body, "private-local-token") {
		t.Fatalf("status=%d body=%q", status, body)
	}
	modelsStatus, modelsBody := sendModelsRequest(t, gateway, "wrong-token")
	if modelsStatus != http.StatusUnauthorized || strings.Contains(modelsBody, "private-local-token") {
		t.Fatalf("models status=%d body=%q", modelsStatus, modelsBody)
	}
}

func TestPrimeCredentialSourcesUsesProbationWithoutGuessingQuota(t *testing.T) {
	store, ids := gatewayTestStore(t, 2)
	for _, id := range ids {
		_, err := store.UpdateCredentialSource(id, func(source *state.CredentialSourceState) error {
			source.MembershipState = state.SourceProbing
			source.QuotaEvidence = state.QuotaEvidence{}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := PrimeCredentialSources(store); err != nil {
		t.Fatal(err)
	}
	pool := store.PoolState()
	for _, id := range ids {
		if pool.Sources[id].MembershipState != state.SourceProbation || pool.Sources[id].QuotaState != "unknown" {
			t.Fatalf("source %s quota was guessed: %#v", id, pool.Sources[id])
		}
	}
}

func TestTransportKeepsStickySourceAcrossTwentyTurns(t *testing.T) {
	store, _ := gatewayTestStore(t, 4)
	var mu sync.Mutex
	used := make([]string, 0, 20)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		used = append(used, request.Header.Get("ChatGPT-Account-ID"))
		mu.Unlock()
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, successfulSSE())
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client()})
	defer gateway.Close()
	for index := 0; index < 20; index++ {
		status, body := sendGatewayRequest(t, gateway, fmt.Sprintf("turn-%d", index))
		if status != 200 || body == "" {
			t.Fatalf("turn %d status=%d body=%q", index, status, body)
		}
	}
	if len(used) != 20 {
		t.Fatalf("upstream calls=%d", len(used))
	}
	for index, source := range used {
		if source != "upstream-1" {
			t.Fatalf("turn %d rotated to %q while A was healthy", index, source)
		}
	}
}

func TestTransportUsesOnePublicAPIWithBalancedPoolCredentials(t *testing.T) {
	store, _ := gatewayTestStore(t, 4)
	var mu sync.Mutex
	used := make([]string, 0, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		used = append(used, request.Header.Get("ChatGPT-Account-ID"))
		mu.Unlock()
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, successfulSSE())
	}))
	defer upstream.Close()
	// BalancedPool changes only the hidden credential selection. Every request
	// still enters this single HTTP server/API and retains the same logical
	// request shape.
	gateway := httptest.NewServer(&Transport{
		Store: store, UpstreamURL: upstream.URL, Client: upstream.Client(), BalancedPool: true,
	})
	defer gateway.Close()
	for index := 0; index < 8; index++ {
		status, body := sendGatewayRequest(t, gateway, fmt.Sprintf("balanced-turn-%d", index))
		if status != http.StatusOK || body == "" {
			t.Fatalf("turn %d status=%d body=%q", index, status, body)
		}
	}
	want := "[upstream-1 upstream-2 upstream-3 upstream-4 upstream-1 upstream-2 upstream-3 upstream-4]"
	if got := fmt.Sprint(used); got != want {
		t.Fatalf("balanced pool credential sequence=%s, want %s", got, want)
	}
}

func TestTransportFailsOverSameRequestBeforeOutput(t *testing.T) {
	store, ids := gatewayTestStore(t, 3)
	var mu sync.Mutex
	var used []string
	var bodies [][]byte
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		account := request.Header.Get("ChatGPT-Account-ID")
		mu.Lock()
		used = append(used, account)
		bodies = append(bodies, body)
		mu.Unlock()
		if account == "upstream-1" {
			http.Error(writer, `{"error":{"code":"usage_limit"}}`, http.StatusTooManyRequests)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, successfulSSE())
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client()})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "same-logical-turn")
	if status != 200 || body == "" {
		t.Fatalf("status=%d body=%q", status, body)
	}
	if fmt.Sprint(used) != "[upstream-1 upstream-2]" {
		t.Fatalf("source order=%v", used)
	}
	if len(bodies) != 2 || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatal("pre-output retry changed the request body")
	}
	pool := store.PoolState()
	if pool.ActiveSourceID != ids[1] || pool.FailoverCount != 1 || pool.Sources[ids[0]].MembershipState != state.SourceDepleted {
		t.Fatalf("unexpected failover state: %#v", pool)
	}
}

func TestTransportSkipsCredentialSourceAfterUpstreamUnauthorized(t *testing.T) {
	store, ids := gatewayTestStore(t, 2)
	var used []string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		used = append(used, request.Header.Get("ChatGPT-Account-ID"))
		if len(used) == 1 {
			http.Error(writer, `{"error":{"message":"token expired"}}`, http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, successfulSSE())
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client()})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "auth-failover")
	if status != http.StatusOK || body == "" || fmt.Sprint(used) != "[upstream-1 upstream-2]" {
		t.Fatalf("credential source was not skipped: status=%d used=%v body=%q", status, used, body)
	}
	pool := store.PoolState()
	if pool.ActiveSourceID != ids[1] || pool.Sources[ids[0]].MembershipState != state.SourceProvisioning {
		t.Fatalf("authentication failure did not quarantine first source: %#v", pool)
	}
}

func TestTransportFailsClosedWhenCompatibilityProfileIsUnknown(t *testing.T) {
	store, _ := gatewayTestStore(t, 2)
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		http.Error(writer, `{"error":{"code":"usage_limit_reached"}}`, http.StatusTooManyRequests)
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{
		Store: store, UpstreamURL: upstream.URL, Client: upstream.Client(), DisableFailover: true,
	})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "unknown-profile")
	if status != http.StatusServiceUnavailable || calls != 1 {
		t.Fatalf("unknown profile retried or returned wrong status: status=%d calls=%d body=%q", status, calls, body)
	}
	if strings.Contains(strings.ToLower(body), "usage_limit") || !strings.Contains(body, "compatibility profile") {
		t.Fatalf("unknown profile leaked upstream quota detail: %q", body)
	}
	if len(store.PoolState().ActiveLeases) != 0 {
		t.Fatalf("unknown profile left an active lease: %#v", store.PoolState().ActiveLeases)
	}
}

func TestTransportRetriesEarlyStreamQuotaButNotLateQuota(t *testing.T) {
	store, ids := gatewayTestStore(t, 3)
	var mu sync.Mutex
	counts := map[string]int{}
	late := false
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		account := request.Header.Get("ChatGPT-Account-ID")
		mu.Lock()
		counts[account]++
		useLate := late
		mu.Unlock()
		writer.Header().Set("Content-Type", "text/event-stream")
		if account == "upstream-1" {
			if useLate {
				_, _ = io.WriteString(writer, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"visible\"}\n\n")
			}
			_, _ = io.WriteString(writer, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"usage_limit\"}}}\n\n")
			return
		}
		_, _ = io.WriteString(writer, successfulSSE())
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client()})
	status, _ := sendGatewayRequest(t, gateway, "early")
	if status != 200 || counts["upstream-1"] != 1 || counts["upstream-2"] != 1 {
		t.Fatalf("early failover counts=%v status=%d", counts, status)
	}

	store2, ids2 := gatewayTestStore(t, 2)
	_ = ids
	late = true
	counts = map[string]int{}
	gateway2 := httptest.NewServer(&Transport{Store: store2, UpstreamURL: upstream.URL, Client: upstream.Client()})
	defer gateway2.Close()
	status, body := sendGatewayRequest(t, gateway2, "late")
	if status != 200 || counts["upstream-1"] != 1 || counts["upstream-2"] != 0 || body == "" {
		t.Fatalf("late quota replayed or lost output: counts=%v status=%d body=%q", counts, status, body)
	}
	lease := activeGatewayLease(t, store2, "late")
	if lease.State != state.PoolLeaseRecoveryRequired || store2.PoolState().ActiveSourceID != ids2[1] {
		t.Fatalf("late quota was not recovery-safe: %#v", lease)
	}
	if task := store2.TaskRecords()["thread-one"]; task.RecoveryState != "recovery-required" {
		t.Fatalf("late quota did not mark task recovery state: %#v", task)
	}
	if strings.Contains(strings.ToLower(body), "usage_limit") || !strings.Contains(body, "relay_pool_recovery_required") {
		t.Fatalf("late quota leaked upstream account error instead of pool recovery event: %q", body)
	}
}

func TestTransportRetriesCleanEOFWithoutTerminalOnNextSource(t *testing.T) {
	store, ids := gatewayTestStore(t, 2)
	var used []string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		account := request.Header.Get("ChatGPT-Account-ID")
		used = append(used, account)
		writer.Header().Set("Content-Type", "text/event-stream")
		if account == "upstream-1" {
			_, _ = io.WriteString(writer, "event: response.created\ndata: {\"type\":\"response.created\"}\n\n")
			return
		}
		_, _ = io.WriteString(writer, successfulSSE())
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client(), BalancedPool: true})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "clean-eof-before-terminal")
	if status != http.StatusOK || !strings.Contains(body, "response.completed") || fmt.Sprint(used) != "[upstream-1 upstream-2]" {
		t.Fatalf("truncated pre-commit SSE was not recovered: status=%d used=%v body=%q", status, used, body)
	}
	pool := store.PoolState()
	failed := pool.Sources[ids[0]]
	if !failed.Connected || failed.MembershipState == state.SourceDepleted || failed.TransientFailures != 1 || failed.CircuitState != "suspect" {
		t.Fatalf("truncated stream corrupted source state: %#v", failed)
	}
	if len(pool.ActiveLeases) != 0 || pool.LastError != nil {
		t.Fatalf("successful stream fallback retained stale state: leases=%#v error=%#v", pool.ActiveLeases, pool.LastError)
	}
}

func TestTransportDoesNotAcceptPartialCompletedEventAsTerminal(t *testing.T) {
	store, _ := gatewayTestStore(t, 2)
	var used []string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		account := request.Header.Get("ChatGPT-Account-ID")
		used = append(used, account)
		writer.Header().Set("Content-Type", "text/event-stream")
		if account == "upstream-1" {
			_, _ = io.WriteString(writer, "event: response.completed\ndata: {\"type\":\"response.completed\"")
			return
		}
		_, _ = io.WriteString(writer, successfulSSE())
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client(), BalancedPool: true})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "partial-terminal")
	if status != http.StatusOK || !strings.Contains(body, "response.completed") || fmt.Sprint(used) != "[upstream-1 upstream-2]" {
		t.Fatalf("partial terminal event was accepted instead of retried: status=%d used=%v body=%q", status, used, body)
	}
}

func TestTransportDoesNotReplayTruncatedStreamAfterVisibleOutput(t *testing.T) {
	store, _ := gatewayTestStore(t, 2)
	var used []string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		used = append(used, request.Header.Get("ChatGPT-Account-ID"))
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"visible\"}\n\n")
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client(), BalancedPool: true})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "truncated-after-output")
	if status != http.StatusOK || len(used) != 1 || !strings.Contains(body, "visible") || !strings.Contains(body, "response.failed") || !strings.Contains(body, "relay_pool_recovery_required") {
		t.Fatalf("post-commit truncation replayed or lost output: status=%d used=%v body=%q", status, used, body)
	}
	lease := activeGatewayLease(t, store, "truncated-after-output")
	if lease.State != state.PoolLeaseRecoveryRequired {
		t.Fatalf("post-commit truncation was not recovery-required: %#v", lease)
	}
}

func TestTransportDropsPartialFrameBeforeRecoveryTerminal(t *testing.T) {
	store, _ := gatewayTestStore(t, 2)
	var used []string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		used = append(used, request.Header.Get("ChatGPT-Account-ID"))
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"visible\"}\n\n")
		// Simulate the provider resetting in the middle of the next JSON
		// event. Forwarding this fragment would corrupt the native parser even
		// if Relay appends a recovery terminal event afterwards.
		_, _ = io.WriteString(writer, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial")
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client(), BalancedPool: true})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "partial-after-output")
	if status != http.StatusOK || len(used) != 1 || !strings.Contains(body, "visible") || !strings.Contains(body, "response.failed") || !strings.Contains(body, "relay_pool_recovery_required") {
		t.Fatalf("partial post-commit frame was not converted to recovery terminal: status=%d used=%v body=%q", status, used, body)
	}
	if strings.Contains(body, "partial") {
		t.Fatalf("unterminated SSE fragment leaked to native client: %q", body)
	}
}

func TestTransportConvertsIdlePostCommitStreamToRecoveryTerminal(t *testing.T) {
	store, _ := gatewayTestStore(t, 1)
	idleTimeout := 50 * time.Millisecond
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp-idle\"}}\n\n")
		_, _ = io.WriteString(writer, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"delta\":\"visible\"}\n\n")
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(idleTimeout + 100*time.Millisecond)
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client(), BalancedPool: true, SSEIdleTimeout: idleTimeout})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "idle-after-output")
	if status != http.StatusOK || !strings.Contains(body, "response.failed") || !strings.Contains(body, "resp-idle") || !strings.Contains(body, "relay_pool_recovery_required") {
		t.Fatalf("idle post-commit stream did not receive terminal recovery event: status=%d body=%q", status, body)
	}
}

func TestTerminalIncompleteIsForwardedButNotRecordedCompleted(t *testing.T) {
	store, _ := gatewayTestStore(t, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "event: response.incomplete\ndata: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\"}}\n\n")
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client(), BalancedPool: true})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "terminal-incomplete")
	if status != http.StatusOK || !strings.Contains(body, "response.incomplete") {
		t.Fatalf("terminal incomplete event was not forwarded: status=%d body=%q", status, body)
	}
	if len(store.PoolState().ActiveLeases) != 0 {
		t.Fatalf("terminal incomplete retained a lease: %#v", store.PoolState().ActiveLeases)
	}
	if task := store.TaskRecords()["thread-one"]; task.LastCompletedTurnID == "terminal-incomplete" {
		t.Fatalf("terminal incomplete was recorded as completed: %#v", task)
	}
}

func TestTransportRetriesTemporaryHTTPBadGatewayAcrossSources(t *testing.T) {
	store, _ := gatewayTestStore(t, 2)
	var used []string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		account := request.Header.Get("ChatGPT-Account-ID")
		used = append(used, account)
		if account == "upstream-1" {
			http.Error(writer, "temporary edge failure", http.StatusBadGateway)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, successfulSSE())
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client(), BalancedPool: true})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "http-502-failover")
	if status != http.StatusOK || !strings.Contains(body, "response.completed") || fmt.Sprint(used) != "[upstream-1 upstream-2]" {
		t.Fatalf("temporary HTTP 502 was not recovered: status=%d used=%v body=%q", status, used, body)
	}
}

func TestTransportStopsProviderWideHTTPFailureBeforeRetryStorm(t *testing.T) {
	store, ids := gatewayTestStore(t, 5)
	var used []string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		used = append(used, request.Header.Get("ChatGPT-Account-ID"))
		http.Error(writer, "temporary shared edge failure", http.StatusBadGateway)
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client(), BalancedPool: true})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "provider-wide-outage")
	if status != http.StatusServiceUnavailable || !strings.Contains(body, "provider_wide_outage") || !strings.Contains(body, "reference RP-") {
		t.Fatalf("provider-wide outage was not surfaced safely: status=%d body=%q", status, body)
	}
	if len(used) != 3 {
		t.Fatalf("provider-wide outage retried every source instead of stopping at threshold: used=%v", used)
	}
	pool := store.PoolState()
	if len(pool.ActiveLeases) != 0 || pool.LastError == nil || pool.LastError.Code != "provider_wide_outage" {
		t.Fatalf("provider-wide outage left inconsistent pool state: leases=%#v error=%#v", pool.ActiveLeases, pool.LastError)
	}
	for _, id := range ids[:3] {
		source := pool.Sources[id]
		if source.MembershipState == state.SourceDepleted || source.QuotaEvidence.ExplicitlyDepleted() {
			t.Fatalf("provider-wide transport failure changed quota state for %q: %#v", id, source)
		}
	}
}

func TestAllSourcesTransientFailureReturnsOneDiagnosticAndClearsLease(t *testing.T) {
	store, ids := gatewayTestStore(t, 3)
	var used []string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		used = append(used, request.Header.Get("ChatGPT-Account-ID"))
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "event: response.created\ndata: {\"type\":\"response.created\"}\n\n")
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client(), BalancedPool: true})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "all-transient")
	if status != http.StatusBadGateway || !strings.Contains(body, "retry_budget_exhausted") || !strings.Contains(body, "3 safe attempt(s)") || !strings.Contains(body, "reference RP-") {
		t.Fatalf("all-source transient failure was not actionable: status=%d body=%q", status, body)
	}
	if len(used) != 3 {
		t.Fatalf("transient retry attempts=%v, want one per eligible source", used)
	}
	pool := store.PoolState()
	if len(pool.ActiveLeases) != 0 {
		t.Fatalf("retry exhaustion retained a lease: %#v", pool.ActiveLeases)
	}
	for _, id := range ids {
		source := pool.Sources[id]
		if !source.Connected || source.MembershipState == state.SourceDepleted || source.QuotaEvidence.ExplicitlyDepleted() {
			t.Fatalf("transient retry exhausted quota/auth state for %q: %#v", id, source)
		}
	}
}

func TestGatewayReplayAfterRestartDoesNotReturnLogicalTurnConflict(t *testing.T) {
	store, _ := gatewayTestStore(t, 2)
	stale, err := store.AcquireBalancedPoolLease(state.PoolLease{
		LeaseID: "restart-replay", LogicalTurnID: "restart-replay", ThreadID: "thread-one",
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecoverPoolLeases(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, exists := store.PoolState().ActiveLeases[stale.LeaseID]; exists {
		t.Fatal("startup recovery retained stale pre-commit lease")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, successfulSSE())
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client(), BalancedPool: true})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, stale.LeaseID)
	if status != http.StatusOK || !strings.Contains(body, "response.completed") {
		t.Fatalf("native replay after restart failed: status=%d body=%q", status, body)
	}
}

func TestConcurrentDuplicateLogicalTurnJoinsOneUpstreamFlight(t *testing.T) {
	store, _ := gatewayTestStore(t, 2)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		upstreamCalls++
		mu.Unlock()
		once.Do(func() { close(started) })
		<-release
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, successfulSSE())
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client(), BalancedPool: true})
	defer gateway.Close()
	type result struct {
		status int
		body   string
	}
	results := make(chan result, 2)
	go func() {
		status, body := sendGatewayRequest(t, gateway, "duplicate-turn")
		results <- result{status: status, body: body}
	}()
	<-started
	go func() {
		status, body := sendGatewayRequest(t, gateway, "duplicate-turn")
		results <- result{status: status, body: body}
	}()
	time.Sleep(25 * time.Millisecond)
	close(release)
	first, second := <-results, <-results
	for index, got := range []result{first, second} {
		if got.status != http.StatusOK || !strings.Contains(got.body, "response.completed") {
			t.Fatalf("duplicate response %d was not replayed from the shared flight: %#v", index, got)
		}
	}
	mu.Lock()
	calls := upstreamCalls
	mu.Unlock()
	if calls != 1 {
		t.Fatalf("duplicate logical turn dispatched %d upstream requests, want exactly one", calls)
	}
	if len(store.PoolState().ActiveLeases) != 0 {
		t.Fatalf("duplicate flight retained a lease: %#v", store.PoolState().ActiveLeases)
	}
}

func TestReusedClientRequestIDDifferentBodiesDispatchDistinctTurns(t *testing.T) {
	store, _ := gatewayTestStore(t, 2)
	var mu sync.Mutex
	var requestBodies []string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		mu.Lock()
		requestBodies = append(requestBodies, string(body))
		mu.Unlock()
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, successfulSSE())
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client(), BalancedPool: true})
	defer gateway.Close()

	const reusedID = "native-reused-request-id"
	bodies := []string{
		`{"stream":true,"input":[{"role":"user","content":"turn one"}]}`,
		`{"stream":true,"input":[{"role":"user","content":"turn two"}]}`,
	}
	for index, body := range bodies {
		status, responseBody := sendGatewayRequestBody(t, gateway, reusedID, body)
		if status != http.StatusOK || !strings.Contains(responseBody, "response.completed") {
			t.Fatalf("turn %d did not complete: status=%d body=%q", index+1, status, responseBody)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(requestBodies, bodies) {
		t.Fatalf("reused client request id collapsed distinct turns: got=%v want=%v", requestBodies, bodies)
	}
	if len(store.PoolState().ActiveLeases) != 0 {
		t.Fatalf("distinct turns retained active leases: %#v", store.PoolState().ActiveLeases)
	}
}

func TestForwardSSECanceledClientDoesNotMarkRecovery(t *testing.T) {
	store, _ := gatewayTestStore(t, 2)
	lease, err := store.AcquirePoolLease(state.PoolLease{
		LeaseID: "client-cancel", LogicalTurnID: "client-cancel-turn", ThreadID: "client-cancel-thread",
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://relay.invalid/v1/responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	event := []byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"visible\"}\n\n")
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       &cancelAfterEventBody{reader: bytes.NewReader(event), cancel: cancel},
		Request:    request,
	}
	transport := &Transport{Store: store}
	_, streamErr := transport.forwardSSE(httptest.NewRecorder(), response, lease)
	if !errors.Is(streamErr, errClientStreamCanceled) {
		t.Fatalf("stream error=%v, want client cancellation", streamErr)
	}
	current := store.PoolState().ActiveLeases[lease.LeaseID]
	if current.State == state.PoolLeaseRecoveryRequired {
		t.Fatalf("client cancellation fabricated recovery state: %#v", current)
	}
	if task := store.TaskRecords()[lease.ThreadID]; task.RecoveryState != "" {
		t.Fatalf("client cancellation fabricated task recovery: %#v", task)
	}
}

func TestTransportRetriesMessageOnlyStreamingUsageLimit(t *testing.T) {
	store, ids := gatewayTestStore(t, 2)
	var used []string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		account := request.Header.Get("ChatGPT-Account-ID")
		used = append(used, account)
		writer.Header().Set("Content-Type", "text/event-stream")
		if account == "upstream-1" {
			// This is the provider shape that previously slipped through the
			// classifier: HTTP 200 plus a response.failed event with only a
			// human-readable usage-limit message and no machine code.
			_, _ = io.WriteString(writer, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"You've hit your usage limit. Try again later.\"}}}\n\n")
			return
		}
		_, _ = io.WriteString(writer, successfulSSE())
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client()})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "message-only-stream-quota")
	if status != http.StatusOK || !strings.Contains(body, "response.completed") || fmt.Sprint(used) != "[upstream-1 upstream-2]" {
		t.Fatalf("message-only stream quota was not retried: status=%d used=%v body=%q", status, used, body)
	}
	p := store.PoolState()
	if p.Sources[ids[0]].MembershipState != state.SourceDepleted || p.ActiveSourceID != ids[1] {
		t.Fatalf("message-only stream quota did not quarantine first source: %#v", p)
	}
	if p.LastError != nil {
		t.Fatalf("successful fallback retained a stale pool error: %#v", p.LastError)
	}
}

func TestTransportRetriesCodexMessagesLimitMessage(t *testing.T) {
	store, ids := gatewayTestStore(t, 2)
	var used []string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		account := request.Header.Get("ChatGPT-Account-ID")
		used = append(used, account)
		writer.Header().Set("Content-Type", "text/event-stream")
		if account == "upstream-1" {
			// The native Codex UI presents this provider shape as “You're out of
			// Codex messages”. It carries no usage_limit/rate_limit token, so it
			// must still be recognized as a pool-quota rejection.
			_, _ = io.WriteString(writer, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"You're out of Codex messages. Your rate limit resets at 6:17 PM.\"}}}\n\n")
			return
		}
		_, _ = io.WriteString(writer, successfulSSE())
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client()})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "codex-message-limit")
	if status != http.StatusOK || !strings.Contains(body, "response.completed") || fmt.Sprint(used) != "[upstream-1 upstream-2]" {
		t.Fatalf("Codex message-limit stream was not retried: status=%d used=%v body=%q", status, used, body)
	}
	p := store.PoolState()
	if p.Sources[ids[0]].MembershipState != state.SourceDepleted || p.ActiveSourceID != ids[1] || p.LastError != nil {
		t.Fatalf("Codex message-limit failover state is wrong: %#v", p)
	}
}

func TestTransportAcceptsChunkedSSEWithoutContentType(t *testing.T) {
	store, ids := gatewayTestStore(t, 2)
	var used []string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		account := request.Header.Get("ChatGPT-Account-ID")
		used = append(used, account)
		if account == "upstream-1" {
			http.Error(writer, `{"error":{"code":"usage_limit"}}`, http.StatusTooManyRequests)
			return
		}
		// Current ChatGPT Responses can send chunked SSE while omitting the
		// Content-Type header. The gateway must sniff the prefix and still
		// stream the response through the same logical lease.
		_, _ = io.WriteString(writer, successfulSSE())
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client()})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "missing-sse-content-type")
	if status != http.StatusOK || !strings.Contains(body, "response.completed") || fmt.Sprint(used) != "[upstream-1 upstream-2]" {
		t.Fatalf("chunked SSE without content type was not streamed after failover: status=%d used=%v body=%q", status, used, body)
	}
	pool := store.PoolState()
	if pool.Sources[ids[0]].MembershipState != state.SourceDepleted || pool.ActiveSourceID != ids[1] || pool.LastError != nil {
		t.Fatalf("missing-content-type SSE failover state is wrong: %#v", pool)
	}
}

func TestTransportRetriesJSONQuotaErrorWithSuccessfulHTTPStatus(t *testing.T) {
	store, ids := gatewayTestStore(t, 2)
	var used []string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		account := request.Header.Get("ChatGPT-Account-ID")
		used = append(used, account)
		if account == "upstream-1" {
			// This is a raw JSON response.failed envelope with no SSE header;
			// newer provider revisions have emitted this shape on HTTP 200.
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"type":"response.failed","response":{"error":{"type":"usage_limit_exceeded","message":"You're out of Codex messages"}}}`)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, successfulSSE())
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client()})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "json-message-limit")
	if status != http.StatusOK || !strings.Contains(body, "response.completed") || fmt.Sprint(used) != "[upstream-1 upstream-2]" {
		t.Fatalf("successful-HTTP JSON quota error was not retried: status=%d used=%v body=%q", status, used, body)
	}
	p := store.PoolState()
	if p.Sources[ids[0]].MembershipState != state.SourceDepleted || p.ActiveSourceID != ids[1] {
		t.Fatalf("successful-HTTP JSON quota failover state is wrong: %#v", p)
	}
}

func TestTransportSanitizesNonQuotaUpstreamErrors(t *testing.T) {
	store, _ := gatewayTestStore(t, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, `{"error":{"message":"secret upstream diagnostic","account":"upstream-1"}}`, http.StatusInternalServerError)
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client()})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "sanitized-upstream")
	if status != http.StatusBadGateway {
		t.Fatalf("status=%d body=%q", status, body)
	}
	if !strings.Contains(body, "upstream_http_500") || !strings.Contains(body, "reference RP-") {
		t.Fatalf("safe HTTP status was not exposed: %s", body)
	}
	for _, forbidden := range []string{"secret upstream diagnostic", "upstream-1"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("sanitized error leaked %q: %s", forbidden, body)
		}
	}
	lastError := store.PoolState().LastError
	if lastError == nil || lastError.Code != "retry_budget_exhausted" || lastError.HTTPStatus != http.StatusBadGateway || !strings.Contains(lastError.Message, "upstream_http_500") {
		t.Fatalf("terminal upstream error was not recorded for diagnostics: %#v", lastError)
	}
}

func TestTransportIncludesSafeUpstreamErrorCode(t *testing.T) {
	store, _ := gatewayTestStore(t, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, `{"error":{"code":"invalid_request","message":"secret upstream diagnostic"}}`, http.StatusBadRequest)
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(&Transport{Store: store, UpstreamURL: upstream.URL, Client: upstream.Client()})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "safe-upstream-code")
	if status != http.StatusBadGateway || !strings.Contains(body, "HTTP 400") || !strings.Contains(body, "invalid_request") {
		t.Fatalf("safe upstream code missing: status=%d body=%q", status, body)
	}
	if strings.Contains(body, "secret upstream diagnostic") {
		t.Fatalf("upstream message leaked: %q", body)
	}
}

func TestTransportDoesNotLeakCredentialInPoolError(t *testing.T) {
	store, _ := gatewayTestStore(t, 1)
	_, err := store.UpdateCredentialSource("primary", func(source *state.CredentialSourceState) error {
		source.MembershipState = state.SourceDepleted
		source.QuotaEvidence.LimitReached = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(&Transport{Store: store})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "empty")
	if status != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%q", status, body)
	}
	var decoded map[string]any
	if json.Unmarshal([]byte(body), &decoded) != nil {
		t.Fatal("pool error is not JSON")
	}
	for _, forbidden := range []string{"primary", "token-1", "upstream-1", "Subscription"} {
		if bytes.Contains([]byte(body), []byte(forbidden)) {
			t.Fatalf("pool error leaked %q: %s", forbidden, body)
		}
	}
}
