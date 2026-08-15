package mux

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFetchProfileImageURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Fatalf("unexpected authorization header %q", got)
		}
		if got := request.Header.Get("ChatGPT-Account-ID"); got != "account-123" {
			t.Fatalf("unexpected account header %q", got)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"profile":{"profile_picture_url":"https://example.com/wanted.png"}
		}`))
	}))
	defer server.Close()

	authPath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(authPath, []byte(`{
		"tokens":{"access_token":"secret-token","account_id":"account-123"}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	imageURL, err := fetchProfileImageURL(
		context.Background(),
		server.Client(),
		server.URL,
		authPath,
	)
	if err != nil {
		t.Fatal(err)
	}
	if imageURL != "https://example.com/wanted.png" {
		t.Fatalf("unexpected image URL %q", imageURL)
	}
}

func TestValidatedProfileImageURLRejectsNonHTTPS(t *testing.T) {
	if _, err := validatedProfileImageURL("http://example.com/avatar.png"); err == nil {
		t.Fatal("expected an insecure profile URL to be rejected")
	}
}
