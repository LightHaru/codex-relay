package mux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LightHaru/codex-relay/internal/backend"
	"github.com/LightHaru/codex-relay/internal/protocol"
	"github.com/LightHaru/codex-relay/internal/state"
)

func newCoordinatorTestMux(t *testing.T) (*Multiplexer, *state.Store) {
	t.Helper()
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	multiplexer, err := New(Options{RealExecutable: "fake", Store: store, Output: bytes.NewBuffer(nil)})
	if err != nil {
		t.Fatal(err)
	}
	return multiplexer, store
}

func TestUnifiedTaskAuthorityFollowsSelectedController(t *testing.T) {
	multiplexer, store := newCoordinatorTestMux(t)
	secondary, err := store.AddAccount("Aira Agent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetController(secondary.ID); err != nil {
		t.Fatal(err)
	}
	multiplexer.gatewayBaseURL = "http://127.0.0.1:48124/v1"
	multiplexer.gatewayToken = "relay-test-token"

	if got := multiplexer.taskAuthorityID(); got != secondary.ID {
		t.Fatalf("unified task authority=%q, want selected controller %q", got, secondary.ID)
	}
	accounts := multiplexer.store.Accounts()
	for _, account := range accounts {
		if account.ID == secondary.ID && !account.Controller {
			t.Fatalf("selected controller lost its Controller flag: %#v", account)
		}
	}
}

func TestUnifiedTaskAuthorityDoesNotSilentlyFallbackWhenSelectedControllerIsDisabled(t *testing.T) {
	multiplexer, store := newCoordinatorTestMux(t)
	secondary, err := store.AddAccount("Aira Agent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetController(secondary.ID); err != nil {
		t.Fatal(err)
	}
	multiplexer.gatewayBaseURL = "http://127.0.0.1:48124/v1"
	multiplexer.gatewayToken = "relay-test-token"
	disabled := false
	if _, err := store.UpdateAccount(secondary.ID, nil, &disabled); err != nil {
		t.Fatal(err)
	}
	if got := multiplexer.taskAuthorityID(); got != "" {
		t.Fatalf("disabled selected authority silently fell back to %q", got)
	}
}

func TestSafeProtocolErrorKeepsRelayDetailButRedactsArbitraryText(t *testing.T) {
	detail, code := safeProtocolError(protocol.Failure(protocol.StringID("request"), -32001, "Relay Pool model service rejected the request (HTTP 503)."))
	if detail != "Relay Pool model service rejected the request (HTTP 503)." || code != -32001 {
		t.Fatalf("Relay detail was not preserved: detail=%q code=%d", detail, code)
	}
	detail, code = safeProtocolError(protocol.Failure(protocol.StringID("request"), -32002, "secret upstream diagnostic C:\\Users\\ADMIN\\.codex\\auth.json"))
	if detail != "the selected subscription must sign in again" || code != -32002 || strings.Contains(detail, "auth.json") {
		t.Fatalf("arbitrary protocol error was not reduced safely: detail=%q code=%d", detail, code)
	}
}

func TestUnifiedProtocolErrorUsesRecentPoolCauseForNative32600(t *testing.T) {
	multiplexer, store := newCoordinatorTestMux(t)
	multiplexer.gatewayBaseURL = "http://127.0.0.1:48124/v1"
	multiplexer.gatewayToken = "relay-test-token"
	if err := store.RecordPoolError("pool_exhausted", 429, "Relay Pool has exhausted every usable quota source"); err != nil {
		t.Fatal(err)
	}
	events, unsubscribe := multiplexer.SubscribeEvents()
	defer unsubscribe()
	multiplexer.publishProtocolError(protocol.Message{Method: "turn/completed", Params: []byte(`{"turn":{"error":{"code":-32600,"message":"exceeded retry limit, last status: 429 Too Many Requests"}}}`)}, "thread-1")
	select {
	case event := <-events:
		if event.Type != "router-error" || !strings.Contains(event.Message, "Relay Pool has exhausted every usable quota source") {
			t.Fatalf("native wrapper hid recent pool cause: %#v", event)
		}
		data, ok := event.Data.(map[string]any)
		if !ok || data["poolCode"] != "pool_exhausted" || data["httpStatus"] != 429 {
			t.Fatalf("pool diagnostics were not attached: %#v", event.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for router-error event")
	}
}

func TestTurnCoordinatorSerializesOneActiveTurnPerThread(t *testing.T) {
	multiplexer, store := newCoordinatorTestMux(t)
	if err := store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	message := protocol.Request("turn/start", protocol.StringID("turn-1"), []byte(`{"threadId":"thread-1"}`))
	active, err := multiplexer.beginTurnAttempt("thread-1", "primary", message)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := multiplexer.beginTurnAttempt("thread-1", "primary", message); !errors.Is(err, errTurnAlreadyActive) {
		t.Fatalf("parallel turn error = %v, want %v", err, errTurnAlreadyActive)
	}
	multiplexer.completeActiveTurn("thread-1", "primary")
	if attempt, ok := store.TurnAttempt(active.attemptID); !ok || attempt.Phase != "COMPLETED" {
		t.Fatalf("completed attempt was not journaled: %#v ok=%v", attempt, ok)
	}
	if _, err := multiplexer.beginTurnAttempt("thread-1", "primary", message); err != nil {
		t.Fatalf("next completed-boundary turn was rejected: %v", err)
	}
}

func TestQuotaFailureAfterSideEffectsRequiresRecoveryInsteadOfRetry(t *testing.T) {
	multiplexer, store := newCoordinatorTestMux(t)
	if err := store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	message := protocol.Request("turn/start", protocol.StringID("turn-1"), []byte(`{"threadId":"thread-1"}`))
	active, err := multiplexer.beginTurnAttempt("thread-1", "primary", message)
	if err != nil {
		t.Fatal(err)
	}
	multiplexer.markAccountSideEffects("primary", "item/commandExecution/requestApproval", []byte(`{"threadId":"thread-1"}`))
	marked, ok := store.TurnAttempt(active.attemptID)
	if !ok || !marked.SideEffectsStarted {
		t.Fatalf("side effect boundary was not persisted: %#v ok=%v", marked, ok)
	}
	failed, ok := multiplexer.takeActiveTurnForFailure("thread-1", "primary")
	if !ok || !failed.sideEffectsStarted {
		t.Fatalf("active side-effect attempt was not claimed: %#v ok=%v", failed, ok)
	}
	multiplexer.requireTurnRecovery("thread-1", failed, "quota exhausted after side effects")
	route, _ := store.ThreadRoute("thread-1")
	if !route.RecoveryRequired || route.ActiveAttemptID != "" {
		t.Fatalf("route did not fail closed for recovery: %#v", route)
	}
	if _, err := multiplexer.beginTurnAttempt("thread-1", "primary", message); !errors.Is(err, errRecoveryRequired) {
		t.Fatalf("new turn error = %v, want recovery required", err)
	}
}

func TestAsyncQuotaNotificationAfterSideEffectDoesNotCreateHandoff(t *testing.T) {
	multiplexer, store := newCoordinatorTestMux(t)
	var output bytes.Buffer
	multiplexer.output = &output
	if err := store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	message := protocol.Request("turn/start", protocol.StringID("turn-1"), []byte(`{"threadId":"thread-1"}`))
	if _, err := multiplexer.beginTurnAttempt("thread-1", "primary", message); err != nil {
		t.Fatal(err)
	}
	multiplexer.markAccountSideEffects("primary", "item/commandExecution/requestApproval", []byte(`{"threadId":"thread-1"}`))
	failure := protocol.Message{Method: "error", Params: []byte(`{"threadId":"thread-1","error":{"message":"usage limit","codexErrorInfo":"UsageLimitExceeded"}}`)}
	raw, err := protocol.Encode(failure)
	if err != nil {
		t.Fatal(err)
	}
	multiplexer.handleInbound(backend.Inbound{AccountID: "primary", Message: failure, Raw: raw})
	route, _ := store.ThreadRoute("thread-1")
	if !route.RecoveryRequired {
		t.Fatalf("quota after side effect did not require recovery: %#v", route)
	}
	if handoffs := store.Handoffs(); len(handoffs) != 0 {
		t.Fatalf("unsafe automatic handoff was created: %#v", handoffs)
	}
	if !bytes.Contains(output.Bytes(), []byte("usage limit")) {
		t.Fatalf("original terminal error was not forwarded: %s", output.String())
	}
}

func TestAsyncQuotaNotificationAfterVisibleOutputRequiresRecovery(t *testing.T) {
	multiplexer, store := newCoordinatorTestMux(t)
	var output bytes.Buffer
	multiplexer.output = &output
	if err := store.SetThreadOwner("thread-visible", "primary"); err != nil {
		t.Fatal(err)
	}
	message := protocol.Request("turn/start", protocol.StringID("turn-visible"), []byte(`{"threadId":"thread-visible"}`))
	active, err := multiplexer.beginTurnAttempt("thread-visible", "primary", message)
	if err != nil {
		t.Fatal(err)
	}
	multiplexer.markVisibleOutput("primary", "item/completed", []byte(`{"threadId":"thread-visible","item":{"type":"agentMessage"}}`))
	marked, ok := store.TurnAttempt(active.attemptID)
	if !ok || marked.FirstVisibleOutputAt == 0 {
		t.Fatalf("visible output boundary was not persisted: %#v ok=%v", marked, ok)
	}
	failure := protocol.Message{Method: "error", Params: []byte(`{"threadId":"thread-visible","error":{"message":"usage limit","codexErrorInfo":"UsageLimitExceeded"}}`)}
	raw, err := protocol.Encode(failure)
	if err != nil {
		t.Fatal(err)
	}
	multiplexer.handleInbound(backend.Inbound{AccountID: "primary", Message: failure, Raw: raw})
	route, _ := store.ThreadRoute("thread-visible")
	if !route.RecoveryRequired || route.ActiveAttemptID != "" {
		t.Fatalf("quota after visible output did not fail closed: %#v", route)
	}
	if handoffs := store.Handoffs(); len(handoffs) != 0 {
		t.Fatalf("visible output was replayed through a handoff: %#v", handoffs)
	}
	if !bytes.Contains(output.Bytes(), []byte("usage limit")) {
		t.Fatalf("original terminal error was not forwarded: %s", output.String())
	}
}

func TestUnifiedGatewayRecoveryMarkerDoesNotCompleteTurn(t *testing.T) {
	multiplexer, store := newCoordinatorTestMux(t)
	var output bytes.Buffer
	multiplexer.output = &output
	multiplexer.gatewayBaseURL = "http://127.0.0.1:1/v1"
	multiplexer.gatewayToken = "local-token"
	if err := store.SetThreadOwner("thread-unified-recovery", "primary"); err != nil {
		t.Fatal(err)
	}
	message := protocol.Request("turn/start", protocol.StringID("turn-unified-recovery"), []byte(`{"threadId":"thread-unified-recovery"}`))
	active, err := multiplexer.beginTurnAttempt("thread-unified-recovery", "primary", message)
	if err != nil {
		t.Fatal(err)
	}
	multiplexer.markVisibleOutput("primary", "item/completed", []byte(`{"threadId":"thread-unified-recovery","item":{"type":"agentMessage"}}`))
	recovery := protocol.Message{Method: "error", Params: []byte(`{"threadId":"thread-unified-recovery","error":{"type":"relay_pool_recovery_required","code":"relay_pool_recovery_required"}}`)}
	raw, err := protocol.Encode(recovery)
	if err != nil {
		t.Fatal(err)
	}
	multiplexer.handleInbound(backend.Inbound{AccountID: "primary", Message: recovery, Raw: raw})
	route, _ := store.ThreadRoute("thread-unified-recovery")
	if !route.RecoveryRequired || route.ActiveAttemptID != "" || route.CurrentState != "recovery-required" {
		t.Fatalf("unified recovery marker completed or left turn active: %#v", route)
	}
	if attempt, ok := store.TurnAttempt(active.attemptID); !ok || attempt.Phase != "RECOVERY_REQUIRED" {
		t.Fatalf("unified recovery marker did not close attempt safely: %#v ok=%v", attempt, ok)
	}
	if !bytes.Contains(output.Bytes(), []byte("relay_pool_recovery_required")) {
		t.Fatalf("sanitized recovery marker was not forwarded: %s", output.String())
	}
}

func TestVisibleOutputClassifierIgnoresUserInputItems(t *testing.T) {
	user := []byte(`{"threadId":"thread-1","item":{"type":"userMessage"}}`)
	if isVisibleOutputNotification("item/started", user) || isVisibleOutputNotification("item/completed", user) {
		t.Fatal("user input was misclassified as visible assistant output")
	}
	for _, itemType := range []string{"agentMessage", "reasoning", "plan"} {
		params := []byte(fmt.Sprintf(`{"threadId":"thread-1","item":{"type":%q}}`, itemType))
		if !isVisibleOutputNotification("item/completed", params) {
			t.Fatalf("%s was not classified as visible assistant output", itemType)
		}
	}
}

func TestBoundedModelCapacityFailureClosesAttemptAndReservation(t *testing.T) {
	multiplexer, store := newCoordinatorTestMux(t)
	var output bytes.Buffer
	multiplexer.output = &output
	if err := store.SetThreadOwner("thread-capacity", "primary"); err != nil {
		t.Fatal(err)
	}
	message := protocol.Request("turn/start", protocol.StringID("turn-capacity"), []byte(`{"threadId":"thread-capacity"}`))
	active, err := multiplexer.beginTurnAttempt("thread-capacity", "primary", message)
	if err != nil {
		t.Fatal(err)
	}
	route := active.route
	route.capacityRetries = maxModelCapacityRetries
	multiplexer.retryTurnAfterModelCapacity(route, "primary", []byte(`{"error":"capacity"}`))
	threadRoute, _ := store.ThreadRoute("thread-capacity")
	if threadRoute.ActiveAttemptID != "" || threadRoute.CurrentState != "idle" {
		t.Fatalf("bounded capacity failure left a live attempt: %#v", threadRoute)
	}
	if _, ok := store.Scheduler().Reservations[active.attemptID]; ok {
		t.Fatal("bounded capacity failure leaked its reservation")
	}
	attempt, _ := store.TurnAttempt(active.attemptID)
	if attempt.Phase != "FAILED" || attempt.FailureCategory != "selected_model_capacity" {
		t.Fatalf("bounded capacity attempt was not failed cleanly: %#v", attempt)
	}
}

func TestStartupRollsBackInterruptedHandoffToSourceGeneration(t *testing.T) {
	for _, phase := range []string{"PREPARED", "COPIED", "RESUMED"} {
		t.Run(phase, func(t *testing.T) {
			multiplexer, store := newCoordinatorTestMux(t)
			route := state.ThreadRoute{ThreadID: "thread-1", AccountID: "primary", Generation: 4, ActiveMigrationID: "handoff-1"}
			handoff := state.Handoff{ID: "handoff-1", ThreadID: "thread-1", SourceAccountID: "primary", TargetAccountID: "secondary", SourceGeneration: 4, TargetGeneration: 5, Phase: phase}
			if err := store.TransitionHandoff(handoff, route); err != nil {
				t.Fatal(err)
			}
			if err := multiplexer.recoverInterruptedHandoffs(); err != nil {
				t.Fatal(err)
			}
			recovered, _ := store.ThreadRoute("thread-1")
			if recovered.AccountID != "primary" || recovered.Generation != 4 || recovered.ActiveMigrationID != "" {
				t.Fatalf("interrupted route was not rolled back: %#v", recovered)
			}
			handoffs := store.Handoffs()
			if len(handoffs) != 1 || handoffs[0].Phase != "ROLLED_BACK" {
				t.Fatalf("interrupted journal was not closed: %#v", handoffs)
			}
		})
	}
}

func TestStartupFailsClosedForAnUnfinishedTurnAndReleasesReservation(t *testing.T) {
	multiplexer, store := newCoordinatorTestMux(t)
	if err := store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	message := protocol.Request("turn/start", protocol.StringID("turn-1"), []byte(`{"threadId":"thread-1"}`))
	active, err := multiplexer.beginTurnAttempt("thread-1", "primary", message)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Scheduler().Reservations[active.attemptID]; !ok {
		t.Fatal("active turn reservation was not persisted")
	}
	restarted, err := New(Options{RealExecutable: "fake", Store: store, Output: bytes.NewBuffer(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.recoverInterruptedAttempts(); err != nil {
		t.Fatal(err)
	}
	route, _ := store.ThreadRoute("thread-1")
	if !route.RecoveryRequired || route.ActiveAttemptID != "" {
		t.Fatalf("unfinished turn did not fail closed: %#v", route)
	}
	attempt, _ := store.TurnAttempt(active.attemptID)
	if attempt.Phase != "RECOVERY_REQUIRED" {
		t.Fatalf("unfinished attempt phase = %q", attempt.Phase)
	}
	if _, ok := store.Scheduler().Reservations[active.attemptID]; ok {
		t.Fatal("crash recovery left a stale reservation")
	}
}

func TestStaleWorkerNotificationsAreSuppressedByCurrentGeneration(t *testing.T) {
	multiplexer, store := newCoordinatorTestMux(t)
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutThreadRoute(state.ThreadRoute{ThreadID: "thread-1", AccountID: secondary.ID, Generation: 3}); err != nil {
		t.Fatal(err)
	}
	params := []byte(`{"threadId":"thread-1","turn":{"status":"completed"}}`)
	if multiplexer.shouldForwardNotification("primary", "turn/completed", params) {
		t.Fatal("stale generation notification was forwarded")
	}
	if !multiplexer.shouldForwardNotification(secondary.ID, "turn/completed", params) {
		t.Fatal("current generation notification was suppressed")
	}
}

func TestTargetChildUnavailableDoesNotPrepareHandoff(t *testing.T) {
	multiplexer, store := newCoordinatorTestMux(t)
	multiplexer.compatibilityProfile = "fixture-reviewed-v2"
	multiplexer.safeHandoff = true
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	err = multiplexer.resumeThreadOnAccount(context.Background(), "thread-1", "primary", secondary.ID)
	if err == nil || !strings.Contains(err.Error(), "target subscription is unavailable") {
		t.Fatalf("target unavailable error = %v", err)
	}
	if handoffs := store.Handoffs(); len(handoffs) != 0 {
		t.Fatalf("unavailable target created journal: %#v", handoffs)
	}
}

func TestCanonicalTurnLedgerStoresRequestHashNotPromptOrPaths(t *testing.T) {
	multiplexer, store := newCoordinatorTestMux(t)
	if err := store.SetThreadOwner("thread-private", "primary"); err != nil {
		t.Fatal(err)
	}
	secretPrompt := "do not persist this prompt SECRET-PROMPT-91827"
	secretPath := `C:\\Users\\Private\\workspace\\secret.txt`
	params := []byte(`{"threadId":"thread-private","input":"` + secretPrompt + `","cwd":"` + secretPath + `"}`)
	message := protocol.Request("turn/start", protocol.StringID("private-turn"), params)
	active, err := multiplexer.beginTurnAttempt("thread-private", "primary", message)
	if err != nil {
		t.Fatal(err)
	}
	attempt, ok := store.TurnAttempt(active.attemptID)
	if !ok || len(attempt.RequestHash) != 64 {
		t.Fatalf("request hash was not persisted: %#v ok=%v", attempt, ok)
	}
	ledgerPath := filepath.Join(multiplexer.canonicalThreadDirectory("thread-private"), "turn-ledger.jsonl")
	ledger, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ledger, []byte(secretPrompt)) || bytes.Contains(ledger, []byte(secretPath)) {
		t.Fatalf("canonical turn ledger leaked request content: %s", ledger)
	}
	if !bytes.Contains(ledger, []byte(attempt.RequestHash)) {
		t.Fatalf("canonical turn ledger is missing request hash: %s", ledger)
	}
	category, summary := sanitizeRoutingFailure("copy existing chat history from " + secretPath + ": bearer SECRET-TOKEN")
	if category != "history_migration_failed" || strings.Contains(summary, secretPath) || strings.Contains(summary, "SECRET-TOKEN") {
		t.Fatalf("unsafe failure sanitization category=%q summary=%q", category, summary)
	}
}
