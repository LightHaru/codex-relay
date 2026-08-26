package mux_test

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

	"github.com/LightHaru/codex-relay/internal/gateway"
	"github.com/LightHaru/codex-relay/internal/mux"
	"github.com/LightHaru/codex-relay/internal/protocol"
	"github.com/LightHaru/codex-relay/internal/state"
)

type protocolSink struct {
	mu      sync.Mutex
	pending bytes.Buffer
	lines   chan []byte
}

func newProtocolSink() *protocolSink { return &protocolSink{lines: make(chan []byte, 256)} }

func (s *protocolSink) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.pending.Write(data)
	for {
		line, err := s.pending.ReadBytes('\n')
		if err != nil {
			_, _ = s.pending.Write(line)
			break
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			s.lines <- append([]byte(nil), trimmed...)
		}
	}
	return len(data), nil
}

func (s *protocolSink) wait(t *testing.T, timeout time.Duration, predicate func(protocol.Message) bool) protocol.Message {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case raw := <-s.lines:
			message, err := protocol.Parse(raw)
			if err == nil && predicate(message) {
				return message
			}
		case <-timer.C:
			t.Fatal("timed out waiting for real app-server protocol message")
		}
	}
}

func writeFakeAuth(t *testing.T, account state.Account, token, upstreamAccount string) {
	t.Helper()
	if err := os.MkdirAll(account.CodexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"tokens": map[string]string{
		"access_token": token, "account_id": upstreamAccount,
	}})
	if err := os.WriteFile(filepath.Join(account.CodexHome, "auth.json"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
}

func realResponseSSE(text string) string {
	responseID, itemID := "resp_relay_e2e", "msg_relay_e2e"
	base := map[string]any{
		"id": responseID, "object": "response", "created_at": time.Now().Unix(), "status": "in_progress",
		"error": nil, "incomplete_details": nil, "instructions": nil, "max_output_tokens": nil,
		"model": "relay-e2e", "output": []any{}, "parallel_tool_calls": true,
		"previous_response_id": nil, "reasoning": map[string]any{"effort": "medium", "summary": nil},
		"store": false, "temperature": nil, "text": map[string]any{"format": map[string]string{"type": "text"}},
		"tool_choice": "auto", "tools": []any{}, "top_p": nil, "truncation": "disabled",
		"usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2,
			"input_tokens_details": map[string]int{"cached_tokens": 0}, "output_tokens_details": map[string]int{"reasoning_tokens": 0}},
		"user": nil, "metadata": map[string]any{},
	}
	item := map[string]any{"id": itemID, "type": "message", "status": "completed", "role": "assistant",
		"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}}
	events := []map[string]any{
		{"type": "response.created", "sequence_number": 0, "response": base},
		{"type": "response.output_item.added", "sequence_number": 1, "output_index": 0,
			"item": map[string]any{"id": itemID, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}}},
		{"type": "response.content_part.added", "sequence_number": 2, "item_id": itemID, "output_index": 0, "content_index": 0,
			"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}},
		{"type": "response.output_text.delta", "sequence_number": 3, "item_id": itemID, "output_index": 0, "content_index": 0, "delta": text, "logprobs": []any{}},
		{"type": "response.output_text.done", "sequence_number": 4, "item_id": itemID, "output_index": 0, "content_index": 0, "text": text, "logprobs": []any{}},
		{"type": "response.output_item.done", "sequence_number": 5, "output_index": 0, "item": item},
	}
	completed := make(map[string]any, len(base))
	for key, value := range base {
		completed[key] = value
	}
	completed["status"] = "completed"
	completed["output"] = []any{item}
	events = append(events, map[string]any{"type": "response.completed", "sequence_number": 6, "response": completed})
	var result strings.Builder
	for _, event := range events {
		encoded, _ := json.Marshal(event)
		fmt.Fprintf(&result, "event: %s\ndata: %s\n\n", event["type"], encoded)
	}
	return result.String()
}

func completedTurnID(params json.RawMessage) string {
	var payload struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(params, &payload) != nil {
		return ""
	}
	return strings.TrimSpace(payload.Turn.ID)
}

func TestUnifiedGatewayUsesOneTaskAuthorityAndFailsOverInsideRequest(t *testing.T) {
	executable := strings.TrimSpace(os.Getenv("CODEX_RELAY_REAL_E2E"))
	if executable == "" {
		t.Skip("set CODEX_RELAY_REAL_E2E to run the real app-server E2E")
	}
	if _, err := os.Stat(executable); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "authority-home"))
	if err != nil {
		t.Fatal(err)
	}
	accounts := store.Accounts()
	for _, label := range []string{"Hidden source B", "Hidden source C", "Hidden source D"} {
		secondary, addErr := store.AddAccount(label)
		if addErr != nil {
			t.Fatal(addErr)
		}
		accounts = append(accounts, secondary)
	}
	for index, account := range accounts {
		letter := string(rune('a' + index))
		writeFakeAuth(t, account, "token-"+letter, "source-"+letter)
	}
	credentialBySource := make(map[string][2]string, len(accounts))
	for index, account := range accounts {
		letter := string(rune('a' + index))
		credentialBySource[account.ID] = [2]string{"token-" + letter, "source-" + letter}
	}
	if err := gateway.PrimeCredentialSources(store); err != nil {
		t.Fatal(err)
	}

	var upstreamMu sync.Mutex
	var upstreamSources []string
	var requestBodies [][]byte
	sourceCalls := make(map[string]int)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		source := request.Header.Get("ChatGPT-Account-ID")
		upstreamMu.Lock()
		upstreamSources = append(upstreamSources, source)
		requestBodies = append(requestBodies, body)
		sourceCalls[source]++
		call := sourceCalls[source]
		upstreamMu.Unlock()
		if source == "source-a" {
			// Exercise the real provider failure shape that motivated this
			// regression test: a successful HTTP response followed by a
			// message-only streaming usage-limit failure.
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(writer, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"You've hit your usage limit. Try again later.\"}}}\n\n")
			return
		}
		if (source == "source-b" || source == "source-c") && call == 3 {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(writer, `{"error":{"code":"usage_limit_reached"}}`)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, realResponseSSE("UNIFIED_POOL_E2E_OK"))
	}))
	defer upstream.Close()
	localToken := "local-e2e-token"
	poolGateway := httptest.NewServer(&gateway.Transport{
		Store: store, UpstreamURL: upstream.URL, Client: upstream.Client(), LocalBearerToken: localToken,
		// The real app-server intentionally rejects these fixture tokens and may
		// rewrite auth.json while warming plugins. Keep transport credentials in
		// memory so the E2E exercises pool routing rather than filesystem races.
		LoadCredentials: func(account state.Account) (string, string, error) {
			values, ok := credentialBySource[account.ID]
			if !ok {
				return "", "", fmt.Errorf("missing fixture credentials for %s", account.ID)
			}
			return values[0], values[1], nil
		},
	})
	defer poolGateway.Close()

	sink := newProtocolSink()
	multiplexer, err := mux.New(mux.Options{
		RealExecutable: executable, RealArgs: []string{"app-server", "--stdio"},
		Environment: os.Environ(), CompatibilityProfile: "real-e2e",
		GatewayBaseURL: poolGateway.URL + "/v1", GatewayToken: localToken,
		Store: store, Output: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := multiplexer.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer multiplexer.Close()

	initializeID := protocol.StringID("initialize-e2e")
	multiplexer.HandleClient(protocol.Request("initialize", initializeID, json.RawMessage(`{"clientInfo":{"name":"relay-e2e","version":"1"},"capabilities":{"experimentalApi":true}}`)))
	initialized := sink.wait(t, 45*time.Second, func(message protocol.Message) bool {
		return protocol.RequestIDKey(message.ID) == protocol.RequestIDKey(initializeID)
	})
	if initialized.Error != nil {
		t.Fatalf("initialize failed: %s", initialized.Error.Message)
	}
	multiplexer.HandleClient(protocol.Message{Method: "initialized"})
	// The fixture credentials are transport-valid but intentionally are not
	// real signed ChatGPT JWTs, so management-only account/read reports them as
	// disconnected. Re-prime after initialization to model the authenticated
	// source state while keeping the E2E entirely offline.
	time.Sleep(time.Second)
	restoreFakeSources := func() {
		for index, account := range accounts {
			current := store.PoolState().Sources[account.ID]
			if current.MembershipState == state.SourceDepleted {
				continue
			}
			letter := string(rune('a' + index))
			writeFakeAuth(t, account, "token-"+letter, "source-"+letter)
			if _, err := store.UpdateCredentialSource(account.ID, func(source *state.CredentialSourceState) error {
				source.Enabled = true
				source.Connected = true
				source.AuthState = "authenticated"
				source.MembershipState = state.SourceAvailable
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	restoreFakeSources()

	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	threadIDRequest := protocol.StringID("thread-e2e")
	threadParams, _ := json.Marshal(map[string]any{"cwd": workspace, "approvalPolicy": "never", "sandbox": "read-only", "ephemeral": true})
	multiplexer.HandleClient(protocol.Request("thread/start", threadIDRequest, threadParams))
	threadResponse := sink.wait(t, 30*time.Second, func(message protocol.Message) bool {
		return protocol.RequestIDKey(message.ID) == protocol.RequestIDKey(threadIDRequest)
	})
	if threadResponse.Error != nil {
		t.Fatalf("thread/start failed: %s", threadResponse.Error.Message)
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(threadResponse.Result, &started); err != nil || started.Thread.ID == "" {
		t.Fatalf("thread/start result: %s err=%v", threadResponse.Result, err)
	}
	// Twenty harmless logical turns make this an actual long-session fixture;
	// the source changes below happen only on structured pre-output quota
	// rejections, never because another source has a higher balance.
	for index := 0; index < 20; index++ {
		restoreFakeSources()
		turnRequestID := protocol.StringID(fmt.Sprintf("turn-e2e-%d", index))
		turnParams, _ := json.Marshal(map[string]any{"threadId": started.Thread.ID,
			"input": []any{map[string]string{"type": "text", "text": fmt.Sprintf("Continue the same long chat, step %d.", index)}},
			"cwd":   workspace, "approvalPolicy": "never"})
		multiplexer.HandleClient(protocol.Request("turn/start", turnRequestID, turnParams))
		turnAccepted := sink.wait(t, 30*time.Second, func(message protocol.Message) bool {
			return protocol.RequestIDKey(message.ID) == protocol.RequestIDKey(turnRequestID)
		})
		if turnAccepted.Error != nil {
			t.Fatalf("turn %d start failed: %s", index, turnAccepted.Error.Message)
		}
		var acceptedPayload struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		_ = json.Unmarshal(turnAccepted.Result, &acceptedPayload)
		turnID := strings.TrimSpace(acceptedPayload.Turn.ID)
		completed := sink.wait(t, 45*time.Second, func(message protocol.Message) bool {
			return message.Method == "turn/completed" && (turnID == "" || completedTurnID(message.Params) == turnID)
		})
		if !bytes.Contains(completed.Params, []byte(`"status":"completed"`)) {
			upstreamMu.Lock()
			observed := append([]string(nil), upstreamSources...)
			upstreamMu.Unlock()
			t.Fatalf("turn %d did not complete: %s upstream=%v pool=%#v", index, completed.Params, observed, store.PoolState())
		}
	}

	upstreamMu.Lock()
	defer upstreamMu.Unlock()
	if len(upstreamSources) != 23 || upstreamSources[0] != "source-a" || upstreamSources[1] != "source-b" ||
		upstreamSources[2] != "source-b" || upstreamSources[3] != "source-b" ||
		upstreamSources[4] != "source-c" || upstreamSources[5] != "source-c" || upstreamSources[6] != "source-c" ||
		upstreamSources[7] != "source-d" {
		t.Fatalf("hidden failover path=%v", upstreamSources)
	}
	for index, source := range upstreamSources {
		if index >= 1 && source == "source-a" || index >= 4 && source == "source-b" || index >= 7 && source == "source-c" {
			t.Fatalf("depleted source was reused at call %d: %v", index, upstreamSources)
		}
	}
	for _, pair := range [][2]int{{0, 1}, {3, 4}, {6, 7}} {
		if len(requestBodies) <= pair[1] || !bytes.Equal(requestBodies[pair[0]], requestBodies[pair[1]]) {
			t.Fatalf("gateway changed logical model request during pre-output failover at calls %v", pair)
		}
	}
	owner, ok := store.ThreadOwner(started.Thread.ID)
	if !ok || owner != accounts[0].ID {
		t.Fatalf("thread authority changed with credential source: owner=%q ok=%v", owner, ok)
	}
	pool := store.PoolState()
	if pool.ActiveSourceID != accounts[3].ID || pool.FailoverCount != 3 {
		t.Fatalf("pool transition=%#v", pool.LastTransition)
	}
	// Give short-lived plugin discovery helpers inherited from the real binary
	// time to release their temporary clone handles before Go removes TempDir.
	multiplexer.Close()
	time.Sleep(2 * time.Second)
}
