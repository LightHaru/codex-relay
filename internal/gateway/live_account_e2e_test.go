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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LightHaru/codex-relay/internal/state"
)

// TestLiveAccountStickyFailover is intentionally opt-in. It reads only the
// operator-selected source auth files, copies them into temporary isolated
// homes, sends up to three short harmless turns to the real Responses service,
// and never prints credential material or raw upstream output.
func TestLiveAccountStickyFailover(t *testing.T) {
	if os.Getenv("CODEX_RELAY_LIVE_ACCOUNTS") != "1" {
		t.Skip("set CODEX_RELAY_LIVE_ACCOUNTS=1 to run the real-account E2E")
	}
	sourceDirs := splitLiveSourceDirs(os.Getenv("CODEX_RELAY_LIVE_SOURCE_DIRS"))
	if len(sourceDirs) < 2 {
		t.Fatal("CODEX_RELAY_LIVE_SOURCE_DIRS must list at least two ordered source homes; put a known depleted source first")
	}
	for _, sourceDir := range sourceDirs {
		if _, err := os.Stat(filepath.Join(sourceDir, "auth.json")); err != nil {
			t.Fatalf("selected source is missing auth.json: %v", err)
		}
	}

	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "authority-home"))
	if err != nil {
		t.Fatal(err)
	}
	accounts := store.Accounts()
	for index := 1; index < len(sourceDirs); index++ {
		account, addErr := store.AddAccount(fmt.Sprintf("Live source %d", index+1))
		if addErr != nil {
			t.Fatal(addErr)
		}
		accounts = append(accounts, account)
	}
	for index, sourceDir := range sourceDirs {
		auth, readErr := os.ReadFile(filepath.Join(sourceDir, "auth.json"))
		if readErr != nil {
			t.Fatalf("read selected source credential: %v", readErr)
		}
		account := accounts[index]
		if err := os.MkdirAll(account.CodexHome, 0o700); err != nil {
			t.Fatal(err)
		}
		authPath := filepath.Join(account.CodexHome, "auth.json")
		if err := os.WriteFile(authPath, auth, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(authPath, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, updateErr := store.UpdateCredentialSource(account.ID, func(source *state.CredentialSourceState) error {
			source.Connected = true
			source.AuthState = "authenticated"
			source.MembershipState = state.SourceProbation
			source.QuotaEvidence = state.QuotaEvidence{}
			return nil
		}); updateErr != nil {
			t.Fatal(updateErr)
		}
	}

	token := "live-local-test-token"
	transport := &Transport{Store: store, LocalBearerToken: token, Client: &http.Client{Timeout: 3 * time.Minute}}
	server := httptest.NewServer(transport)
	defer server.Close()
	model := strings.TrimSpace(os.Getenv("CODEX_RELAY_LIVE_MODEL"))
	if model == "" {
		// Use a model exposed by current Windows Codex builds. Operators may
		// override this when their account has a different entitlement.
		model = "gpt-5.6-terra"
	}
	threadID := "live-relay-thread"
	for turn := 1; turn <= 3; turn++ {
		status, body := sendLiveTurn(t, server.URL, token, model, threadID, turn)
		if status != http.StatusOK || !strings.Contains(body, "response.completed") {
			t.Fatalf("live turn %d did not complete; status=%d pool=%s", turn, status, sanitizedLivePool(store.PoolState()))
		}
	}

	pool := store.PoolState()
	if pool.FailoverCount == 0 || pool.ActiveSourceID != pool.SourceOrder[1] {
		t.Fatalf("LIVE PENDING: first selected source was not rejected before the next source; pool=%s", sanitizedLivePool(pool))
	}
	if pool.Sources[pool.SourceOrder[0]].MembershipState != state.SourceDepleted {
		t.Fatalf("LIVE PENDING: first source was not marked depleted; pool=%s", sanitizedLivePool(pool))
	}
	if pool.Sources[pool.SourceOrder[1]].MembershipState != state.SourceActive {
		t.Fatalf("live continuation did not remain sticky on the next source; pool=%s", sanitizedLivePool(pool))
	}
	t.Logf("LIVE PASS: turns=3 failovers=%d activeSourceOrdinal=2 health=%s", pool.FailoverCount, pool.Health)
}

func splitLiveSourceDirs(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == '\n' || r == '\r' })
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func sendLiveTurn(t *testing.T, endpoint, token, model, threadID string, turn int) (int, string) {
	t.Helper()
	payload := map[string]any{
		"model": model, "stream": true,
		"input": []any{map[string]any{"role": "user", "content": []any{map[string]any{
			"type": "input_text", "text": "Reply with exactly OK. Live Relay smoke turn " + strconv.Itoa(turn),
		}}}},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint+"/v1/responses", bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Session-Id", "live-relay-session")
	request.Header.Set("Thread-Id", threadID)
	request.Header.Set("X-Client-Request-Id", fmt.Sprintf("live-relay-turn-%d", turn))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(body)
}

func sanitizedLivePool(pool state.PoolState) string {
	return fmt.Sprintf("pool=%s revision=%d health=%s failovers=%d sourceOrdinal=%d", state.DefaultPoolID, pool.Revision, pool.Health, pool.FailoverCount, sourceOrdinal(pool))
}

func sourceOrdinal(pool state.PoolState) int {
	for index, sourceID := range pool.SourceOrder {
		if sourceID == pool.ActiveSourceID {
			return index + 1
		}
	}
	return 0
}
