package mux

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LightHaru/codex-relay/internal/protocol"
	"github.com/LightHaru/codex-relay/internal/state"
)

var (
	errTurnAlreadyActive = errors.New("another turn is already active for this chat")
	errRecoveryRequired  = errors.New("this chat requires recovery review before another turn can start")
)

func (m *Multiplexer) beginTurnAttempt(threadID, accountID string, message protocol.Message) (activeTurnRoute, error) {
	threadID = strings.TrimSpace(threadID)
	accountID = strings.TrimSpace(accountID)
	if threadID == "" || accountID == "" {
		return activeTurnRoute{}, errors.New("thread and account IDs are required")
	}
	m.turnMu.Lock()
	defer m.turnMu.Unlock()
	if _, exists := m.activeTurns[threadID]; exists {
		return activeTurnRoute{}, errTurnAlreadyActive
	}
	route, ok := m.store.ThreadRoute(threadID)
	if !ok {
		route = state.ThreadRoute{ThreadID: threadID, AccountID: accountID, Generation: 1}
	}
	if route.RecoveryRequired {
		return activeTurnRoute{}, errRecoveryRequired
	}
	if route.ActiveMigrationID != "" || route.ActiveAttemptID != "" {
		return activeTurnRoute{}, errTurnAlreadyActive
	}
	now := time.Now().UnixMilli()
	attemptID := fmt.Sprintf("attempt-%d-%d", now, m.serverSequence.Add(1))
	attemptNumber := 1
	for _, previous := range m.store.TurnAttempts() {
		if previous.ThreadID == threadID && previous.AttemptNumber >= attemptNumber {
			attemptNumber = previous.AttemptNumber + 1
		}
	}
	attempt := state.TurnAttempt{
		ID: attemptID, ThreadID: threadID, AccountID: accountID,
		LogicalTurnID: attemptID, RequestHash: requestHash(message.Method, message.Params),
		Generation: route.Generation, RouteGeneration: route.Generation, AttemptNumber: attemptNumber,
		Phase: "reserved", StartedAt: now, UpdatedAt: now,
	}
	if err := m.store.PutTurnAttempt(attempt); err != nil {
		return activeTurnRoute{}, err
	}
	route.ActiveAttemptID = attemptID
	route.Policy = m.effectiveRoutingPolicy()
	route.CurrentState = "reserved"
	if err := m.store.PutThreadRoute(route); err != nil {
		attempt.Phase = "FAILED"
		attempt.Failure = err.Error()
		_ = m.store.PutTurnAttempt(attempt)
		return activeTurnRoute{}, err
	}
	if err := m.store.PutReservation(state.Reservation{ID: attemptID, AccountID: accountID, ThreadID: threadID, AttemptID: attemptID, Weight: 1, ExpiresAt: time.Now().Add(2 * time.Minute).UnixMilli()}); err != nil {
		route.ActiveAttemptID = ""
		_ = m.store.PutThreadRoute(route)
		attempt.Phase = "FAILED"
		attempt.Failure = err.Error()
		_ = m.store.PutTurnAttempt(attempt)
		return activeTurnRoute{}, err
	}
	active := activeTurnRoute{
		route:     externalRoute{accountID: accountID, method: message.Method, message: message},
		attemptID: attemptID, generation: route.Generation,
	}
	m.activeTurns[threadID] = active
	m.appendCanonicalTurn(attempt)
	m.recordAttemptTimeline(attempt, "turn_reserved", "turn_reserved", "capacity reserved for the logical turn", "pending", now)
	_ = m.persistCanonicalSnapshot(threadID)
	m.publish(Event{Type: "turn-route-reserved", ThreadID: threadID, AttemptID: attemptID, AccountID: accountID, RouteGeneration: route.Generation, Data: map[string]any{"policy": route.Policy}})
	return active, nil
}

func (m *Multiplexer) moveTurnAttempt(threadID, accountID string, generation uint64) error {
	m.turnMu.Lock()
	active, ok := m.activeTurns[threadID]
	if !ok {
		if route, found := m.store.ThreadRoute(threadID); found && route.ActiveAttemptID != "" {
			active = activeTurnRoute{attemptID: route.ActiveAttemptID, generation: route.Generation, route: externalRoute{accountID: route.AccountID}}
			ok = true
		}
	}
	if !ok {
		m.turnMu.Unlock()
		return errors.New("active turn attempt is missing")
	}
	active.route.accountID = accountID
	active.generation = generation
	m.activeTurns[threadID] = active
	m.turnMu.Unlock()
	attempt, ok := m.store.TurnAttempt(active.attemptID)
	if !ok {
		return errors.New("active attempt journal is missing")
	}
	attempt.AccountID = accountID
	attempt.Generation = generation
	attempt.RouteGeneration = generation
	attempt.Phase = "sent"
	attempt.UpdatedAt = time.Now().UnixMilli()
	if err := m.store.PutTurnAttempt(attempt); err != nil {
		return err
	}
	m.appendCanonicalTurn(attempt)
	m.recordAttemptTimeline(attempt, "worker_selected", "selected_highest_score", "worker selected for the logical turn", "selected", attempt.UpdatedAt)
	if route, ok := m.store.ThreadRoute(threadID); ok {
		route.CurrentState = "sent"
		_ = m.store.PutThreadRoute(route)
	}
	return m.store.PutReservation(state.Reservation{ID: active.attemptID, AccountID: accountID, ThreadID: threadID, AttemptID: active.attemptID, Weight: 1, ExpiresAt: time.Now().Add(2 * time.Minute).UnixMilli()})
}

func (m *Multiplexer) rollbackTurnReservation(threadID, attemptID, accountID string, generation uint64, reasonCode string) {
	if attemptID == "" {
		return
	}
	if err := m.store.RollbackReservation(attemptID); err != nil {
		return
	}
	m.recordTimelineEvent(threadID, stateRoutingEvent{
		ID: attemptID + ":reservation_rolled_back", EventType: "reservation_rolled_back",
		AttemptID: attemptID, AccountID: accountID, Generation: generation,
		ReasonCode: reasonCode, Reason: "the uncommitted scheduler reservation was rolled back",
		Result: "rolled_back", CreatedAt: time.Now().UnixMilli(),
	})
}

func (m *Multiplexer) finishTurnAttempt(threadID string, active activeTurnRoute, phase, failure string) {
	_ = m.store.ReleaseReservation(active.attemptID)
	attempt, ok := m.store.TurnAttempt(active.attemptID)
	if ok {
		attempt.Phase = phase
		attempt.FailureCategory, attempt.Failure = sanitizeRoutingFailure(failure)
		attempt.UpdatedAt = time.Now().UnixMilli()
		if phase == "COMPLETED" || phase == "FAILED" || phase == "RECOVERY_REQUIRED" {
			attempt.CompletedAt = attempt.UpdatedAt
		}
		_ = m.store.PutTurnAttempt(attempt)
		m.appendCanonicalTurn(attempt)
		eventType, result := "turn_failed", "failed"
		reasonCode := attempt.FailureCategory
		if phase == "COMPLETED" {
			eventType, result, reasonCode = "turn_completed", "completed", "turn_completed"
		} else if phase == "RECOVERY_REQUIRED" {
			eventType, result = "recovery_required", "blocked"
		}
		m.recordAttemptTimeline(attempt, eventType, reasonCode, attempt.Failure, result, attempt.CompletedAt)
	}
	route, ok := m.store.ThreadRoute(threadID)
	if ok && route.ActiveAttemptID == active.attemptID {
		route.ActiveAttemptID = ""
		route.CurrentState = "idle"
		if phase == "RECOVERY_REQUIRED" {
			route.RecoveryRequired = true
			route.CurrentState = "recovery-required"
		}
		_ = m.store.PutThreadRoute(route)
		_ = m.persistCanonicalSnapshot(threadID)
	}
}

// sanitizeRoutingFailure deliberately persists only a bounded classification,
// never an arbitrary upstream error. Upstream errors can contain prompt text,
// workspace paths, callback data, or credentials and therefore do not belong
// in the route API, SSE payloads, or canonical diagnostic ledger.
func sanitizeRoutingFailure(failure string) (string, string) {
	text := strings.ToLower(strings.TrimSpace(failure))
	if text == "" {
		return "", ""
	}
	type classification struct {
		category string
		message  string
		needles  []string
	}
	for _, candidate := range []classification{
		{category: "side_effect_already_observed", message: "automatic retry is blocked after a side effect", needles: []string{"side effect", "approval", "tool callback"}},
		{category: "visible_output_already_observed", message: "automatic retry is blocked after visible assistant output", needles: []string{"visible assistant output"}},
		{category: "quota_exhausted", message: "the selected subscription has no confirmed quota", needles: []string{"quota", "usage limit", "rate limit"}},
		{category: "auth_expired", message: "the selected subscription must sign in again", needles: []string{"auth", "sign in", "login"}},
		{category: "selected_model_capacity", message: "the selected model is temporarily at capacity", needles: []string{"model", "capacity"}},
		{category: "unsupported_model", message: "the selected worker does not support this model", needles: []string{"unsupported model"}},
		{category: "history_not_found", message: "the authoritative task history was not found", needles: []string{"no rollout", "history not found"}},
		{category: "resume_failed", message: "the target worker could not verify the resumed task", needles: []string{"resume", "resumed chat"}},
		{category: "history_migration_failed", message: "the authoritative task history could not be verified on the target worker", needles: []string{"history", "rollout", "checkpoint", "hash", "copy existing chat"}},
		{category: "child_unavailable", message: "the selected worker is unavailable", needles: []string{"child", "subscription is unavailable", "broken pipe"}},
		{category: "network_transient", message: "a temporary worker communication error occurred", needles: []string{"timeout", "deadline", "network", "connection"}},
		{category: "recovery_required", message: "the task requires recovery review before continuing", needles: []string{"recovery"}},
	} {
		for _, needle := range candidate.needles {
			if strings.Contains(text, needle) {
				return candidate.category, candidate.message
			}
		}
	}
	return "routing_operation_failed", "the routing operation failed before a safe terminal state"
}

func (m *Multiplexer) failTurnAttempt(threadID string, failure error) {
	m.turnMu.Lock()
	active, ok := m.activeTurns[threadID]
	if ok {
		delete(m.activeTurns, threadID)
	}
	m.turnMu.Unlock()
	if !ok {
		if route, found := m.store.ThreadRoute(threadID); found && route.ActiveAttemptID != "" {
			if attempt, attemptFound := m.store.TurnAttempt(route.ActiveAttemptID); attemptFound {
				active = activeTurnRoute{attemptID: attempt.ID, generation: attempt.Generation, route: externalRoute{accountID: attempt.AccountID}}
				ok = true
			}
		}
	}
	if ok {
		m.finishTurnAttempt(threadID, active, "FAILED", failure.Error())
	}
}

func (m *Multiplexer) requireTurnRecovery(threadID string, active activeTurnRoute, reason string) {
	m.finishTurnAttempt(threadID, active, "RECOVERY_REQUIRED", reason)
	m.publish(Event{Type: "recovery-required", ThreadID: threadID, AttemptID: active.attemptID, AccountID: active.route.accountID, RouteGeneration: active.generation, Message: reason})
}

func (m *Multiplexer) markAccountSideEffects(accountID, method string, params []byte) {
	text := strings.ToLower(method + " " + string(params))
	if !strings.Contains(text, "approval") && !strings.Contains(text, "command") &&
		!strings.Contains(text, "file_change") && !strings.Contains(text, "patch") &&
		!strings.Contains(text, "tool") && !strings.Contains(text, "hook") {
		return
	}
	m.turnMu.Lock()
	updates := make([]activeTurnRoute, 0)
	for threadID, active := range m.activeTurns {
		if active.route.accountID != accountID || active.sideEffectsStarted {
			continue
		}
		active.sideEffectsStarted = true
		m.activeTurns[threadID] = active
		updates = append(updates, active)
	}
	m.turnMu.Unlock()
	for _, active := range updates {
		if attempt, ok := m.store.TurnAttempt(active.attemptID); ok {
			attempt.SideEffectsStarted = true
			attempt.Phase = "side-effect-observed"
			attempt.SideEffectTypes = appendUniqueString(attempt.SideEffectTypes, sanitizedSideEffectType(method))
			attempt.UpdatedAt = time.Now().UnixMilli()
			_ = m.store.PutTurnAttempt(attempt)
			m.appendCanonicalTurn(attempt)
			m.recordAttemptTimeline(attempt, "side_effect_observed", "side_effect_observed", "a side effect was observed on the active worker", "observed", attempt.UpdatedAt)
		}
	}
}

func (m *Multiplexer) markTurnAccepted(threadID, accountID string) {
	m.turnMu.Lock()
	active, ok := m.activeTurns[threadID]
	m.turnMu.Unlock()
	if !ok || active.route.accountID != accountID || active.attemptID == "" {
		return
	}
	attempt, ok := m.store.TurnAttempt(active.attemptID)
	if !ok {
		return
	}
	now := time.Now().UnixMilli()
	attempt.Phase = "accepted"
	if attempt.AcceptedAt == 0 {
		attempt.AcceptedAt = now
	}
	attempt.UpdatedAt = now
	if m.store.PutTurnAttempt(attempt) == nil {
		m.appendCanonicalTurn(attempt)
		m.recordAttemptTimeline(attempt, "turn_accepted", "turn_accepted", "the worker accepted the logical turn", "accepted", attempt.AcceptedAt)
	}
}

func (m *Multiplexer) markVisibleOutput(accountID, method string, params []byte) {
	threadID := threadIDFromAnyParams(params)
	if threadID == "" {
		return
	}
	m.turnMu.Lock()
	active, ok := m.activeTurns[threadID]
	if ok && active.route.accountID == accountID {
		active.visibleOutputStarted = true
		m.activeTurns[threadID] = active
	}
	m.turnMu.Unlock()
	if !ok || active.route.accountID != accountID || active.attemptID == "" {
		return
	}
	attempt, ok := m.store.TurnAttempt(active.attemptID)
	if !ok {
		return
	}
	now := time.Now().UnixMilli()
	if attempt.FirstVisibleOutputAt == 0 {
		attempt.FirstVisibleOutputAt = now
	}
	if !attempt.SideEffectsStarted {
		attempt.Phase = "producing-output"
	}
	attempt.UpdatedAt = now
	if m.store.PutTurnAttempt(attempt) == nil {
		m.appendCanonicalTurn(attempt)
		m.recordAttemptTimeline(attempt, "visible_output_started", "visible_output_observed", "visible assistant output started", "observed", attempt.FirstVisibleOutputAt)
	}
}

func appendUniqueString(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func sanitizedSideEffectType(method string) string {
	method = strings.ToLower(method)
	for _, value := range []string{"approval", "command", "file_change", "patch", "tool", "hook"} {
		if strings.Contains(method, value) {
			return value
		}
	}
	return "item"
}
