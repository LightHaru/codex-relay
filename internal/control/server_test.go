package control

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/LightHaru/codex-relay/internal/mux"
	"github.com/LightHaru/codex-relay/internal/state"
)

func testServer(t *testing.T) *Server {
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
	return New("127.0.0.1:0", "test-token", multiplexer, false)
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
