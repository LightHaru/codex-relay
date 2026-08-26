package mux

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/LightHaru/codex-relay/internal/state"
)

type RouterStatus struct {
	ContractVersion      int                       `json:"contractVersion"`
	StateVersion         int                       `json:"stateVersion"`
	Policy               state.RoutingPolicy       `json:"policy"`
	EffectivePolicy      state.RoutingPolicy       `json:"effectivePolicy"`
	PolicyFallbackReason string                    `json:"policyFallbackReason,omitempty"`
	HandoffSupported     bool                      `json:"handoffSupported"`
	CompatibilityProfile string                    `json:"compatibilityProfile"`
	Scheduler            state.SchedulerState      `json:"scheduler"`
	AccountHealth        []state.AccountHealth     `json:"accountHealth"`
	Capabilities         []state.AccountCapability `json:"capabilities"`
	Accounts             []AccountSnapshot         `json:"accounts"`
	Pool                 PoolStatus                `json:"pool"`
	AccountRoutes        []AccountRouteStatus      `json:"accountRoutes"`
	ActiveTurnCount      int                       `json:"activeTurnCount"`
	RecoveryTaskCount    int                       `json:"recoveryTaskCount"`
	Handoffs             []state.Handoff           `json:"handoffs"`
	EligiblePool         []RoutingCandidate        `json:"eligiblePool"`
	NextCandidate        *RoutingCandidate         `json:"nextCandidate,omitempty"`
	Timeline             []RoutingTimelineEvent    `json:"timeline"`
}

// PoolStatus is the user-facing quota contract. Upstream subscriptions remain
// credential-isolated management sources, while unified Relay presents their
// schedulable quota as one additive pool: five fully available subscriptions
// report 500/500%.
type PoolStatus struct {
	PoolID                    string  `json:"poolId"`
	Revision                  uint64  `json:"revision"`
	Health                    string  `json:"health"`
	ActiveLeaseCount          int     `json:"activeLeaseCount"`
	ConnectedSubscriptions    int     `json:"connectedSubscriptions"`
	MaximumPercent            float64 `json:"maximumPercent"`
	ConfirmedRemainingPercent float64 `json:"confirmedRemainingPercent"`
	ConfirmedUsedPercent      float64 `json:"confirmedUsedPercent"`
	KnownSubscriptions        int     `json:"knownSubscriptions"`
	UnknownSubscriptions      int     `json:"unknownSubscriptions"`
	AvailableSubscriptions    int     `json:"availableSubscriptions"`
	DepletedSubscriptions     int     `json:"depletedSubscriptions"`
	NextResetAt               int64   `json:"nextResetAt,omitempty"`
	QuotaUpdatedAt            int64   `json:"quotaUpdatedAt,omitempty"`
	RoutingCapacityOnly       bool    `json:"routingCapacityOnly"`
}

type RoutingScoreComponents struct {
	Deficit            float64 `json:"deficit"`
	QuotaWeight        float64 `json:"quotaWeight"`
	ReservationPenalty float64 `json:"reservationPenalty"`
	FinalScore         float64 `json:"finalScore"`
}

type RoutingCandidate struct {
	AccountID        string                 `json:"accountId"`
	Label            string                 `json:"label"`
	PlanLabel        string                 `json:"planLabel,omitempty"`
	RemainingPercent float64                `json:"remainingPercent"`
	QuotaKnown       bool                   `json:"quotaKnown"`
	Score            float64                `json:"score"`
	ScoreComponents  RoutingScoreComponents `json:"scoreComponents"`
	ReasonCode       string                 `json:"reasonCode"`
	IsPreview        bool                   `json:"isPreview"`
}

type AccountRouteStatus struct {
	AccountID           string                 `json:"accountId"`
	SubscriptionIndex   int                    `json:"subscriptionIndex"`
	Label               string                 `json:"label"`
	DisplayName         string                 `json:"displayName,omitempty"`
	MaskedIdentity      string                 `json:"maskedIdentity,omitempty"`
	PlanLabel           string                 `json:"planLabel,omitempty"`
	Enabled             bool                   `json:"enabled"`
	Connected           bool                   `json:"connected"`
	Eligible            bool                   `json:"eligible"`
	AuthType            string                 `json:"authType"`
	Health              string                 `json:"health"`
	CircuitState        string                 `json:"circuitState"`
	ShortRemaining      *float64               `json:"shortRemaining,omitempty"`
	LongRemaining       *float64               `json:"longRemaining,omitempty"`
	ConfirmedRemaining  *float64               `json:"confirmedRemainingPercent,omitempty"`
	MaximumContribution float64                `json:"maximumContributionPercent"`
	EffectiveCapacity   *float64               `json:"effectiveCapacityPercent,omitempty"`
	QuotaKnown          bool                   `json:"quotaKnown"`
	QuotaSnapshotAt     int64                  `json:"quotaSnapshotAt,omitempty"`
	QuotaFreshness      string                 `json:"quotaFreshness"`
	CooldownUntil       int64                  `json:"cooldownUntil,omitempty"`
	ConsecutiveFailures int                    `json:"consecutiveFailures"`
	InFlightTurns       int                    `json:"inFlightTurns"`
	ReservedTurnUnits   float64                `json:"reservedTurnUnits"`
	DispatchDeficit     float64                `json:"dispatchDeficit"`
	DispatchSequence    uint64                 `json:"dispatchSequence"`
	LastSelectedAt      int64                  `json:"lastSelectedAt,omitempty"`
	ResetAt             int64                  `json:"resetAt,omitempty"`
	LastFailure         string                 `json:"lastFailure,omitempty"`
	LastFailureCategory string                 `json:"lastFailureCategory,omitempty"`
	ScoreComponents     RoutingScoreComponents `json:"scoreComponents"`
	ReasonCode          string                 `json:"reasonCode"`
}

type RoutingWorkerIdentity struct {
	AccountID         string `json:"accountId"`
	Label             string `json:"label"`
	DisplayName       string `json:"displayName,omitempty"`
	MaskedIdentity    string `json:"maskedIdentity,omitempty"`
	PlanLabel         string `json:"planLabel,omitempty"`
	SubscriptionIndex int    `json:"subscriptionIndex"`
}

type RoutingReservationStatus struct {
	AttemptID string  `json:"attemptId,omitempty"`
	AccountID string  `json:"accountId"`
	Weight    float64 `json:"weight"`
	ExpiresAt int64   `json:"expiresAt"`
}

type QuotaAttributionStatus struct {
	Status           string   `json:"status"`
	AccountID        string   `json:"accountId,omitempty"`
	BeforeRemaining  *float64 `json:"beforeRemainingPercent,omitempty"`
	BeforeObservedAt int64    `json:"beforeObservedAt,omitempty"`
	AfterRemaining   *float64 `json:"afterRemainingPercent,omitempty"`
	AfterObservedAt  int64    `json:"afterObservedAt,omitempty"`
	DeltaPercent     *float64 `json:"deltaPercent,omitempty"`
}

type RoutingTimelineEvent struct {
	ID               string   `json:"id"`
	Type             string   `json:"type"`
	Timestamp        int64    `json:"timestamp"`
	Generation       uint64   `json:"generation,omitempty"`
	AccountID        string   `json:"accountId,omitempty"`
	SourceAccountID  string   `json:"sourceAccountId,omitempty"`
	TargetAccountID  string   `json:"targetAccountId,omitempty"`
	ReasonCode       string   `json:"reasonCode,omitempty"`
	Reason           string   `json:"reason,omitempty"`
	Result           string   `json:"result,omitempty"`
	RemainingPercent *float64 `json:"remainingPercent,omitempty"`
}

type ThreadRouteStatus struct {
	ContractVersion          int                        `json:"contractVersion"`
	ThreadID                 string                     `json:"threadId"`
	Route                    state.ThreadRoute          `json:"route"`
	Attempt                  *state.TurnAttempt         `json:"attempt,omitempty"`
	Checkpoint               *state.CanonicalCheckpoint `json:"checkpoint,omitempty"`
	Handoffs                 []state.Handoff            `json:"handoffs"`
	LatestHandoff            *state.Handoff             `json:"latestHandoff,omitempty"`
	LastDecision             *state.RoutingDecision     `json:"lastDecision,omitempty"`
	NextCandidate            *RoutingCandidate          `json:"nextCandidate,omitempty"`
	Controller               *RoutingWorkerIdentity     `json:"controller,omitempty"`
	CurrentOwner             *RoutingWorkerIdentity     `json:"currentOwner,omitempty"`
	ActiveWorker             *RoutingWorkerIdentity     `json:"activeWorker,omitempty"`
	LastCompletedWorker      *RoutingWorkerIdentity     `json:"lastCompletedWorker,omitempty"`
	LastQuotaConsumingWorker *RoutingWorkerIdentity     `json:"lastQuotaConsumingWorker,omitempty"`
	PreviousWorker           *RoutingWorkerIdentity     `json:"previousWorker,omitempty"`
	RequestedPolicy          state.RoutingPolicy        `json:"requestedPolicy"`
	EffectivePolicy          state.RoutingPolicy        `json:"effectivePolicy"`
	PolicyFallbackReason     string                     `json:"policyFallbackReason,omitempty"`
	NextCandidateIsPreview   bool                       `json:"nextCandidateIsPreview"`
	RecoveryRequired         bool                       `json:"recoveryRequired"`
	ActiveTurnState          string                     `json:"activeTurnState"`
	Reservation              *RoutingReservationStatus  `json:"reservation,omitempty"`
	QuotaSnapshotAt          int64                      `json:"quotaSnapshotAt,omitempty"`
	QuotaFreshness           string                     `json:"quotaFreshness"`
	SchedulerDecisionID      string                     `json:"schedulerDecisionId,omitempty"`
	QuotaAttribution         QuotaAttributionStatus     `json:"quotaAttribution"`
	Pool                     PoolStatus                 `json:"pool"`
	Workers                  []AccountRouteStatus       `json:"workers"`
	Timeline                 []RoutingTimelineEvent     `json:"timeline"`
}

func (m *Multiplexer) RouterStatus(ctx context.Context) RouterStatus {
	m.turnMu.Lock()
	activeCount := len(m.activeTurns)
	m.turnMu.Unlock()
	if m.unifiedPoolEnabled() {
		// Refresh credential-free quota evidence before projecting the pool. This
		// prevents a fresh Relay launch from reporting the persisted
		// "warming/0%" state until a separate account-menu request happens to
		// probe the subscriptions.
		m.refreshUnifiedQuota(ctx)
		recoveryCount := 0
		for _, task := range m.store.TaskRecords() {
			if task.RecoveryState != "" {
				recoveryCount++
			}
		}
		return RouterStatus{
			ContractVersion: 2,
			StateVersion:    state.PoolSchemaVersion,
			Policy:          state.RoutingPolicySticky,
			EffectivePolicy: state.RoutingPolicySticky,
			Scheduler: state.SchedulerState{
				Policy:         state.RoutingPolicySticky,
				Deficits:       map[string]float64{},
				Dispatches:     map[string]uint64{},
				LastSelectedAt: map[string]int64{},
				Reservations:   map[string]state.Reservation{},
			},
			Pool:              m.unifiedPoolStatus(),
			ActiveTurnCount:   activeCount,
			RecoveryTaskCount: recoveryCount,
		}
	}
	routes := m.store.ThreadRoutes()
	recoveryCount := 0
	for _, route := range routes {
		if route.RecoveryRequired {
			recoveryCount++
		}
	}
	accounts := m.Accounts(ctx)
	eligiblePool := m.previewCandidates(accounts)
	var nextCandidate *RoutingCandidate
	if len(eligiblePool) > 0 {
		candidate := eligiblePool[0]
		nextCandidate = &candidate
	}
	policy, effectivePolicy := m.store.RoutingPolicy(), m.effectiveRoutingPolicy()
	poolStatus := m.poolStatus(accounts)
	return RouterStatus{
		ContractVersion: 1, StateVersion: 2, Policy: policy, EffectivePolicy: effectivePolicy,
		PolicyFallbackReason: m.policyFallbackReason(),
		HandoffSupported:     m.safeHandoff, CompatibilityProfile: compatibilityProfileOrUnknown(m.compatibilityProfile), Scheduler: m.store.Scheduler(),
		AccountHealth: sanitizedAccountHealth(m.store.AccountHealthAll()), Capabilities: m.routerCapabilities(), Accounts: sanitizedRoutingSnapshots(accounts), Pool: poolStatus,
		AccountRoutes:   m.accountRouteStatuses(accounts),
		ActiveTurnCount: activeCount, RecoveryTaskCount: recoveryCount, Handoffs: sanitizedHandoffs(m.store.Handoffs()),
		EligiblePool: eligiblePool, NextCandidate: nextCandidate, Timeline: m.routingTimeline("", 100),
	}
}

func (m *Multiplexer) unifiedPoolStatus() PoolStatus {
	pool := m.store.PoolState()
	result := PoolStatus{
		PoolID: pool.PoolID, Revision: pool.Revision, Health: pool.Health,
		ActiveLeaseCount: len(pool.ActiveLeases), RoutingCapacityOnly: false,
	}
	for _, sourceID := range pool.SourceOrder {
		source, ok := pool.Sources[sourceID]
		if !ok || !source.Enabled || source.MembershipState == state.SourceRemoved || source.MembershipState == state.SourceDraining {
			continue
		}
		result.MaximumPercent += 100
		if source.Connected && source.AuthState == "authenticated" {
			result.ConnectedSubscriptions++
		}
		evidence := source.QuotaEvidence
		known := evidence.Allowed != nil || evidence.LimitReached || evidence.ShortUsed != nil || evidence.LongUsed != nil
		if known {
			result.KnownSubscriptions++
			used := 0.0
			if evidence.ShortUsed != nil && *evidence.ShortUsed > used {
				used = *evidence.ShortUsed
			}
			if evidence.LongUsed != nil && *evidence.LongUsed > used {
				used = *evidence.LongUsed
			}
			if evidence.ExplicitlyDepleted() {
				used = 100
			}
			used = clampRoutePercent(used)
			result.ConfirmedUsedPercent += used
			result.ConfirmedRemainingPercent += 100 - used
			if evidence.ObservedAt > 0 && (result.QuotaUpdatedAt == 0 || evidence.ObservedAt < result.QuotaUpdatedAt) {
				result.QuotaUpdatedAt = evidence.ObservedAt
			}
		} else {
			result.UnknownSubscriptions++
		}
		switch source.MembershipState {
		case state.SourceAvailable, state.SourceActive, state.SourceProbation:
			if source.Connected && source.AuthState == "authenticated" && !evidence.ExplicitlyDepleted() {
				result.AvailableSubscriptions++
			} else {
				result.DepletedSubscriptions++
			}
		case state.SourceDepleted:
			result.DepletedSubscriptions++
		}
		reset := source.ResetEpoch
		if evidence.ResetEpoch > 0 {
			reset = evidence.ResetEpoch
		}
		if reset > 0 && (result.NextResetAt == 0 || reset < result.NextResetAt) {
			result.NextResetAt = reset
		}
	}
	return result
}

func sanitizedRoutingSnapshots(accounts []AccountSnapshot) []AccountSnapshot {
	result := make([]AccountSnapshot, 0, len(accounts))
	for _, account := range accounts {
		account.Username = ""
		account.Email = ""
		account.Error = ""
		account.RateLimitError = ""
		account.RawAccount = nil
		result = append(result, account)
	}
	return result
}

func sanitizedAccountHealth(values []state.AccountHealth) []state.AccountHealth {
	result := make([]state.AccountHealth, 0, len(values))
	for _, value := range values {
		category, message := sanitizeRoutingFailure(value.Reason)
		if value.Reason != "" {
			value.Reason = message
			if value.LastFailureCategory == "" {
				value.LastFailureCategory = category
			}
		}
		result = append(result, value)
	}
	return result
}

func sanitizedHandoffs(values []state.Handoff) []state.Handoff {
	result := make([]state.Handoff, 0, len(values))
	for _, value := range values {
		if value.Failure != "" {
			_, value.Failure = sanitizeRoutingFailure(value.Failure)
		}
		value.Reason = safeRoutingReason(value.ReasonCode, "handoff_"+strings.ToLower(value.Phase), value.Reason)
		result = append(result, value)
	}
	return result
}

func (m *Multiplexer) poolStatus(accounts []AccountSnapshot) PoolStatus {
	now := time.Now()
	if m.now != nil {
		now = m.now()
	}
	result := PoolStatus{RoutingCapacityOnly: true}
	for _, account := range accounts {
		if !account.Enabled || !account.Connected || account.AuthType != "chatgpt" {
			continue
		}
		result.ConnectedSubscriptions++
		result.MaximumPercent += 100
		remaining, known := routableRemainingPercent(account, now)
		if known {
			result.KnownSubscriptions++
			result.ConfirmedRemainingPercent += clampRoutePercent(remaining)
			result.ConfirmedUsedPercent += 100 - clampRoutePercent(remaining)
			if result.QuotaUpdatedAt == 0 || account.RateLimitsObservedAt < result.QuotaUpdatedAt {
				result.QuotaUpdatedAt = account.RateLimitsObservedAt
			}
		} else {
			result.UnknownSubscriptions++
		}
		if accountHasCapacity(account) && m.accountCircuitAllows(account, now) {
			result.AvailableSubscriptions++
		} else {
			result.DepletedSubscriptions++
		}
		if account.NextRateLimitResetAt != nil && *account.NextRateLimitResetAt > 0 &&
			(result.NextResetAt == 0 || *account.NextRateLimitResetAt < result.NextResetAt) {
			result.NextResetAt = *account.NextRateLimitResetAt
		}
	}
	return result
}

func (m *Multiplexer) accountRouteStatuses(accounts []AccountSnapshot) []AccountRouteStatus {
	now := time.Now()
	if m.now != nil {
		now = m.now()
	}
	scheduler := m.store.Scheduler()
	inFlight := make(map[string]int)
	m.turnMu.Lock()
	for _, active := range m.activeTurns {
		inFlight[active.route.accountID]++
	}
	m.turnMu.Unlock()
	result := make([]AccountRouteStatus, 0, len(accounts))
	preview := m.previewCandidates(accounts)
	previewByID := make(map[string]RoutingCandidate, len(preview))
	for _, candidate := range preview {
		previewByID[candidate.AccountID] = candidate
	}
	hasKnownEligible := false
	hasSafeEligible := false
	for _, account := range accounts {
		remaining, known := routableRemainingPercent(account, now)
		if accountHasCapacity(account) && m.accountCircuitAllows(account, now) {
			hasKnownEligible = hasKnownEligible || known
			hasSafeEligible = hasSafeEligible || (known && remaining >= 5)
		}
	}
	for index, account := range accounts {
		value := AccountRouteStatus{
			AccountID: account.ID, SubscriptionIndex: index + 1, Label: account.Label,
			DisplayName: account.DisplayName, MaskedIdentity: maskedRoutingIdentity(account), PlanLabel: account.PlanLabel,
			Enabled: account.Enabled, Connected: account.Connected, AuthType: account.AuthType,
			Health: "Healthy", QuotaSnapshotAt: account.RateLimitsObservedAt, QuotaFreshness: "fresh",
			InFlightTurns: inFlight[account.ID], DispatchDeficit: scheduler.Deficits[account.ID], DispatchSequence: scheduler.Dispatches[account.ID],
			LastSelectedAt: scheduler.LastSelectedAt[account.ID], MaximumContribution: 100, CircuitState: "closed",
		}
		remaining, known := routableRemainingPercent(account, now)
		value.QuotaKnown = known
		if known {
			confirmed := clampRoutePercent(remaining)
			value.ConfirmedRemaining = &confirmed
			value.EffectiveCapacity = &confirmed
		}
		if account.NextRateLimitResetAt != nil {
			value.ResetAt = *account.NextRateLimitResetAt
		}
		if account.RateLimits != nil {
			long, short := longestAndShortestWindow(account.RateLimits)
			if short != nil {
				remaining := clampRoutePercent(100 - short.UsedPercent)
				value.ShortRemaining = &remaining
			}
			if long != nil {
				remaining := clampRoutePercent(100 - long.UsedPercent)
				value.LongRemaining = &remaining
			}
		}
		for _, reservation := range scheduler.Reservations {
			if reservation.AccountID == account.ID && reservation.ExpiresAt > now.UnixMilli() {
				value.ReservedTurnUnits += reservation.Weight
			}
		}
		health, hasHealth := m.store.AccountHealth(account.ID)
		if hasHealth {
			value.CooldownUntil = health.OpenUntil
			value.ConsecutiveFailures = health.ConsecutiveFailures
			category, message := sanitizeRoutingFailure(health.Reason)
			value.LastFailure = message
			value.LastFailureCategory = firstNonEmpty(health.LastFailureCategory, category)
			if health.State != "" {
				value.CircuitState = health.State
			}
		}
		switch {
		case !account.Enabled:
			value.Health, value.QuotaFreshness, value.ReasonCode = "Disconnected", "unknown", "skipped_disabled"
		case !account.Connected:
			value.Health, value.QuotaFreshness, value.ReasonCode = "Disconnected", "unknown", "skipped_disconnected"
		case account.AuthType != "chatgpt":
			value.Health, value.ReasonCode = "AuthExpired", "skipped_incompatible"
		case hasHealth && health.State == "open" && !m.accountCircuitAllows(account, now):
			value.Health, value.ReasonCode = "Cooldown", "skipped_cooldown"
		case hasHealth && health.State == "half-open" && !m.accountCircuitAllows(account, now):
			value.Health, value.ReasonCode = "Probation", "skipped_open_circuit"
		case !accountHasCapacity(account):
			value.Health, value.ReasonCode = "Depleted", "skipped_depleted"
		case account.RateLimitsObservedAt == 0:
			value.Health, value.QuotaFreshness, value.ReasonCode = "Probation", "unknown", "skipped_unknown_quota"
		case now.Sub(time.UnixMilli(account.RateLimitsObservedAt)) > 2*time.Minute:
			value.Health, value.QuotaFreshness, value.ReasonCode = "QuotaStale", "stale", "skipped_stale_quota"
		case minimumRemaining(value.ShortRemaining, value.LongRemaining) < 5:
			value.Health = "Draining"
			if hasSafeEligible {
				value.ReasonCode = "skipped_low_water_reserve"
			}
		}
		if value.ReasonCode == "" && !known && hasKnownEligible {
			value.ReasonCode = "skipped_unknown_quota"
		}
		if candidate, ok := previewByID[account.ID]; ok {
			value.Eligible = true
			value.ScoreComponents = candidate.ScoreComponents
			value.ReasonCode = candidate.ReasonCode
		}
		result = append(result, value)
	}
	return result
}

func minimumRemaining(values ...*float64) float64 {
	minimum, found := 100.0, false
	for _, value := range values {
		if value != nil && (!found || *value < minimum) {
			minimum, found = *value, true
		}
	}
	if !found {
		return 100
	}
	return minimum
}

func clampRoutePercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func compatibilityProfileOrUnknown(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return "unknown"
	}
	return profile
}

func (m *Multiplexer) policyFallbackReason() string {
	if m.store.RoutingPolicy() != state.RoutingPolicySticky && m.effectiveRoutingPolicy() == state.RoutingPolicySticky {
		return "policy_downgraded_unknown_profile"
	}
	return ""
}

func maskedRoutingIdentity(account AccountSnapshot) string {
	value := strings.TrimSpace(account.Email)
	if value == "" {
		value = strings.TrimSpace(account.Username)
	}
	if value == "" {
		return ""
	}
	if at := strings.LastIndex(value, "@"); at > 0 {
		prefix := []rune(value[:at])
		visible := "*"
		if len(prefix) > 0 {
			visible = string(prefix[0]) + "***"
		}
		return visible + value[at:]
	}
	runes := []rune(value)
	if len(runes) <= 2 {
		return strings.Repeat("*", len(runes))
	}
	return string(runes[0]) + strings.Repeat("*", len(runes)-2) + string(runes[len(runes)-1])
}

func (m *Multiplexer) previewNextCandidate(snapshots []AccountSnapshot) *RoutingCandidate {
	candidates := m.previewCandidates(snapshots)
	if len(candidates) == 0 {
		return nil
	}
	return &candidates[0]
}

func (m *Multiplexer) previewCandidates(snapshots []AccountSnapshot) []RoutingCandidate {
	now := time.Now()
	if m.now != nil {
		now = m.now()
	}
	scheduler := m.store.Scheduler()
	type candidate struct {
		snapshot       AccountSnapshot
		remaining      float64
		known          bool
		score          float64
		weight         float64
		dispatches     uint64
		lastSelectedAt int64
	}
	values := make([]candidate, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if !accountHasCapacity(snapshot) {
			continue
		}
		if !m.accountCircuitAllows(snapshot, now) {
			continue
		}
		remaining, known := routableRemainingPercent(snapshot, now)
		weight := remaining / 100
		if weight < 0.01 {
			weight = 0.01
		}
		reserved := 0.0
		for _, reservation := range scheduler.Reservations {
			if reservation.AccountID == snapshot.ID && reservation.ExpiresAt > now.UnixMilli() {
				reserved += reservation.Weight
			}
		}
		values = append(values, candidate{
			snapshot: snapshot, remaining: remaining, known: known,
			score:          scheduler.Deficits[snapshot.ID] + weight - reserved,
			weight:         weight,
			dispatches:     scheduler.Dispatches[snapshot.ID],
			lastSelectedAt: scheduler.LastSelectedAt[snapshot.ID],
		})
	}
	if len(values) == 0 {
		return []RoutingCandidate{}
	}
	hasKnown := false
	for _, value := range values {
		if value.known {
			hasKnown = true
			break
		}
	}
	filtered := values[:0]
	for _, value := range values {
		if !hasKnown || value.known {
			filtered = append(filtered, value)
		}
	}
	values = filtered
	hasSafe := false
	for _, value := range values {
		if value.remaining >= 5 {
			hasSafe = true
			break
		}
	}
	filtered = values[:0]
	for _, value := range values {
		if !hasSafe || value.remaining >= 5 {
			filtered = append(filtered, value)
		}
	}
	values = filtered
	sort.SliceStable(values, func(i, j int) bool {
		return fairShareCandidateRanksBefore(
			values[i].score,
			values[i].weight,
			values[i].dispatches,
			values[i].lastSelectedAt,
			values[i].snapshot.CreatedAt,
			values[i].snapshot.ID,
			values[j].score,
			values[j].weight,
			values[j].dispatches,
			values[j].lastSelectedAt,
			values[j].snapshot.CreatedAt,
			values[j].snapshot.ID,
		)
	})
	result := make([]RoutingCandidate, 0, len(values))
	for index, value := range values {
		reserved := 0.0
		for _, reservation := range scheduler.Reservations {
			if reservation.AccountID == value.snapshot.ID && reservation.ExpiresAt > now.UnixMilli() {
				reserved += reservation.Weight
			}
		}
		weight := value.remaining / 100
		if weight < 0.01 {
			weight = 0.01
		}
		reasonCode := "eligible_lower_score"
		if index == 0 {
			reasonCode = "selected_highest_score"
			if !value.known {
				reasonCode = "selected_probation_fallback"
			}
		}
		result = append(result, RoutingCandidate{
			AccountID: value.snapshot.ID, Label: value.snapshot.Label, PlanLabel: value.snapshot.PlanLabel,
			RemainingPercent: value.remaining, QuotaKnown: value.known, Score: value.score,
			ScoreComponents: RoutingScoreComponents{Deficit: scheduler.Deficits[value.snapshot.ID], QuotaWeight: weight, ReservationPenalty: reserved, FinalScore: value.score},
			ReasonCode:      reasonCode, IsPreview: true,
		})
	}
	return result
}

func (m *Multiplexer) routerCapabilities() []state.AccountCapability {
	known := make(map[string]state.AccountCapability)
	for _, capability := range m.store.AccountCapabilities() {
		known[capability.AccountID] = capability
	}
	result := make([]state.AccountCapability, 0, len(m.store.Accounts()))
	for _, account := range m.store.Accounts() {
		capability, ok := known[account.ID]
		if !ok {
			capability = m.baseCapability(account.ID)
		}
		result = append(result, capability)
	}
	return result
}

func (m *Multiplexer) ThreadRouteStatus(ctx context.Context, threadID string) (ThreadRouteStatus, error) {
	route, ok := m.store.ThreadRoute(threadID)
	if !ok {
		return ThreadRouteStatus{}, fmt.Errorf("thread %q has no Relay route", threadID)
	}
	if m.unifiedPoolEnabled() {
		publicRoute := route
		publicRoute.AccountID = state.DefaultPoolID
		publicRoute.PreviousAccountID = ""
		publicRoute.LastCompletedAccountID = ""
		publicRoute.LastQuotaAccountID = ""
		publicRoute.ActiveMigrationID = ""
		publicRoute.ActiveAttemptID = ""
		publicRoute.RolloutRelativePath = ""
		publicRoute.Policy = state.RoutingPolicySticky
		if task, found := m.store.TaskRecords()[threadID]; found {
			if task.CanonicalGeneration > 0 {
				publicRoute.Generation = task.CanonicalGeneration
				publicRoute.HistoryGeneration = task.CanonicalGeneration
			}
			publicRoute.RecoveryRequired = task.RecoveryState != ""
			if task.RecoveryState != "" {
				publicRoute.CurrentState = "recovery-required"
			}
		}
		relayIdentity := &RoutingWorkerIdentity{
			AccountID: state.DefaultPoolID,
			Label:     "Codex Relay Pool",
			PlanLabel: "Unified Pool",
		}
		return ThreadRouteStatus{
			ContractVersion:        2,
			ThreadID:               threadID,
			Route:                  publicRoute,
			Controller:             relayIdentity,
			CurrentOwner:           relayIdentity,
			RequestedPolicy:        state.RoutingPolicySticky,
			EffectivePolicy:        state.RoutingPolicySticky,
			NextCandidateIsPreview: false,
			RecoveryRequired:       publicRoute.RecoveryRequired,
			ActiveTurnState:        publicRoute.CurrentState,
			QuotaFreshness:         "pool",
			QuotaAttribution:       QuotaAttributionStatus{Status: "pool_routed"},
			Pool:                   m.unifiedPoolStatus(),
		}, nil
	}
	accounts := m.Accounts(ctx)
	workers := m.accountRouteStatuses(accounts)
	identities := routingIdentityMap(accounts)
	publicRoute := route
	publicRoute.RolloutRelativePath = ""
	result := ThreadRouteStatus{
		ContractVersion:        1,
		ThreadID:               threadID,
		Route:                  publicRoute,
		RequestedPolicy:        m.store.RoutingPolicy(),
		EffectivePolicy:        m.effectiveRoutingPolicy(),
		PolicyFallbackReason:   m.policyFallbackReason(),
		NextCandidateIsPreview: true,
		RecoveryRequired:       route.RecoveryRequired,
		ActiveTurnState:        route.CurrentState,
		Pool:                   m.poolStatus(accounts),
		Workers:                workers,
		QuotaAttribution:       QuotaAttributionStatus{Status: "unavailable"},
	}
	for _, account := range accounts {
		if account.Controller {
			result.Controller = identityPointer(identities, account.ID)
			break
		}
	}
	result.CurrentOwner = identityPointer(identities, route.AccountID)
	result.PreviousWorker = identityPointer(identities, route.PreviousAccountID)
	if route.LastCompletedAccountID != "" {
		result.LastCompletedWorker = identityPointer(identities, route.LastCompletedAccountID)
	}
	if route.LastQuotaAccountID != "" {
		result.LastQuotaConsumingWorker = identityPointer(identities, route.LastQuotaAccountID)
	}
	if route.ActiveAttemptID != "" {
		if attempt, found := m.store.TurnAttempt(route.ActiveAttemptID); found {
			if attempt.Failure != "" {
				attempt.FailureCategory, attempt.Failure = sanitizeRoutingFailure(attempt.Failure)
			}
			result.Attempt = &attempt
			result.ActiveWorker = identityPointer(identities, attempt.AccountID)
		}
	}
	m.turnMu.Lock()
	if active, found := m.activeTurns[threadID]; found && active.route.accountID != "" {
		result.ActiveWorker = identityPointer(identities, active.route.accountID)
	}
	m.turnMu.Unlock()
	if reservation, found := m.store.Scheduler().Reservations[route.ActiveAttemptID]; found {
		result.Reservation = &RoutingReservationStatus{
			AttemptID: reservation.AttemptID, AccountID: reservation.AccountID,
			Weight: reservation.Weight, ExpiresAt: reservation.ExpiresAt,
		}
	}
	if completed, found := latestCompletedAttempt(m.store.TurnAttempts(), threadID); found {
		if result.LastCompletedWorker == nil {
			result.LastCompletedWorker = identityPointer(identities, completed.AccountID)
		}
		result.QuotaAttribution = quotaAttributionFromAttempt(completed)
		if completed.QuotaAttribution == "confirmed" && result.LastQuotaConsumingWorker == nil {
			result.LastQuotaConsumingWorker = identityPointer(identities, completed.AccountID)
		}
	}
	if checkpoint, found := m.store.Checkpoint(threadID); found {
		checkpoint.RolloutPath = ""
		result.Checkpoint = &checkpoint
	}
	for _, handoff := range sanitizedHandoffs(m.store.Handoffs()) {
		if handoff.ThreadID == threadID {
			result.Handoffs = append(result.Handoffs, handoff)
			if result.LatestHandoff == nil || handoff.UpdatedAt > result.LatestHandoff.UpdatedAt {
				value := handoff
				result.LatestHandoff = &value
			}
		}
	}
	if decisions := m.RoutingDecisions(threadID, 0); len(decisions) > 0 {
		decision := decisions[len(decisions)-1]
		result.LastDecision = &decision
		for index := len(decisions) - 1; index >= 0; index-- {
			if decisions[index].EventType == "route_decision" || decisions[index].EventType == "worker_selected" {
				result.SchedulerDecisionID = decisions[index].ID
				break
			}
		}
	}
	result.NextCandidate = m.previewCandidateForRoute(accounts, route)
	result.Timeline = m.routingTimeline(threadID, 100)
	quotaAccountID := route.AccountID
	if result.ActiveWorker != nil {
		quotaAccountID = result.ActiveWorker.AccountID
	}
	for _, worker := range workers {
		if worker.AccountID != quotaAccountID {
			continue
		}
		result.QuotaSnapshotAt = worker.QuotaSnapshotAt
		result.QuotaFreshness = worker.QuotaFreshness
		break
	}
	if result.QuotaFreshness == "" {
		result.QuotaFreshness = "unknown"
	}
	return result, nil
}

func (m *Multiplexer) previewCandidateForRoute(accounts []AccountSnapshot, route state.ThreadRoute) *RoutingCandidate {
	candidates := m.previewCandidates(accounts)
	if len(candidates) == 0 {
		return nil
	}
	policy := m.effectiveRoutingPolicy()
	if policy == state.RoutingPolicySticky {
		for _, candidate := range candidates {
			if candidate.AccountID == route.AccountID {
				candidate.ReasonCode = "selected_sticky_owner"
				return &candidate
			}
		}
		return nil
	}
	if policy == state.RoutingPolicyRotate {
		for _, candidate := range candidates {
			if candidate.AccountID != route.AccountID {
				candidate.ReasonCode = "selected_rotation"
				return &candidate
			}
		}
		return nil
	}
	candidate := candidates[0]
	return &candidate
}

func routingIdentityMap(accounts []AccountSnapshot) map[string]RoutingWorkerIdentity {
	result := make(map[string]RoutingWorkerIdentity, len(accounts))
	for index, account := range accounts {
		result[account.ID] = RoutingWorkerIdentity{
			AccountID: account.ID, Label: account.Label, DisplayName: account.DisplayName,
			MaskedIdentity: maskedRoutingIdentity(account), PlanLabel: account.PlanLabel,
			SubscriptionIndex: index + 1,
		}
	}
	return result
}

func identityPointer(identities map[string]RoutingWorkerIdentity, accountID string) *RoutingWorkerIdentity {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil
	}
	identity, ok := identities[accountID]
	if !ok {
		identity = RoutingWorkerIdentity{AccountID: accountID, Label: accountID}
	}
	return &identity
}

func latestCompletedAttempt(attempts []state.TurnAttempt, threadID string) (state.TurnAttempt, bool) {
	var latest state.TurnAttempt
	found := false
	for _, attempt := range attempts {
		if attempt.ThreadID != threadID || attempt.CompletedAt == 0 {
			continue
		}
		if !found || attempt.CompletedAt > latest.CompletedAt {
			latest, found = attempt, true
		}
	}
	return latest, found
}

func quotaAttributionFromAttempt(attempt state.TurnAttempt) QuotaAttributionStatus {
	result := QuotaAttributionStatus{
		Status: attempt.QuotaAttribution, AccountID: attempt.AccountID,
		BeforeRemaining: attempt.QuotaBeforeRemaining, BeforeObservedAt: attempt.QuotaBeforeObservedAt,
		AfterRemaining: attempt.QuotaAfterRemaining, AfterObservedAt: attempt.QuotaAfterObservedAt,
	}
	if result.Status == "" {
		if attempt.QuotaBeforeRemaining != nil && attempt.QuotaAfterRemaining == nil {
			result.Status = "waiting_for_refreshed_quota"
		} else {
			result.Status = "unavailable"
		}
	}
	if attempt.QuotaBeforeRemaining != nil && attempt.QuotaAfterRemaining != nil {
		delta := *attempt.QuotaBeforeRemaining - *attempt.QuotaAfterRemaining
		result.DeltaPercent = &delta
	}
	return result
}

func (m *Multiplexer) routingTimeline(threadID string, limit int) []RoutingTimelineEvent {
	events := make([]RoutingTimelineEvent, 0)
	seen := make(map[string]struct{})
	appendEvent := func(event RoutingTimelineEvent) {
		if event.ID == "" || event.Timestamp == 0 {
			return
		}
		if _, exists := seen[event.ID]; exists {
			return
		}
		seen[event.ID] = struct{}{}
		events = append(events, event)
	}
	for _, decision := range m.RoutingDecisions(threadID, 0) {
		eventType := decision.EventType
		if eventType == "" {
			eventType = "route_decision"
		}
		reasonCode := decision.ReasonCode
		if reasonCode == "" {
			reasonCode = routingReasonCode(decision.Reason, decision.Policy, decision.FromAccountID, decision.ToAccountID)
		}
		appendEvent(RoutingTimelineEvent{
			ID: decision.ID, Type: eventType, Timestamp: decision.CreatedAt, Generation: decision.Generation,
			AccountID: decision.ToAccountID, SourceAccountID: decision.FromAccountID, TargetAccountID: decision.ToAccountID,
			ReasonCode: reasonCode, Reason: decision.Reason, Result: decision.Result, RemainingPercent: decision.RemainingPercent,
		})
	}
	for _, attempt := range m.store.TurnAttempts() {
		if attempt.ThreadID != threadID {
			continue
		}
		appendEvent(RoutingTimelineEvent{ID: attempt.ID + ":turn_reserved", Type: "turn_reserved", Timestamp: attempt.StartedAt, Generation: attempt.Generation, AccountID: attempt.AccountID, Result: "pending"})
		appendEvent(RoutingTimelineEvent{ID: attempt.ID + ":turn_accepted", Type: "turn_accepted", Timestamp: attempt.AcceptedAt, Generation: attempt.Generation, AccountID: attempt.AccountID, Result: "accepted"})
		appendEvent(RoutingTimelineEvent{ID: attempt.ID + ":visible_output_started", Type: "visible_output_started", Timestamp: attempt.FirstVisibleOutputAt, Generation: attempt.Generation, AccountID: attempt.AccountID, Result: "observed"})
		if attempt.SideEffectsStarted {
			appendEvent(RoutingTimelineEvent{ID: attempt.ID + ":side_effect_observed", Type: "side_effect_observed", Timestamp: attempt.UpdatedAt, Generation: attempt.Generation, AccountID: attempt.AccountID, Result: "observed"})
		}
		if attempt.CompletedAt != 0 {
			eventType, result := "turn_failed", "failed"
			switch strings.ToUpper(attempt.Phase) {
			case "COMPLETED":
				eventType, result = "turn_completed", "completed"
			case "RECOVERY_REQUIRED":
				eventType, result = "recovery_required", "blocked"
			}
			appendEvent(RoutingTimelineEvent{ID: attempt.ID + ":" + eventType, Type: eventType, Timestamp: attempt.CompletedAt, Generation: attempt.Generation, AccountID: attempt.AccountID, ReasonCode: attempt.FailureCategory, Reason: attempt.Failure, Result: result})
		}
	}
	for _, handoff := range m.store.Handoffs() {
		if handoff.ThreadID != threadID {
			continue
		}
		appendEvent(RoutingTimelineEvent{
			ID: handoff.ID + ":" + strings.ToLower(handoff.Phase), Type: "handoff_" + strings.ToLower(handoff.Phase),
			Timestamp: handoff.UpdatedAt, Generation: handoff.TargetGeneration,
			SourceAccountID: handoff.SourceAccountID, TargetAccountID: handoff.TargetAccountID,
			ReasonCode: firstNonEmpty(handoff.ReasonCode, handoffReasonCode(handoff.Phase)), Reason: firstNonEmpty(handoff.Failure, handoff.Reason), Result: strings.ToLower(handoff.Phase),
		})
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Timestamp != events[j].Timestamp {
			return events[i].Timestamp < events[j].Timestamp
		}
		return events[i].ID < events[j].ID
	})
	if limit <= 0 {
		limit = 100
	}
	if len(events) > limit {
		events = events[len(events)-limit:]
	}
	return events
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func handoffReasonCode(phase string) string {
	switch strings.ToUpper(strings.TrimSpace(phase)) {
	case "FAILED", "ROLLED_BACK":
		return "handoff_failed"
	case "COMMITTED":
		return "handoff_committed"
	default:
		return "handoff_in_progress"
	}
}

func routingReasonCode(reason string, policy state.RoutingPolicy, fromAccountID, toAccountID string) string {
	value := strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(value, "first turn"):
		return "selected_sticky_owner"
	case strings.Contains(value, "quota rejected") || strings.Contains(value, "depleted"):
		return "handoff_quota_exhausted"
	case strings.Contains(value, "recovery"):
		return "recovery_reviewed"
	case strings.Contains(value, "policy changed"):
		return "policy_changed"
	case policy == state.RoutingPolicyRotate && fromAccountID != "" && fromAccountID != toAccountID:
		return "handoff_rotate_boundary"
	case policy == state.RoutingPolicyBalanced && fromAccountID != "" && fromAccountID != toAccountID:
		return "handoff_balanced_boundary"
	case policy == state.RoutingPolicySticky:
		return "selected_sticky_owner"
	default:
		return "selected_highest_score"
	}
}

func (m *Multiplexer) AcknowledgeThreadRecovery(threadID string) error {
	route, ok := m.store.ThreadRoute(threadID)
	if !ok {
		return fmt.Errorf("thread %q has no Relay route", threadID)
	}
	if route.ActiveAttemptID != "" || route.ActiveMigrationID != "" {
		return errors.New("the task still has active routing work")
	}
	route.RecoveryRequired = false
	if err := m.store.PutThreadRoute(route); err != nil {
		return err
	}
	m.recordRoutingDecision(threadID, route.AccountID, route.AccountID, "recovery review acknowledged")
	m.publish(Event{Type: "recovery-cleared", ThreadID: threadID, AccountID: route.AccountID, RouteGeneration: route.Generation})
	return nil
}

func (m *Multiplexer) RoutingDecisions(threadID string, limit int) []state.RoutingDecision {
	all := m.store.RoutingDecisions(0)
	filtered := make([]state.RoutingDecision, 0, len(all))
	for _, decision := range all {
		if threadID == "" || decision.ThreadID == threadID {
			if decision.ReasonCode == "" {
				decision.ReasonCode = routingReasonCode(decision.Reason, decision.Policy, decision.FromAccountID, decision.ToAccountID)
			}
			decision.Reason = safeRoutingReason(decision.ReasonCode, decision.EventType, decision.Reason)
			filtered = append(filtered, decision)
		}
	}
	if limit <= 0 || limit > len(filtered) {
		limit = len(filtered)
	}
	return filtered[len(filtered)-limit:]
}

func safeRoutingReason(reasonCode, eventType, _ string) string {
	switch reasonCode {
	case "selected_highest_score":
		return "the scheduler selected the highest eligible score"
	case "selected_rotation":
		return "the scheduler selected the next eligible worker for rotation"
	case "selected_sticky_owner":
		return "the task stayed with its Sticky owner"
	case "selected_probation_fallback":
		return "the scheduler selected a probation worker because no confirmed quota was available"
	case "handoff_quota_exhausted":
		return "the previous worker had no confirmed quota before side effects"
	case "handoff_balanced_boundary":
		return "the task moved at a safe completed-turn balancing boundary"
	case "handoff_rotate_boundary":
		return "the task moved at a safe completed-turn rotate boundary"
	case "policy_downgraded_unknown_profile":
		return "safe cross-account handoff is unavailable for this compatibility profile"
	case "policy_changed":
		return "routing policy changed"
	case "quota_snapshot_confirmed":
		return "a newer quota snapshot showed a measurable decrease"
	case "quota_snapshot_no_measurable_change":
		return "a refreshed quota snapshot showed no measurable decrease"
	case "quota_snapshot_crossed_reset":
		return "the refreshed quota snapshot crossed a reset boundary"
	case "skipped_disabled", "skipped_disconnected", "skipped_incompatible", "skipped_open_circuit", "skipped_cooldown", "skipped_depleted", "skipped_unknown_quota", "skipped_stale_quota", "skipped_low_water_reserve":
		return strings.ReplaceAll(reasonCode, "_", " ")
	case "turn_reserved":
		return "capacity reserved for the logical turn"
	case "turn_completed":
		return "the logical turn completed"
	case "dispatch_not_committed":
		return "the uncommitted scheduler reservation was rolled back"
	case "circuit_recovered":
		return "a fresh successful turn closed the quota circuit"
	case "recovery_reviewed":
		return "recovery review acknowledged"
	}
	switch eventType {
	case "handoff_prepared", "handoff_copied", "handoff_resumed", "handoff_committed":
		return "the handoff advanced through a verified journal phase"
	case "worker_selected":
		return "a worker was selected for the logical turn"
	case "turn_accepted":
		return "the selected worker accepted the logical turn"
	case "visible_output_started":
		return "visible assistant output started"
	case "side_effect_observed":
		return "a side effect boundary was observed"
	}
	return "a sanitized routing event was recorded"
}

func (m *Multiplexer) SetRoutingPolicy(policy state.RoutingPolicy) error {
	if !policy.Valid() {
		return errors.New("policy must be sticky, balanced, or rotate-completed-turn")
	}
	if err := m.store.SetRoutingPolicy(policy); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	m.recordTimelineEvent("", stateRoutingEvent{ID: fmt.Sprintf("policy-%d", now), EventType: "policy_changed", ReasonCode: "policy_changed", Reason: "routing policy changed", Result: string(policy), CreatedAt: now, Policy: policy})
	if fallback := m.policyFallbackReason(); fallback != "" {
		m.recordTimelineEvent("", stateRoutingEvent{ID: fmt.Sprintf("policy-fallback-%d", now), EventType: "policy_downgraded", ReasonCode: fallback, Reason: "safe cross-account handoff is unavailable for this compatibility profile", Result: string(m.effectiveRoutingPolicy()), CreatedAt: now, Policy: policy})
	}
	m.publish(Event{Type: "routing-policy-changed", Message: string(policy), Data: map[string]any{"policy": policy}})
	return nil
}

func (m *Multiplexer) recordRoutingDecision(threadID, fromAccountID, toAccountID, reason string) {
	attemptID := ""
	generation := uint64(0)
	if route, ok := m.store.ThreadRoute(threadID); ok {
		attemptID = route.ActiveAttemptID
		generation = route.Generation
	}
	m.recordTimelineEvent(threadID, stateRoutingEvent{
		ID:        fmt.Sprintf("decision-%d-%d", time.Now().UnixMilli(), m.serverSequence.Add(1)),
		EventType: "route_decision", AttemptID: attemptID,
		FromAccountID: fromAccountID, ToAccountID: toAccountID, Generation: generation,
		ReasonCode: routingReasonCode(reason, m.effectiveRoutingPolicy(), fromAccountID, toAccountID),
		Reason:     reason, Result: "selected", CreatedAt: time.Now().UnixMilli(),
	})
}
