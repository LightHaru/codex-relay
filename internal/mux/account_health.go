package mux

import (
	"fmt"
	"math"
	"strings"
	"time"
)

func (m *Multiplexer) accountCircuitAllows(snapshot AccountSnapshot, now time.Time) bool {
	health, ok := m.store.AccountHealth(snapshot.ID)
	if !ok || (health.State != "open" && health.State != "half-open") {
		return true
	}
	if health.OpenUntil > now.UnixMilli() {
		return false
	}
	// Cooldown expiry alone is not evidence that quota returned. Require a
	// quota snapshot observed after the failure before allowing one half-open
	// probation dispatch.
	return snapshot.RateLimitsObservedAt > health.LastFailureAt && accountHasCapacity(snapshot)
}

func (m *Multiplexer) recordAccountFailure(accountID, reason string) {
	now := time.Now()
	if m.now != nil {
		now = m.now()
	}
	snapshot, snapshotFound := m.latestAccountQuotaSnapshot(accountID)
	health, _ := m.store.AccountHealth(accountID)
	health.AccountID = accountID
	health.ConsecutiveFailures++
	health.State = "open"
	category, safeReason := sanitizeRoutingFailure(reason)
	health.Reason = safeReason
	health.LastFailureCategory = category
	health.LastFailureAt = now.UnixMilli()
	if snapshotFound {
		health.LastQuotaObservedAt = snapshot.RateLimitsObservedAt
		if reset := blockingRateLimitResetAt(snapshot); reset != nil {
			health.BlockedResetAt = *reset
			health.LastQuotaResetAt = *reset
		}
	}
	health.RecoverySource = ""
	seconds := 30 * math.Pow(2, float64(min(health.ConsecutiveFailures-1, 5)))
	health.OpenUntil = now.Add(time.Duration(seconds) * time.Second).UnixMilli()
	_ = m.store.PutAccountHealth(health)
	// Force the next scheduler/UI observation to query the native Usage source
	// again. A cached allow=true response must never survive a real rejected
	// turn and immediately reopen the same worker.
	m.invalidateUsageQuotaSignal(accountID)
	eventID := fmt.Sprintf("circuit-%s-open-%d", accountID, health.LastFailureAt)
	m.recordTimelineEvent("", stateRoutingEvent{ID: eventID, EventType: "quota_circuit_opened", AccountID: accountID, ReasonCode: "skipped_open_circuit", Reason: safeReason, Result: "opened", CreatedAt: health.LastFailureAt})
}

func (m *Multiplexer) recordAccountSuccess(accountID string) {
	if accountID == "" {
		return
	}
	now := time.Now()
	if m.now != nil {
		now = m.now()
	}
	health, exists := m.store.AccountHealth(accountID)
	if !exists || (health.State == "closed" && health.ConsecutiveFailures == 0) {
		return
	}
	health.AccountID = accountID
	health.State = "closed"
	health.ConsecutiveFailures = 0
	health.OpenUntil = 0
	health.Reason = ""
	health.LastFailureCategory = ""
	health.BlockedResetAt = 0
	health.RecoverySource = "successful_turn"
	health.LastSuccessAt = now.UnixMilli()
	_ = m.store.PutAccountHealth(health)
	m.recordTimelineEvent("", stateRoutingEvent{ID: fmt.Sprintf("circuit-%s-closed-%d", accountID, health.LastSuccessAt), EventType: "quota_circuit_closed", AccountID: accountID, ReasonCode: "circuit_recovered", Reason: "a fresh successful turn closed the quota circuit", Result: "closed", CreatedAt: health.LastSuccessAt})
}

// observeAccountQuotaSnapshot keeps the latest credential-free quota evidence
// in memory and closes an open circuit only when the quota window itself has
// advanced. A fresh poll of the same reset epoch remains probationary and must
// still prove itself with a successful turn after cooldown.
func (m *Multiplexer) observeAccountQuotaSnapshot(snapshot AccountSnapshot) {
	if strings.TrimSpace(snapshot.ID) == "" {
		return
	}
	m.quotaMu.Lock()
	if m.quotaSnapshots == nil {
		m.quotaSnapshots = make(map[string]AccountSnapshot)
	}
	m.quotaSnapshots[snapshot.ID] = cloneAccountQuotaSnapshot(snapshot)
	m.quotaMu.Unlock()

	health, ok := m.store.AccountHealth(snapshot.ID)
	if !ok || (health.State != "open" && health.State != "half-open") {
		return
	}
	if snapshot.RateLimitsObservedAt <= health.LastFailureAt || !accountHasCapacity(snapshot) {
		return
	}
	now := time.Now()
	if m.now != nil {
		now = m.now()
	}
	reset := latestRateLimitResetAt(snapshot.RateLimits)
	resetAt := int64(0)
	if reset != nil {
		resetAt = *reset
	}
	resetAdvanced := health.BlockedResetAt > 0 &&
		(now.Unix() >= health.BlockedResetAt || resetAt > health.BlockedResetAt)
	if !resetAdvanced {
		return
	}
	health.State = "closed"
	health.ConsecutiveFailures = 0
	health.OpenUntil = 0
	health.Reason = ""
	health.LastFailureCategory = ""
	health.BlockedResetAt = 0
	health.LastQuotaObservedAt = snapshot.RateLimitsObservedAt
	health.LastQuotaResetAt = resetAt
	health.RecoverySource = "quota_reset"
	health.LastSuccessAt = now.UnixMilli()
	if m.store.PutAccountHealth(health) != nil {
		return
	}
	m.recordTimelineEvent("", stateRoutingEvent{
		ID:        fmt.Sprintf("circuit-%s-reset-%d", snapshot.ID, snapshot.RateLimitsObservedAt),
		EventType: "quota_circuit_closed", AccountID: snapshot.ID,
		ReasonCode: "circuit_recovered", Reason: "a newer quota window confirmed capacity after reset",
		Result: "closed", CreatedAt: snapshot.RateLimitsObservedAt,
	})
}

// blockingRateLimitResetAt returns the last reset needed to clear every
// currently exhausted window. If Usage explicitly denies routing without a
// 100%-used window, fall back to the latest reported epoch rather than
// guessing that the shorter window is sufficient.
func blockingRateLimitResetAt(snapshot AccountSnapshot) *int64 {
	var latest *int64
	if snapshot.RateLimits != nil {
		for _, window := range []*RateLimitWindow{snapshot.RateLimits.Primary, snapshot.RateLimits.Secondary} {
			if window == nil || window.UsedPercent < 100 || window.ResetsAt == nil {
				continue
			}
			if latest == nil || *window.ResetsAt > *latest {
				latest = cloneInt64Pointer(window.ResetsAt)
			}
		}
	}
	if latest != nil {
		return latest
	}
	return latestRateLimitResetAt(snapshot.RateLimits)
}

func latestRateLimitResetAt(limits *RateLimits) *int64 {
	if limits == nil {
		return nil
	}
	var latest *int64
	for _, window := range []*RateLimitWindow{limits.Primary, limits.Secondary} {
		if window == nil || window.ResetsAt == nil {
			continue
		}
		if latest == nil || *window.ResetsAt > *latest {
			latest = cloneInt64Pointer(window.ResetsAt)
		}
	}
	return latest
}

func (m *Multiplexer) latestAccountQuotaSnapshot(accountID string) (AccountSnapshot, bool) {
	m.quotaMu.RLock()
	snapshot, ok := m.quotaSnapshots[accountID]
	m.quotaMu.RUnlock()
	if !ok {
		return AccountSnapshot{}, false
	}
	return cloneAccountQuotaSnapshot(snapshot), true
}

func cloneAccountQuotaSnapshot(snapshot AccountSnapshot) AccountSnapshot {
	copy := AccountSnapshot{
		ID: snapshot.ID, Enabled: snapshot.Enabled, Connected: snapshot.Connected,
		AuthType: snapshot.AuthType, RateLimitAvailable: snapshot.RateLimitAvailable,
		RateLimitsObservedAt: snapshot.RateLimitsObservedAt, QuotaSource: snapshot.QuotaSource,
	}
	copy.QuotaAllowed = cloneBoolPointer(snapshot.QuotaAllowed)
	copy.QuotaLimitReached = cloneBoolPointer(snapshot.QuotaLimitReached)
	copy.NextRateLimitResetAt = cloneInt64Pointer(snapshot.NextRateLimitResetAt)
	if snapshot.RateLimits != nil {
		copy.RateLimits = &RateLimits{RateLimitReachedType: snapshot.RateLimits.RateLimitReachedType}
		cloneWindow := func(window *RateLimitWindow) *RateLimitWindow {
			if window == nil {
				return nil
			}
			return &RateLimitWindow{
				UsedPercent:        window.UsedPercent,
				WindowDurationMins: cloneInt64Pointer(window.WindowDurationMins),
				ResetsAt:           cloneInt64Pointer(window.ResetsAt),
			}
		}
		copy.RateLimits.Primary = cloneWindow(snapshot.RateLimits.Primary)
		copy.RateLimits.Secondary = cloneWindow(snapshot.RateLimits.Secondary)
	}
	return copy
}

func (m *Multiplexer) markHalfOpenIfReady(accountID string, now time.Time) {
	health, ok := m.store.AccountHealth(accountID)
	if !ok || health.State != "open" || health.OpenUntil > now.UnixMilli() {
		return
	}
	health.State = "half-open"
	_ = m.store.PutAccountHealth(health)
}
