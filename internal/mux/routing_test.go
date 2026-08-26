package mux

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/LightHaru/codex-relay/internal/backend"
	"github.com/LightHaru/codex-relay/internal/protocol"
	"github.com/LightHaru/codex-relay/internal/state"
)

func TestIsUsageLimitResponseRecognizesStructuredError(t *testing.T) {
	message := protocol.Message{Error: &protocol.RPCError{
		Code:    -32000,
		Message: "turn failed",
		Data:    json.RawMessage(`{"codexErrorInfo":"usage_limit_exceeded"}`),
	}}
	if !isUsageLimitResponse(message) {
		t.Fatal("expected usage-limit error to be recognized")
	}
}

func TestIsUsageLimitResponseIgnoresUnrelatedError(t *testing.T) {
	message := protocol.Message{Error: &protocol.RPCError{
		Code:    -32000,
		Message: "workspace folder is unavailable",
	}}
	if isUsageLimitResponse(message) {
		t.Fatal("unrelated error was misclassified as a usage limit")
	}
}

func TestUsageLimitNotificationRecognizesCurrentCamelCaseTurnError(t *testing.T) {
	message := protocol.Message{
		Method: "turn/completed",
		Params: json.RawMessage(`{
			"threadId":"thread-1",
			"turn":{"id":"turn-1","status":"failed","error":{
				"message":"You've hit your usage limit",
				"codexErrorInfo":"usageLimitExceeded"
			}}
		}`),
	}
	if !isUsageLimitNotification(message) {
		t.Fatal("current app-server usageLimitExceeded notification was not recognized")
	}
}

func TestUsageLimitNotificationRecognizesGoalTerminalErrorWithoutStatus(t *testing.T) {
	message := protocol.Message{
		Method: "turn/completed",
		Params: json.RawMessage(`{
			"threadId":"thread-1",
			"turn":{"id":"turn-1","error":{
				"message":"usage rejected",
				"codex_error_info":"usage_limit_exceeded"
			}}
		}`),
	}
	if !isUsageLimitNotification(message) {
		t.Fatal("goal terminal quota error without status was not recognized")
	}
}

func TestUsageLimitNotificationIgnoresQuotaTextInTurnItems(t *testing.T) {
	message := protocol.Message{
		Method: "turn/completed",
		Params: json.RawMessage(`{
			"threadId":"thread-1",
			"turn":{"id":"turn-1","status":"completed","error":null,
			"items":[{"type":"userMessage","text":"please explain quota routing"}]}
		}`),
	}
	if isUsageLimitNotification(message) {
		t.Fatal("ordinary user quota text was misclassified as an upstream failure")
	}
}

func TestIsModelCapacityResponseRecognizesSelectedModelMessage(t *testing.T) {
	message := protocol.Message{Error: &protocol.RPCError{
		Code:    -32000,
		Message: "Selected model is at capacity. Please try a different model.",
	}}
	if !isModelCapacityResponse(message) {
		t.Fatal("expected selected-model capacity error to be recognized")
	}
}

func TestIsModelCapacityResponseRecognizesStructuredCode(t *testing.T) {
	message := protocol.Message{Error: &protocol.RPCError{
		Code: -32000,
		Data: json.RawMessage(`{"code":"model_at_capacity"}`),
	}}
	if !isModelCapacityResponse(message) {
		t.Fatal("expected model_at_capacity code to be recognized")
	}
}

func TestIsModelCapacityResponseIgnoresUnrelatedCapacity(t *testing.T) {
	message := protocol.Message{Error: &protocol.RPCError{
		Code:    -32000,
		Message: "Workspace capacity is unavailable",
	}}
	if isModelCapacityResponse(message) {
		t.Fatal("unrelated capacity error was misclassified")
	}
}

func TestAllSubscriptionsDepletedUsesActionableMessage(t *testing.T) {
	message := allSubscriptionsDepleted(json.RawMessage(`7`), nil)
	if message.Error == nil || message.Error.Code != -32026 {
		t.Fatalf("unexpected error response: %#v", message)
	}
	if message.Error.Message != "All connected subscriptions are depleted. Add another subscription or wait for usage to reset." {
		t.Fatalf("unexpected depletion message: %q", message.Error.Message)
	}
}

func TestAllSubscriptionsDepletedShowsKnownResetTime(t *testing.T) {
	reset := time.Date(2026, time.August, 16, 10, 30, 0, 0, time.Local).Unix()
	message := allSubscriptionsDepleted(json.RawMessage(`7`), &reset)
	if message.Error == nil {
		t.Fatal("expected an error response")
	}
	want := "All connected subscriptions are depleted. Usage resets on Sunday, 16 August at 10:30 AM."
	if message.Error.Message != want {
		t.Fatalf("unexpected reset message: %q", message.Error.Message)
	}
}

func TestThreadIDParserSupportsReviewedProtocolShapes(t *testing.T) {
	fixtures := []string{
		`{"threadId":"thread-1"}`,
		`{"thread_id":"thread-1"}`,
		`{"conversationId":"thread-1"}`,
		`{"thread":{"id":"thread-1"}}`,
		`{"turn":{"threadId":"thread-1"}}`,
		`{"turn":{"thread":{"id":"thread-1"}}}`,
	}
	for _, fixture := range fixtures {
		if got := threadIDFromAnyParams(json.RawMessage(fixture)); got != "thread-1" {
			t.Fatalf("thread ID from %s = %q", fixture, got)
		}
	}
}

func TestUnifiedManagementErrorsDoNotPublishPublicRouterError(t *testing.T) {
	multiplexer := &Multiplexer{gatewayBaseURL: "http://127.0.0.1:48124/v1", gatewayToken: "test-token"}
	for _, method := range []string{
		"account/read",
		"account/rateLimits/read",
		"app/list",
		"mcpServerStatus/list",
		"plugin/list",
	} {
		if multiplexer.shouldPublishRoutedProtocolError(method) {
			t.Fatalf("unified management method %q should not publish router-error", method)
		}
	}
	if !multiplexer.shouldPublishRoutedProtocolError("turn/start") {
		t.Fatal("task turn failures must remain visible")
	}
}

func TestUnifiedUnscopedStartupErrorDoesNotPublishPublicRouterError(t *testing.T) {
	multiplexer := &Multiplexer{gatewayBaseURL: "http://127.0.0.1:48124/v1", gatewayToken: "test-token"}
	if multiplexer.shouldPublishNotificationProtocolError("") {
		t.Fatal("unscoped unified startup error should not publish router-error")
	}
	if !multiplexer.shouldPublishNotificationProtocolError("thread-1") {
		t.Fatal("thread-scoped task error must remain visible")
	}
}

func TestUnifiedSecondaryManagementResponseReachesRendererWithoutToast(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	multiplexer := &Multiplexer{
		gatewayBaseURL: "http://127.0.0.1:48124/v1",
		gatewayToken:   "test-token",
		store:          store,
		output:         &output,
		externalRoutes: make(map[string]externalRoute),
		events:         make(map[chan Event]struct{}),
	}
	events, unsubscribe := multiplexer.SubscribeEvents()
	defer unsubscribe()
	id := protocol.StringID("app-list-1")
	request := protocol.Request("app/list", id, json.RawMessage(`{}`))
	multiplexer.externalRoutes[protocol.RequestIDKey(id)] = externalRoute{
		accountID: secondary.ID,
		method:    request.Method,
		message:   request,
	}
	failure := protocol.Failure(id, -32600, "background refresh failed")
	raw, err := protocol.Encode(failure)
	if err != nil {
		t.Fatal(err)
	}
	multiplexer.handleInbound(backend.Inbound{AccountID: secondary.ID, Message: failure, Raw: raw})
	if !bytes.Contains(output.Bytes(), raw) {
		t.Fatalf("secondary management response was not forwarded: %s", output.String())
	}
	select {
	case event := <-events:
		t.Fatalf("management response emitted public router-error: %#v", event)
	default:
	}
}
