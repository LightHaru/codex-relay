package mux

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/LightHaru/codex-relay/internal/protocol"
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
