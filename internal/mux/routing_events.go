package mux

import (
	"fmt"
	"strings"
	"time"

	"github.com/LightHaru/codex-relay/internal/state"
)

type stateRoutingEvent struct {
	ID               string
	EventType        string
	AttemptID        string
	FromAccountID    string
	ToAccountID      string
	AccountID        string
	Generation       uint64
	ReasonCode       string
	Reason           string
	Result           string
	CreatedAt        int64
	QuotaSnapshotAt  int64
	RemainingPercent *float64
	Score            *float64
	Policy           state.RoutingPolicy
}

func routingReasonText(reasonCode string) string {
	switch reasonCode {
	case "handoff_quota_exhausted":
		return "the previous worker had no confirmed quota before side effects"
	case "handoff_rotate_boundary":
		return "the task moved at a safe completed-turn rotate boundary"
	case "handoff_balanced_boundary":
		return "the task moved at a safe completed-turn balancing boundary"
	default:
		return "the task moved at a verified safe boundary"
	}
}

func (m *Multiplexer) recordTimelineEvent(threadID string, event stateRoutingEvent) {
	threadID = strings.TrimSpace(threadID)
	if strings.TrimSpace(event.ID) == "" {
		event.ID = fmt.Sprintf("event-%d-%d", time.Now().UnixMilli(), m.serverSequence.Add(1))
	}
	if event.CreatedAt == 0 {
		event.CreatedAt = time.Now().UnixMilli()
	}
	toAccountID := event.ToAccountID
	if toAccountID == "" {
		toAccountID = event.AccountID
	}
	policy := event.Policy
	if !policy.Valid() {
		policy = m.effectiveRoutingPolicy()
	}
	decision := state.RoutingDecision{
		ID: event.ID, ThreadID: threadID, AttemptID: event.AttemptID,
		FromAccountID: event.FromAccountID, ToAccountID: toAccountID,
		Policy: policy, EventType: event.EventType,
		ReasonCode: event.ReasonCode, Reason: event.Reason, Result: event.Result,
		Generation: event.Generation, QuotaSnapshotAt: event.QuotaSnapshotAt,
		RemainingPercent: event.RemainingPercent, Score: event.Score, CreatedAt: event.CreatedAt,
	}
	inserted, err := m.store.RecordDecisionIfNew(decision)
	if err != nil || !inserted {
		return
	}
	m.appendCanonicalDecision(decision)
	m.publish(Event{
		ID: event.ID, Type: event.EventType, ThreadID: threadID, AttemptID: event.AttemptID,
		AccountID: toAccountID, PreviousAccountID: event.FromAccountID,
		RouteGeneration: event.Generation, Message: event.Reason, Data: decision,
	})
}

func (m *Multiplexer) recordAttemptTimeline(attempt state.TurnAttempt, eventType, reasonCode, reason, result string, timestamp int64) {
	if attempt.ID == "" || attempt.ThreadID == "" || eventType == "" {
		return
	}
	if timestamp == 0 {
		timestamp = time.Now().UnixMilli()
	}
	m.recordTimelineEvent(attempt.ThreadID, stateRoutingEvent{
		ID: attempt.ID + ":" + eventType, EventType: eventType, AttemptID: attempt.ID,
		AccountID: attempt.AccountID, Generation: attempt.Generation,
		ReasonCode: reasonCode, Reason: reason, Result: result, CreatedAt: timestamp,
	})
}

func (m *Multiplexer) recordHandoffTimeline(handoff state.Handoff, reasonCode, reason string) {
	if strings.TrimSpace(reasonCode) == "" {
		reasonCode = handoff.ReasonCode
	}
	if strings.TrimSpace(reason) == "" {
		reason = handoff.Reason
	}
	phase := strings.ToLower(strings.TrimSpace(handoff.Phase))
	if phase == "" {
		phase = "unknown"
	}
	result := phase
	if phase == "committed" {
		result = "completed"
	} else if phase == "failed" || phase == "rolled_back" || phase == "rolled-back" {
		result = "failed"
	}
	m.recordTimelineEvent(handoff.ThreadID, stateRoutingEvent{
		ID: handoff.ID + ":" + phase, EventType: "handoff_" + strings.ReplaceAll(phase, "-", "_"),
		FromAccountID: handoff.SourceAccountID, ToAccountID: handoff.TargetAccountID,
		Generation: handoff.TargetGeneration, ReasonCode: reasonCode, Reason: reason,
		Result: result, CreatedAt: handoff.UpdatedAt,
	})
}

// claimCompletedQuotaFailure is a persistent idempotency fence for terminal
// quota notifications that have no renderer-owned active turn (notably Goal
// continuations). The same app-server may emit duplicate terminal events or a
// renderer may reconnect while one is in flight; only one of them may start a
// cross-account handoff.
func (m *Multiplexer) claimCompletedQuotaFailure(threadID, accountID, turnID string) bool {
	threadID = strings.TrimSpace(threadID)
	accountID = strings.TrimSpace(accountID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || accountID == "" || turnID == "" {
		return false
	}
	now := time.Now()
	if m.now != nil {
		now = m.now()
	}
	decision := state.RoutingDecision{
		ID:       "quota-terminal:" + threadID + ":" + turnID + ":" + accountID,
		ThreadID: threadID, ToAccountID: accountID,
		Policy: m.effectiveRoutingPolicy(), EventType: "quota_failure_detected",
		ReasonCode: "quota_rejected_terminal_turn",
		Reason:     "the upstream worker rejected a terminal turn because quota was exhausted",
		Result:     "claimed", CreatedAt: now.UnixMilli(),
	}
	inserted, err := m.store.RecordDecisionIfNew(decision)
	if err != nil || !inserted {
		return false
	}
	m.appendCanonicalDecision(decision)
	m.publish(Event{
		ID: decision.ID, Type: decision.EventType, ThreadID: threadID,
		AccountID: accountID, Timestamp: decision.CreatedAt,
		Message: decision.Reason, Data: decision,
	})
	return true
}
