package mux

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LightHaru/codex-relay/internal/protocol"
	"github.com/LightHaru/codex-relay/internal/state"
)

func TestExplainabilityPreviewDoesNotMutateScheduler(t *testing.T) {
	multiplexer, store := newCoordinatorTestMux(t)
	now := time.Unix(2_000_000_000, 0)
	multiplexer.now = func() time.Time { return now }
	minutes := int64(300)
	snapshots := []AccountSnapshot{
		{ID: "primary", Label: "Primary", Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitAvailable: true, RateLimitsObservedAt: now.UnixMilli(), RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 25, WindowDurationMins: &minutes}}},
	}
	before := store.Scheduler()
	first := multiplexer.previewCandidates(snapshots)
	second := multiplexer.previewCandidates(snapshots)
	after := store.Scheduler()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("read-only preview mutated scheduler: before=%#v after=%#v", before, after)
	}
	if !reflect.DeepEqual(first, second) || len(first) != 1 || !first[0].IsPreview || first[0].ReasonCode != "selected_highest_score" {
		t.Fatalf("preview is not deterministic and explicitly marked: first=%#v second=%#v", first, second)
	}
}

func TestExplainabilityTimelineUsesStableUniqueEventIDs(t *testing.T) {
	multiplexer, store := newCoordinatorTestMux(t)
	if err := store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	message := protocol.Request("turn/start", protocol.StringID("turn-1"), []byte(`{"threadId":"thread-1"}`))
	active, err := multiplexer.beginTurnAttempt("thread-1", "primary", message)
	if err != nil {
		t.Fatal(err)
	}
	multiplexer.markTurnAccepted("thread-1", "primary")
	multiplexer.completeActiveTurn("thread-1", "primary")
	events := multiplexer.routingTimeline("thread-1", 100)
	want := map[string]bool{
		active.attemptID + ":turn_reserved":  false,
		active.attemptID + ":turn_accepted":  false,
		active.attemptID + ":turn_completed": false,
	}
	seen := make(map[string]struct{}, len(events))
	for _, event := range events {
		if _, duplicate := seen[event.ID]; duplicate {
			t.Fatalf("timeline repeated event ID %q: %#v", event.ID, events)
		}
		seen[event.ID] = struct{}{}
		if _, expected := want[event.ID]; expected {
			want[event.ID] = true
		}
	}
	for id, found := range want {
		if !found {
			t.Fatalf("timeline is missing %q: %#v", id, events)
		}
	}
}

func TestExplainabilityContractSeparatesOwnerWorkerAndQuotaAttribution(t *testing.T) {
	pool := newRoutingTestPool(t, 20, 20, 30, 30)
	if err := pool.store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	route, _ := pool.store.ThreadRoute("thread-1")
	route.PreviousAccountID = pool.secondary.ID
	route.LastCompletedAccountID = "primary"
	route.LastQuotaAccountID = pool.secondary.ID
	if err := pool.store.PutThreadRoute(route); err != nil {
		t.Fatal(err)
	}
	status, err := pool.multiplexer.ThreadRouteStatus(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if status.ContractVersion != 1 || status.CurrentOwner == nil || status.CurrentOwner.AccountID != "primary" {
		t.Fatalf("missing versioned current-owner contract: %#v", status)
	}
	if status.LastCompletedWorker == nil || status.LastCompletedWorker.AccountID != "primary" || status.LastQuotaConsumingWorker == nil || status.LastQuotaConsumingWorker.AccountID != pool.secondary.ID {
		t.Fatalf("worker roles were conflated: completed=%#v quota=%#v", status.LastCompletedWorker, status.LastQuotaConsumingWorker)
	}
	if status.PreviousWorker == nil || status.PreviousWorker.AccountID != pool.secondary.ID || !status.NextCandidateIsPreview {
		t.Fatalf("previous/preview semantics are missing: %#v", status)
	}
}

func TestExplainabilityResponsesDoNotExposeRawIdentityErrorsOrPaths(t *testing.T) {
	pool := newRoutingTestPool(t, 20, 20, 30, 30)
	secretPath := `C:\Users\Private\prompt-workspace\rollout.jsonl`
	if err := pool.store.PutAccountHealth(state.AccountHealth{AccountID: "primary", State: "open", Reason: "failed for prompt secret at " + secretPath, LastFailureAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := pool.store.RecordDecision(state.RoutingDecision{ID: "corrupt-private-decision", Policy: state.RoutingPolicyBalanced, EventType: "route_decision", ReasonCode: "selected_highest_score", Reason: "prompt secret at " + secretPath, CreatedAt: 2}); err != nil {
		t.Fatal(err)
	}
	status := pool.multiplexer.RouterStatus(context.Background())
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"test@example.invalid", secretPath, "prompt secret"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("explainability response leaked %q: %s", forbidden, text)
		}
	}
	if status.ContractVersion != 1 || len(status.AccountRoutes) != 2 || len(status.Accounts) != 2 {
		t.Fatalf("sanitized status lost its public routing shape: %#v", status)
	}
	for _, account := range status.Accounts {
		if account.Email != "" || account.Username != "" || account.Error != "" || account.RateLimitError != "" {
			t.Fatalf("router status returned raw account diagnostics: %#v", account)
		}
	}
}

func TestUnifiedPoolStatusPublishesOnePoolWithoutCredentialSources(t *testing.T) {
	pool := newRoutingTestPool(t, 20, 20, 30, 30)
	pool.multiplexer.gatewayBaseURL = "http://127.0.0.1:1/v1"
	pool.multiplexer.gatewayToken = "local-test-token"

	status := pool.multiplexer.RouterStatus(context.Background())
	if status.ContractVersion != 2 || status.StateVersion != state.PoolSchemaVersion {
		t.Fatalf("unified contract versions = %d/%d", status.ContractVersion, status.StateVersion)
	}
	if status.Pool.PoolID != state.DefaultPoolID || status.Pool.RoutingCapacityOnly {
		t.Fatalf("unified pool projection = %#v", status.Pool)
	}
	if len(status.Accounts) != 0 || len(status.AccountRoutes) != 0 || len(status.AccountHealth) != 0 || len(status.Capabilities) != 0 || len(status.EligiblePool) != 0 || status.NextCandidate != nil || len(status.Handoffs) != 0 || len(status.Timeline) != 0 {
		t.Fatalf("unified public status exposed source-level routing state: %#v", status)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"primary", pool.secondary.ID, "test@example.invalid"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("unified public status leaked credential source %q: %s", forbidden, encoded)
		}
	}
}

func TestUnifiedThreadRouteKeepsOneRelayIdentity(t *testing.T) {
	pool := newRoutingTestPool(t, 20, 20, 30, 30)
	pool.multiplexer.gatewayBaseURL = "http://127.0.0.1:1/v1"
	pool.multiplexer.gatewayToken = "local-test-token"
	if err := pool.store.SetThreadOwner("thread-unified", pool.secondary.ID); err != nil {
		t.Fatal(err)
	}
	route, _ := pool.store.ThreadRoute("thread-unified")
	route.PreviousAccountID = "primary"
	route.LastCompletedAccountID = pool.secondary.ID
	route.LastQuotaAccountID = "primary"
	if err := pool.store.PutThreadRoute(route); err != nil {
		t.Fatal(err)
	}

	status, err := pool.multiplexer.ThreadRouteStatus(context.Background(), "thread-unified")
	if err != nil {
		t.Fatal(err)
	}
	if status.ContractVersion != 2 || status.Route.AccountID != state.DefaultPoolID || status.Controller == nil || status.Controller.AccountID != state.DefaultPoolID || status.CurrentOwner == nil || status.CurrentOwner.AccountID != state.DefaultPoolID {
		t.Fatalf("thread did not project one Relay identity: %#v", status)
	}
	if status.ActiveWorker != nil || status.PreviousWorker != nil || status.LastCompletedWorker != nil || status.LastQuotaConsumingWorker != nil || status.NextCandidate != nil || len(status.Workers) != 0 || len(status.Handoffs) != 0 || len(status.Timeline) != 0 {
		t.Fatalf("unified thread status exposed credential-source routing: %#v", status)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"primary", pool.secondary.ID, "test@example.invalid"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("unified thread status leaked credential source %q: %s", forbidden, encoded)
		}
	}
}

func TestQuotaAttributionRequiresBeforeAndNewerAfterSnapshot(t *testing.T) {
	before, after := 71.0, 69.5
	confirmed := quotaAttributionFromAttempt(state.TurnAttempt{
		AccountID: "subscription-2", QuotaAttribution: "confirmed",
		QuotaBeforeRemaining: &before, QuotaBeforeObservedAt: 100,
		QuotaAfterRemaining: &after, QuotaAfterObservedAt: 200,
	})
	if confirmed.Status != "confirmed" || confirmed.DeltaPercent == nil || *confirmed.DeltaPercent != 1.5 {
		t.Fatalf("confirmed attribution = %#v", confirmed)
	}
	waiting := quotaAttributionFromAttempt(state.TurnAttempt{AccountID: "subscription-2", QuotaBeforeRemaining: &before, QuotaBeforeObservedAt: 100})
	if waiting.Status != "waiting_for_refreshed_quota" || waiting.DeltaPercent != nil {
		t.Fatalf("unrefreshed attribution made a confirmation claim: %#v", waiting)
	}
	unchanged := quotaAttributionFromAttempt(state.TurnAttempt{
		AccountID: "subscription-2", QuotaAttribution: "refreshed_no_measurable_change",
		QuotaBeforeRemaining: &before, QuotaBeforeObservedAt: 100,
		QuotaAfterRemaining: &before, QuotaAfterObservedAt: 200,
	})
	if unchanged.Status == "confirmed" || unchanged.DeltaPercent == nil || *unchanged.DeltaPercent != 0 {
		t.Fatalf("unchanged quota snapshot made a consumption claim: %#v", unchanged)
	}
}

func TestExplainabilityWorkerReasonsAreStandardizedAndQuotaTruthful(t *testing.T) {
	multiplexer, store := newCoordinatorTestMux(t)
	now := time.Unix(2_000_000_000, 0)
	multiplexer.now = func() time.Time { return now }
	minutes := int64(300)
	window := func(used float64) *RateLimits {
		return &RateLimits{Primary: &RateLimitWindow{UsedPercent: used, WindowDurationMins: &minutes}}
	}
	snapshots := []AccountSnapshot{
		{ID: "fresh", Label: "Fresh", Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitAvailable: true, RateLimitsObservedAt: now.UnixMilli(), RateLimits: window(20)},
		{ID: "disabled", Label: "Disabled", Enabled: false, Connected: true, AuthType: "chatgpt", RateLimitsObservedAt: now.UnixMilli(), RateLimits: window(20)},
		{ID: "disconnected", Label: "Disconnected", Enabled: true, Connected: false, AuthType: "chatgpt"},
		{ID: "depleted", Label: "Depleted", Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitAvailable: true, RateLimitsObservedAt: now.UnixMilli(), RateLimits: window(100)},
		{ID: "cooldown", Label: "Cooldown", Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitAvailable: true, RateLimitsObservedAt: now.UnixMilli(), RateLimits: window(20)},
		{ID: "open-circuit", Label: "Open circuit", Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitAvailable: true, RateLimitsObservedAt: now.UnixMilli(), RateLimits: window(20)},
		{ID: "unknown", Label: "Unknown", Enabled: true, Connected: true, AuthType: "chatgpt"},
		{ID: "stale", Label: "Stale", Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitAvailable: true, RateLimitsObservedAt: now.Add(-3 * time.Minute).UnixMilli(), RateLimits: window(10)},
	}
	if err := store.PutAccountHealth(state.AccountHealth{AccountID: "cooldown", State: "open", OpenUntil: now.Add(time.Minute).UnixMilli(), Reason: "quota rejected turn"}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutAccountHealth(state.AccountHealth{AccountID: "open-circuit", State: "half-open", OpenUntil: now.Add(time.Minute).UnixMilli(), Reason: "quota rejected turn"}); err != nil {
		t.Fatal(err)
	}
	statuses := multiplexer.accountRouteStatuses(snapshots)
	byID := make(map[string]AccountRouteStatus, len(statuses))
	for _, status := range statuses {
		byID[status.AccountID] = status
	}
	want := map[string]string{
		"disabled": "skipped_disabled", "disconnected": "skipped_disconnected",
		"depleted": "skipped_depleted", "cooldown": "skipped_cooldown",
		"open-circuit": "skipped_open_circuit", "unknown": "skipped_unknown_quota",
		"stale": "skipped_stale_quota",
	}
	for accountID, reason := range want {
		if byID[accountID].ReasonCode != reason {
			t.Errorf("%s reason = %q, want %q (%#v)", accountID, byID[accountID].ReasonCode, reason, byID[accountID])
		}
	}
	if byID["unknown"].ConfirmedRemaining != nil || byID["stale"].ConfirmedRemaining != nil || byID["unknown"].QuotaKnown || byID["stale"].QuotaKnown {
		t.Fatalf("unknown/stale quota was presented as confirmed: unknown=%#v stale=%#v", byID["unknown"], byID["stale"])
	}
}

func TestExplainabilityHandoffSummaryIncludesReasonAndGenerations(t *testing.T) {
	pool := newRoutingTestPool(t, 20, 20, 30, 30)
	if err := pool.store.SetThreadOwner("thread-1", pool.secondary.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	handoff := state.Handoff{
		ID: "handoff-proof", ThreadID: "thread-1", SourceAccountID: "primary", TargetAccountID: pool.secondary.ID,
		SourceGeneration: 1, TargetGeneration: 2, Phase: "COMMITTED", ReasonCode: "handoff_quota_exhausted",
		Reason: "the previous worker had no confirmed quota before side effects", StartedAt: now - 10, UpdatedAt: now,
	}
	route, _ := pool.store.ThreadRoute("thread-1")
	route.PreviousAccountID = "primary"
	route.Generation = 2
	if err := pool.store.TransitionHandoff(handoff, route); err != nil {
		t.Fatal(err)
	}
	status, err := pool.multiplexer.ThreadRouteStatus(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if status.LatestHandoff == nil || status.LatestHandoff.SourceAccountID != "primary" || status.LatestHandoff.TargetAccountID != pool.secondary.ID || status.LatestHandoff.SourceGeneration != 1 || status.LatestHandoff.TargetGeneration != 2 || status.LatestHandoff.ReasonCode != "handoff_quota_exhausted" {
		t.Fatalf("latest handoff projection is incomplete: %#v", status.LatestHandoff)
	}
}

func TestFailedReservationRollbackCreatesOneTimelineEvent(t *testing.T) {
	multiplexer, store := newCoordinatorTestMux(t)
	if err := store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	active, err := multiplexer.beginTurnAttempt("thread-1", "primary", protocol.Request("turn/start", protocol.StringID("turn-1"), []byte(`{"threadId":"thread-1"}`)))
	if err != nil {
		t.Fatal(err)
	}
	multiplexer.rollbackTurnReservation("thread-1", active.attemptID, "primary", active.generation, "dispatch_not_committed")
	multiplexer.rollbackTurnReservation("thread-1", active.attemptID, "primary", active.generation, "dispatch_not_committed")
	count := 0
	for _, event := range multiplexer.routingTimeline("thread-1", 100) {
		if event.Type == "reservation_rolled_back" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("reservation rollback timeline count = %d, want 1", count)
	}
	if _, exists := store.Scheduler().Reservations[active.attemptID]; exists {
		t.Fatal("failed reservation remained in scheduler")
	}
}

func TestCanonicalRoutingTimelineRetentionIsBounded(t *testing.T) {
	multiplexer, store := newCoordinatorTestMux(t)
	if err := store.SetThreadOwner("thread-retention", "primary"); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 105; index++ {
		if err := store.RecordDecision(state.RoutingDecision{
			ID: fmt.Sprintf("decision-%03d", index), ThreadID: "thread-retention",
			Policy: state.RoutingPolicyBalanced, EventType: "route_decision",
			ReasonCode: "selected_highest_score", Reason: "safe fixed reason", CreatedAt: int64(index + 1),
		}); err != nil {
			t.Fatal(err)
		}
	}
	multiplexer.compactCanonicalDecisionLedgers()
	path := filepath.Join(multiplexer.canonicalThreadDirectory("thread-retention"), "routing-decisions.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := bytes.Count(data, []byte{'\n'}); lines != 100 {
		t.Fatalf("canonical decision retention = %d lines, want 100", lines)
	}
	if bytes.Contains(data, []byte(`"decision-004"`)) || !bytes.Contains(data, []byte(`"decision-005"`)) || !bytes.Contains(data, []byte(`"decision-104"`)) {
		t.Fatalf("canonical decision retention did not keep the newest 100 rows")
	}
}
