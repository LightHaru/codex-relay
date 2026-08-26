package mux

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/LightHaru/codex-relay/internal/state"
)

const unifiedQuotaRefreshTTL = 10 * time.Second

// refreshUnifiedQuota gives the pool status endpoint a bounded live view. The
// persisted PoolState is intentionally crash-safe, but it can start in
// PROBING/WARMING after a restart and therefore cannot be the only source for
// a quota dashboard. A short single-flight window keeps repeated renderer
// polls cheap while still re-reading every management child often enough to
// notice a reset or depletion.
func (m *Multiplexer) refreshUnifiedQuota(ctx context.Context) {
	if m == nil || !m.unifiedPoolEnabled() {
		return
	}
	now := time.Now()
	if m.now != nil {
		now = m.now()
	}
	m.quotaRefreshMu.Lock()
	if !m.quotaRefreshAt.IsZero() && now.Sub(m.quotaRefreshAt) < unifiedQuotaRefreshTTL {
		m.quotaRefreshMu.Unlock()
		return
	}
	// Mark before the I/O so concurrent status requests do not fan out one
	// quota read per renderer surface. A failed read is retried after the same
	// short interval and is represented by the account error/status endpoints.
	m.quotaRefreshAt = now
	m.quotaRefreshMu.Unlock()
	_ = m.accountSnapshots(ctx, false)
}

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
	// turn and immediately reopen the same Relay task authority.
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
	m.persistPoolSourceObservation(snapshot)

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

// persistPoolSourceObservation projects credential-free account and quota
// evidence into the v3 pool state. The model gateway consumes only this
// projection plus the selected source's auth file; it never asks a task worker
// to choose or switch accounts.
func (m *Multiplexer) persistPoolSourceObservation(snapshot AccountSnapshot) {
	if !snapshot.Connected {
		// A management child can briefly fail account/read while its isolated
		// process is restarting, refreshing plugins, or losing a renderer
		// request. Do not discard a still-present credential source on that
		// transient control-plane error; the Gateway will quarantine it on a
		// real 401/credential rejection. A missing or malformed auth file still
		// remains disconnected and cannot enter the pool.
		if account, ok := m.store.Account(snapshot.ID); ok {
			if credentials, err := readAuthFile(filepath.Join(account.CodexHome, "auth.json")); err == nil && strings.TrimSpace(credentials.Tokens.AccessToken) != "" {
				snapshot.Connected = true
				snapshot.AuthType = "chatgpt"
			}
		}
	}
	now := time.Now()
	if m.now != nil {
		now = m.now()
	}
	_, _ = m.store.UpdateCredentialSource(snapshot.ID, func(source *state.CredentialSourceState) error {
		wasDepleted := source.MembershipState == state.SourceDepleted
		previousQuotaEvidence := source.QuotaEvidence
		previousQuotaObservedAt := source.QuotaEvidence.ObservedAt
		source.Enabled = snapshot.Enabled
		source.Connected = snapshot.Connected
		source.LastObservedAt = now.UnixMilli()
		source.AuthState = "disconnected"
		source.QuotaState = "unknown"
		source.QuotaEvidence = state.QuotaEvidence{
			Allowed: cloneBoolPointer(snapshot.QuotaAllowed), ObservedAt: snapshot.RateLimitsObservedAt,
			Source: snapshot.QuotaSource,
		}
		if snapshot.QuotaLimitReached != nil {
			source.QuotaEvidence.LimitReached = *snapshot.QuotaLimitReached
		}
		if snapshot.RateLimits != nil {
			if snapshot.RateLimits.Primary != nil {
				used := snapshot.RateLimits.Primary.UsedPercent
				source.QuotaEvidence.ShortUsed = &used
			}
			if snapshot.RateLimits.Secondary != nil {
				used := snapshot.RateLimits.Secondary.UsedPercent
				source.QuotaEvidence.LongUsed = &used
			}
		}
		if snapshot.NextRateLimitResetAt != nil {
			source.ResetEpoch = *snapshot.NextRateLimitResetAt
			source.QuotaEvidence.ResetEpoch = *snapshot.NextRateLimitResetAt
		}
		if wasDepleted {
			source.Connected = snapshot.Connected
			if snapshot.Connected && snapshot.AuthType == "chatgpt" {
				source.AuthState = "authenticated"
			}
			freshRecoveryEvidence := accountHasCapacity(snapshot) &&
				snapshot.RateLimitsObservedAt > previousQuotaObservedAt &&
				(snapshot.RateLimitAvailable || (snapshot.QuotaAllowed != nil && *snapshot.QuotaAllowed))
			if !freshRecoveryEvidence {
				source.MembershipState = state.SourceDepleted
				source.QuotaState = "depleted"
				source.QuotaEvidence = previousQuotaEvidence
				return nil
			}
		}

		switch {
		case snapshot.PendingLogin:
			source.MembershipState = state.SourceLoginPending
		case !snapshot.Enabled:
			source.MembershipState = state.SourceDraining
		case !snapshot.Connected || snapshot.AuthType != "chatgpt":
			source.MembershipState = state.SourceProvisioning
		case !accountHasCapacity(snapshot):
			source.AuthState = "authenticated"
			source.MembershipState = state.SourceDepleted
			source.QuotaState = "depleted"
			source.DepletedAt = now.UnixMilli()
		case snapshot.RateLimitAvailable || snapshot.QuotaAllowed != nil:
			source.AuthState = "authenticated"
			source.MembershipState = state.SourceAvailable
			source.QuotaState = "available"
			source.RecoveredAt = now.UnixMilli()
		case snapshot.Connected:
			// A connected subscription whose quota endpoint is temporarily
			// unknown is a bounded probation candidate. The first real model
			// request is the probe; a structured rejection immediately excludes
			// it from the same logical request.
			source.AuthState = "authenticated"
			source.MembershipState = state.SourceProbation
		}
		return nil
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
