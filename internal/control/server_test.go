package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/LightHaru/codex-relay/internal/mux"
	"github.com/LightHaru/codex-relay/internal/state"
)

func testServer(t *testing.T) *Server {
	server, _ := testServerWithStore(t)
	return server
}

func testServerWithStore(t *testing.T) (*Server, *state.Store) {
	t.Helper()
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	multiplexer, err := mux.New(mux.Options{
		RealExecutable: "codex-test-helper",
		Store:          store,
		Output:         bytes.NewBuffer(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	return New("127.0.0.1:0", "test-token", multiplexer, false), store
}

func requestWithToken(method, path string, body []byte) *http.Request {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("X-Codex-Mux-Token", "test-token")
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestAccountManagementRoutesProtectPrimaryAndUnknownAccounts(t *testing.T) {
	server := testServer(t)

	delete := httptest.NewRecorder()
	server.accountAction(delete, requestWithToken(http.MethodDelete, "/v1/accounts/primary", []byte(`{"force":true}`)))
	if delete.Code != http.StatusBadRequest {
		t.Fatalf("deleting current Primary returned %d, want 400: %s", delete.Code, delete.Body.String())
	}

	primary := httptest.NewRecorder()
	server.accountAction(primary, requestWithToken(http.MethodPost, "/v1/accounts/missing/primary", []byte(`{}`)))
	if primary.Code != http.StatusBadRequest {
		t.Fatalf("selecting unknown Primary returned %d, want 400: %s", primary.Code, primary.Body.String())
	}
}

func TestUsageRouteRequiresRouterTokenAndGET(t *testing.T) {
	server := testServer(t)

	unauthorized := httptest.NewRecorder()
	server.usage(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/usage", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized usage returned %d, want 401: %s", unauthorized.Code, unauthorized.Body.String())
	}

	wrongMethod := httptest.NewRecorder()
	server.usage(wrongMethod, requestWithToken(http.MethodPost, "/v1/usage", nil))
	if wrongMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST usage returned %d, want 405: %s", wrongMethod.Code, wrongMethod.Body.String())
	}
}

func TestUsageAllRouteRequiresRouterTokenAndGET(t *testing.T) {
	server := testServer(t)

	unauthorized := httptest.NewRecorder()
	server.usageAll(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/usage/all", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized usage/all returned %d, want 401: %s", unauthorized.Code, unauthorized.Body.String())
	}

	wrongMethod := httptest.NewRecorder()
	server.usageAll(wrongMethod, requestWithToken(http.MethodPost, "/v1/usage/all", nil))
	if wrongMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST usage/all returned %d, want 405: %s", wrongMethod.Code, wrongMethod.Body.String())
	}
}

func TestRoutingPolicyAndThreadRouteAPIsAreTokenProtected(t *testing.T) {
	server := testServer(t)

	unauthorized := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/router/status", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized router status returned %d", unauthorized.Code)
	}

	policy := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(policy, requestWithToken(http.MethodPut, "/v1/routing/policy", []byte(`{"policy":"rotate-completed-turn"}`)))
	if policy.Code != http.StatusOK {
		t.Fatalf("set routing policy returned %d: %s", policy.Code, policy.Body.String())
	}

	invalid := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(invalid, requestWithToken(http.MethodPut, "/v1/routing/policy", []byte(`{"policy":"random"}`)))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid policy returned %d, want 400", invalid.Code)
	}

	status := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(status, requestWithToken(http.MethodGet, "/v1/router/status", nil))
	if status.Code != http.StatusOK {
		t.Fatalf("router status returned %d: %s", status.Code, status.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(status.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["policy"] != "rotate-completed-turn" || decoded["stateVersion"] != float64(2) {
		t.Fatalf("unexpected router status: %#v", decoded)
	}
	if decoded["effectivePolicy"] != "sticky" || decoded["handoffSupported"] != false || decoded["compatibilityProfile"] != "unknown" {
		t.Fatalf("unknown compatibility profile did not fail closed: %#v", decoded)
	}

	missing := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(missing, requestWithToken(http.MethodGet, "/v1/thread-route?threadId=missing", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing route returned %d, want 404", missing.Code)
	}
}

func TestRoutingDecisionLimitFilterAndValidation(t *testing.T) {
	server := testServer(t)
	for _, policy := range []string{"sticky", "balanced", "rotate-completed-turn"} {
		response := httptest.NewRecorder()
		server.http.Handler.ServeHTTP(response, requestWithToken(http.MethodPut, "/v1/routing/policy", []byte(`{"policy":"`+policy+`"}`)))
		if response.Code != http.StatusOK {
			t.Fatalf("set policy %q returned %d: %s", policy, response.Code, response.Body.String())
		}
	}

	limited := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(limited, requestWithToken(http.MethodGet, "/v1/routing/decisions?limit=1", nil))
	if limited.Code != http.StatusOK {
		t.Fatalf("limited decisions returned %d: %s", limited.Code, limited.Body.String())
	}
	var payload struct {
		Decisions []state.RoutingDecision `json:"decisions"`
	}
	if err := json.Unmarshal(limited.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Decisions) != 1 || payload.Decisions[0].Policy != state.RoutingPolicyRotate {
		t.Fatalf("limited decisions = %#v", payload.Decisions)
	}

	filtered := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(filtered, requestWithToken(http.MethodGet, "/v1/routing/decisions?threadId=missing&limit=20", nil))
	if filtered.Code != http.StatusOK {
		t.Fatalf("filtered decisions returned %d: %s", filtered.Code, filtered.Body.String())
	}
	if err := json.Unmarshal(filtered.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Decisions) != 0 {
		t.Fatalf("thread filter leaked unrelated decisions: %#v", payload.Decisions)
	}

	for _, query := range []string{"limit=0", "limit=201", "limit=not-a-number"} {
		response := httptest.NewRecorder()
		server.http.Handler.ServeHTTP(response, requestWithToken(http.MethodGet, "/v1/routing/decisions?"+query, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s returned %d, want 400", query, response.Code)
		}
	}
}

func TestThreadRouteAPIRedactsCanonicalRolloutPath(t *testing.T) {
	server, store := testServerWithStore(t)
	if err := store.PutThreadRoute(state.ThreadRoute{ThreadID: "thread-private", AccountID: "primary", Generation: 3}); err != nil {
		t.Fatal(err)
	}
	secretPath := `C:\Users\Private\workspace\rollout.jsonl`
	if err := store.PutCheckpoint(state.CanonicalCheckpoint{ThreadID: "thread-private", Generation: 3, RolloutPath: secretPath, HistorySHA256: "abc", HistorySize: 42}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(response, requestWithToken(http.MethodGet, "/v1/thread-route?threadId=thread-private", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("thread route returned %d: %s", response.Code, response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte(secretPath)) {
		t.Fatalf("thread route API leaked rollout path: %s", response.Body.String())
	}
	var payload mux.ThreadRouteStatus
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Checkpoint == nil || payload.Checkpoint.RolloutPath != "" || payload.Checkpoint.HistorySHA256 != "abc" {
		t.Fatalf("unexpected sanitized checkpoint: %#v", payload.Checkpoint)
	}
}

func TestSecurityHeadersAllowPackagedRendererOrigins(t *testing.T) {
	server := testServer(t)

	tests := []struct {
		name            string
		origin          string
		wantAllowOrigin string
	}{
		{name: "electron app origin", origin: "app://-", wantAllowOrigin: "app://-"},
		{name: "electron app origin with slash", origin: "app://-/", wantAllowOrigin: "app://-/"},
		{name: "file renderer opaque origin", origin: "null", wantAllowOrigin: "null"},
		{name: "file scheme compatibility", origin: "file://", wantAllowOrigin: "file://"},
		{name: "untrusted web origin", origin: "https://example.test", wantAllowOrigin: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
			request.Header.Set("Origin", test.origin)
			recorder := httptest.NewRecorder()
			server.http.Handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("health returned %d, want 200: %s", recorder.Code, recorder.Body.String())
			}
			if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != test.wantAllowOrigin {
				t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, test.wantAllowOrigin)
			}
			if test.wantAllowOrigin != "" {
				if got := recorder.Header().Get("Access-Control-Allow-Private-Network"); got != "true" {
					t.Fatalf("Access-Control-Allow-Private-Network = %q, want %q", got, "true")
				}
			}
		})
	}
}

func TestSecurityHeadersHandleRendererPreflight(t *testing.T) {
	server := testServer(t)
	request := httptest.NewRequest(http.MethodOptions, "/v1/usage", nil)
	request.Header.Set("Origin", "null")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	request.Header.Set("Access-Control-Request-Headers", "x-codex-mux-token")
	recorder := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("preflight returned %d, want 204", recorder.Code)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "null" {
		t.Fatalf("preflight Access-Control-Allow-Origin = %q, want %q", got, "null")
	}
	if got := recorder.Header().Get("Access-Control-Allow-Private-Network"); got != "true" {
		t.Fatalf("preflight Access-Control-Allow-Private-Network = %q, want %q", got, "true")
	}
}
