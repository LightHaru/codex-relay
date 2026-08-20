package mux

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
