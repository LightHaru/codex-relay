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
