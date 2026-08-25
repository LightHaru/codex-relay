package mux

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/LightHaru/codex-relay/internal/state"
)

func TestFetchUsageStatusUsesIsolatedAccountCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Fatalf("method=%s, want GET", request.Method)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer isolated-token" {
			t.Fatalf("unexpected authorization header %q", got)
		}
		if got := request.Header.Get("ChatGPT-Account-ID"); got != "account-usage" {
			t.Fatalf("unexpected account header %q", got)
		}
		if got := request.Header.Get("User-Agent"); got != "Codex Relay" {
			t.Fatalf("unexpected user agent %q", got)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"rate_limit":{"limit_reached":false}}`))
	}))
	defer server.Close()

	authPath := writeUsageAuthFile(t, t.TempDir(), "isolated-token", "account-usage")
	status, err := fetchUsageStatus(context.Background(), server.Client(), server.URL, authPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(status), `{"rate_limit":{"limit_reached":false}}`; got != want {
		t.Fatalf("usage status=%s, want %s", got, want)
	}
}

func TestUsagePayloadAllowedDoesNotOverrideExhaustedWindow(t *testing.T) {
	status := json.RawMessage(`{
		"rate_limit": {
			"allowed": true,
			"limit_reached": false,
			"primary_window": {
				"used_percent": 100,
				"limit_window_seconds": 18000,
				"reset_at": 2000000300
			},
			"secondary_window": {
				"used_percent": 20,
				"limit_window_seconds": 604800,
				"reset_at": 2000600000
			}
		}
	}`)
	if usagePayloadHasCapacity(status) {
		t.Fatal("allowed=true incorrectly overrode a 100%-used quota window")
	}
	signal, ok := decodeUsageQuotaSignal(status)
	if !ok || signal.Allowed == nil || !*signal.Allowed || signal.RateLimits == nil {
		t.Fatalf("usage quota signal was not decoded: %#v", signal)
	}
	if signal.RateLimits.Primary == nil || signal.RateLimits.Primary.WindowDurationMins == nil ||
		*signal.RateLimits.Primary.WindowDurationMins != 300 {
		t.Fatalf("primary window was not converted to minutes: %#v", signal.RateLimits.Primary)
	}
}

func TestUsagePayloadExplicitDenyOverridesRemainingWindow(t *testing.T) {
	status := json.RawMessage(`{
		"rate_limit": {
			"allowed": false,
			"limit_reached": true,
			"primary_window": {"used_percent": 42}
		}
	}`)
	if usagePayloadHasCapacity(status) {
		t.Fatal("explicit usage denial was treated as routable")
	}
}

func TestUsageStatusFallsBackWhenControllerCredentialIsUnavailable(t *testing.T) {
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		token := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		requested = append(requested, token)
		if token == "controller-token" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		if token != "secondary-token" {
			t.Fatalf("unexpected token %q", token)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"rate_limit":{"limit_reached":false},"plan_type":"plus"}`))
	}))
	defer server.Close()

	root := t.TempDir()
	primaryHome := filepath.Join(root, "primary")
	store, err := state.Open(filepath.Join(root, "mux"), primaryHome)
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	writeUsageAuthFile(t, primaryHome, "controller-token", "controller-account")
	writeUsageAuthFile(t, secondary.CodexHome, "secondary-token", "secondary-account")

	multiplexer, err := New(Options{
		RealExecutable: "codex-test-helper",
		Store:          store,
		Output:         bytes.NewBuffer(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	multiplexer.profileClient = server.Client()
	multiplexer.usageEndpoint = server.URL

	status, err := multiplexer.UsageStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(status), `"plan_type":"plus"`) {
		t.Fatalf("unexpected fallback usage status: %s", status)
	}
	if got, want := strings.Join(requested, ","), "controller-token,secondary-token"; got != want {
		t.Fatalf("credential order=%q, want %q", got, want)
	}
}

func TestUsageStatusPrefersConnectedSubscriptionWithCapacity(t *testing.T) {
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		accountID := request.Header.Get("ChatGPT-Account-ID")
		requested = append(requested, accountID)
		response.Header().Set("Content-Type", "application/json")
		if accountID == "controller-account" {
			_, _ = response.Write([]byte(`{"plan_type":"plus","rate_limit":{"allowed":false,"limit_reached":true}}`))
			return
		}
		if accountID != "secondary-account" {
			t.Fatalf("unexpected account %q", accountID)
		}
		_, _ = response.Write([]byte(`{"plan_type":"plus","rate_limit":{"allowed":true,"limit_reached":false}}`))
	}))
	defer server.Close()

	root := t.TempDir()
	primaryHome := filepath.Join(root, "primary")
	store, err := state.Open(filepath.Join(root, "mux"), primaryHome)
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	writeUsageAuthFile(t, primaryHome, "controller-token", "controller-account")
	writeUsageAuthFile(t, secondary.CodexHome, "secondary-token", "secondary-account")

	multiplexer, err := New(Options{
		RealExecutable: "codex-test-helper",
		Store:          store,
		Output:         bytes.NewBuffer(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	multiplexer.profileClient = server.Client()
	multiplexer.usageEndpoint = server.URL

	status, err := multiplexer.UsageStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(status), `"allowed":true`) {
		t.Fatalf("expected a usable subscription Usage payload, got %s", status)
	}
	if got, want := strings.Join(requested, ","), "controller-account,secondary-account"; got != want {
		t.Fatalf("credential order=%q, want %q", got, want)
	}
}

func TestUsageStatusAllReturnsEveryEnabledSubscriptionAndPartialFailures(t *testing.T) {
	var requested []string
	var requestedMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		accountID := request.Header.Get("ChatGPT-Account-ID")
		requestedMu.Lock()
		requested = append(requested, accountID)
		requestedMu.Unlock()
		if accountID == "secondary-account" {
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"plan_type":"plus","credits":{"balance":"12"}}`))
			return
		}
		response.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	root := t.TempDir()
	primaryHome := filepath.Join(root, "primary")
	store, err := state.Open(filepath.Join(root, "mux"), primaryHome)
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	writeUsageAuthFile(t, primaryHome, "controller-token", "primary-account")
	writeUsageAuthFile(t, secondary.CodexHome, "secondary-token", "secondary-account")

	multiplexer, err := New(Options{
		RealExecutable: "codex-test-helper",
		Store:          store,
		Output:         bytes.NewBuffer(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	multiplexer.profileClient = server.Client()
	multiplexer.usageEndpoint = server.URL

	collection, err := multiplexer.UsageStatusAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if collection.RequestedCount != 2 || collection.AvailableCount != 1 || collection.FailedCount != 1 {
		t.Fatalf("unexpected usage summary: %#v", collection)
	}
	if got, want := len(collection.Accounts), 2; got != want {
		t.Fatalf("account count=%d, want %d", got, want)
	}
	if got, want := collection.Accounts[0].AccountID, "primary"; got != want {
		t.Fatalf("first account=%q, want %q", got, want)
	}
	if collection.Accounts[0].Connected != true || collection.Accounts[0].Error == "" {
		t.Fatalf("primary failure was not represented safely: %#v", collection.Accounts[0])
	}
	if got, want := collection.Accounts[1].AccountID, secondary.ID; got != want {
		t.Fatalf("second account=%q, want %q", got, want)
	}
	if !collection.Accounts[1].Connected || collection.Accounts[1].Error != "" {
		t.Fatalf("secondary success was not represented: %#v", collection.Accounts[1])
	}
	var payload map[string]any
	if err := json.Unmarshal(collection.Accounts[1].Usage, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["plan_type"] != "plus" {
		t.Fatalf("secondary payload was not preserved: %#v", payload)
	}
	if len(requested) != 2 {
		t.Fatalf("expected one request per account, got %d", len(requested))
	}
}

func TestUsageStatusAllIncludesUnconnectedAccountWithoutRequest(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = response.Write([]byte(`{}`))
	}))
	defer server.Close()

	root := t.TempDir()
	primaryHome := filepath.Join(root, "primary")
	store, err := state.Open(filepath.Join(root, "mux"), primaryHome)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddAccount("Waiting for sign-in"); err != nil {
		t.Fatal(err)
	}
	writeUsageAuthFile(t, primaryHome, "primary-token", "primary-account")
	multiplexer, err := New(Options{
		RealExecutable: "codex-test-helper",
		Store:          store,
		Output:         bytes.NewBuffer(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	multiplexer.profileClient = server.Client()
	multiplexer.usageEndpoint = server.URL

	collection, err := multiplexer.UsageStatusAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if collection.RequestedCount != 2 || collection.AvailableCount != 1 || collection.FailedCount != 1 {
		t.Fatalf("unexpected usage summary: %#v", collection)
	}
	if collection.Accounts[1].Connected || collection.Accounts[1].Error != "subscription is not connected" {
		t.Fatalf("unconnected account was not represented safely: %#v", collection.Accounts[1])
	}
	if requests != 1 {
		t.Fatalf("unconnected account triggered an upstream request; got %d", requests)
	}
}

func TestFetchUsageStatusRejectsNonObjectPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`[]`))
	}))
	defer server.Close()

	authPath := writeUsageAuthFile(t, t.TempDir(), "token", "account")
	if _, err := fetchUsageStatus(context.Background(), server.Client(), server.URL, authPath); err == nil {
		t.Fatal("expected non-object usage payload to be rejected")
	}
}

func writeUsageAuthFile(t *testing.T, home, token, accountID string) string {
	t.Helper()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "auth.json")
	contents := `{"tokens":{"access_token":"` + token + `","account_id":"` + accountID + `"}}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
