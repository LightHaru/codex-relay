package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LightHaru/codex-relay/internal/state"
)

func pointer(value bool) *bool { return &value }

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

func sendGatewayRequest(t *testing.T, server *httptest.Server, id string) (int, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/responses", bytes.NewBufferString(`{"stream":true,"input":[]}`))
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

func TestTransportRequiresItsLocalBearerToken(t *testing.T) {
	store, _ := gatewayTestStore(t, 1)
	gateway := httptest.NewServer(&Transport{Store: store, LocalBearerToken: "private-local-token"})
	defer gateway.Close()
	status, body := sendGatewayRequest(t, gateway, "unauthorized")
	if status != http.StatusUnauthorized || strings.Contains(body, "private-local-token") {
		t.Fatalf("status=%d body=%q", status, body)
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
	lease := store2.PoolState().ActiveLeases["late"]
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
	if !strings.Contains(body, "HTTP 500") {
		t.Fatalf("safe HTTP status was not exposed: %s", body)
	}
	for _, forbidden := range []string{"secret upstream diagnostic", "upstream-1"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("sanitized error leaked %q: %s", forbidden, body)
		}
	}
	lastError := store.PoolState().LastError
	if lastError == nil || lastError.Code != "upstream_http_error" || lastError.HTTPStatus != http.StatusBadGateway || !strings.Contains(lastError.Message, "HTTP 500") {
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
