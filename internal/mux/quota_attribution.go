package mux

import (
	"context"
	"time"
)

// recordAttemptQuotaBefore stores only the effective remaining percentage and
// observation time used by the scheduler. It never persists the upstream Usage
// payload, credentials, prompt, or request parameters.
func (m *Multiplexer) recordAttemptQuotaBefore(threadID string, snapshot AccountSnapshot) {
	route, ok := m.store.ThreadRoute(threadID)
	if !ok || route.ActiveAttemptID == "" {
		return
	}
	attempt, ok := m.store.TurnAttempt(route.ActiveAttemptID)
	if !ok {
		return
	}
	remaining, known := routableRemainingPercent(snapshot, m.now())
	attempt.AccountID = snapshot.ID
	attempt.QuotaBeforeRemaining = nil
	attempt.QuotaBeforeObservedAt = 0
	attempt.QuotaAfterRemaining = nil
	attempt.QuotaAfterObservedAt = 0
	attempt.QuotaAttribution = "unavailable"
	if known && snapshot.RateLimitsObservedAt > 0 {
		value := clampRoutePercent(remaining)
		attempt.QuotaBeforeRemaining = &value
		attempt.QuotaBeforeObservedAt = snapshot.RateLimitsObservedAt
		attempt.QuotaAttribution = "waiting_for_refreshed_quota"
	}
	attempt.UpdatedAt = m.now().UnixMilli()
	if m.store.PutTurnAttempt(attempt) == nil {
		m.appendCanonicalTurn(attempt)
	}
}

func (m *Multiplexer) scheduleQuotaAttribution(threadID, accountID, attemptID string) {
	if threadID == "" || accountID == "" || attemptID == "" {
		return
	}
	m.runAsync(func() {
		// The upstream rate-limit snapshot can lag the terminal turn event. A
		// short delay avoids labelling the pre-turn cached value as an after-turn
		// observation while keeping completion off the critical response path.
		timer := time.NewTimer(350 * time.Millisecond)
		defer timer.Stop()
		<-timer.C
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		snapshot, err := m.accountSnapshotWithProfile(ctx, accountID, false)
		attempt, ok := m.store.TurnAttempt(attemptID)
		if !ok || attempt.ThreadID != threadID || attempt.AccountID != accountID {
			return
		}
		if err != nil || attempt.QuotaBeforeRemaining == nil {
			attempt.QuotaAttribution = "unavailable"
			attempt.UpdatedAt = m.now().UnixMilli()
			if m.store.PutTurnAttempt(attempt) == nil {
				m.appendCanonicalTurn(attempt)
			}
			return
		}
		remaining, known := routableRemainingPercent(snapshot, m.now())
		if !known || snapshot.RateLimitsObservedAt <= attempt.QuotaBeforeObservedAt {
			attempt.QuotaAttribution = "unavailable"
			attempt.UpdatedAt = m.now().UnixMilli()
			if m.store.PutTurnAttempt(attempt) == nil {
				m.appendCanonicalTurn(attempt)
			}
			return
		}
		after := clampRoutePercent(remaining)
		attempt.QuotaAfterRemaining = &after
		attempt.QuotaAfterObservedAt = snapshot.RateLimitsObservedAt
		eventType := "quota_attribution_unconfirmed"
		reasonCode := "quota_snapshot_no_measurable_change"
		reason := "a refreshed quota snapshot did not show a measurable decrease"
		result := "unconfirmed"
		if after < *attempt.QuotaBeforeRemaining {
			attempt.QuotaAttribution = "confirmed"
			eventType = "quota_attribution_confirmed"
			reasonCode = "quota_snapshot_confirmed"
			reason = "quota attribution confirmed by a newer snapshot with a measurable decrease"
			result = "confirmed"
		} else if after > *attempt.QuotaBeforeRemaining {
			attempt.QuotaAttribution = "refresh_crossed_reset_window"
			reasonCode = "quota_snapshot_crossed_reset"
			reason = "the refreshed quota snapshot crossed a reset and cannot attribute this turn"
		} else {
			attempt.QuotaAttribution = "refreshed_no_measurable_change"
		}
		attempt.UpdatedAt = m.now().UnixMilli()
		if m.store.PutTurnAttempt(attempt) != nil {
			return
		}
		m.appendCanonicalTurn(attempt)
		if attempt.QuotaAttribution == "confirmed" {
			if route, found := m.store.ThreadRoute(threadID); found {
				route.LastQuotaAccountID = accountID
				route.UpdatedAt = m.now().UnixMilli()
				if m.store.PutThreadRoute(route) == nil {
					_ = m.persistCanonicalSnapshot(threadID)
				}
			}
		}
		m.recordTimelineEvent(threadID, stateRoutingEvent{
			ID: attemptID + ":quota-attribution", EventType: eventType,
			AccountID: accountID, Generation: attempt.Generation,
			ReasonCode: reasonCode, Reason: reason,
			Result: result, CreatedAt: attempt.QuotaAfterObservedAt, RemainingPercent: &after,
		})
	})
}
