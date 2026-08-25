package state

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// RoutingPolicy controls when a thread may move between subscriptions. A move
// is always evaluated at a completed-turn boundary; the policy never permits
// two workers to write the same thread concurrently.
type RoutingPolicy string

const (
	RoutingPolicySticky   RoutingPolicy = "sticky"
	RoutingPolicyBalanced RoutingPolicy = "balanced"
	RoutingPolicyRotate   RoutingPolicy = "rotate-completed-turn"
)

func (p RoutingPolicy) Valid() bool {
	switch p {
	case RoutingPolicySticky, RoutingPolicyBalanced, RoutingPolicyRotate:
		return true
	default:
		return false
	}
}

type Reservation struct {
	ID                 string             `json:"id"`
	AccountID          string             `json:"accountId"`
	ThreadID           string             `json:"threadId,omitempty"`
	AttemptID          string             `json:"attemptId,omitempty"`
	Weight             float64            `json:"weight"`
	ExpiresAt          int64              `json:"expiresAt"`
	DispatchCharged    bool               `json:"dispatchCharged,omitempty"`
	DispatchDelta      map[string]float64 `json:"dispatchDelta,omitempty"`
	DispatchCounted    bool               `json:"dispatchCounted,omitempty"`
	SelectedAt         int64              `json:"selectedAt,omitempty"`
	PreviousSelectedAt int64              `json:"previousSelectedAt,omitempty"`
}

type SchedulerState struct {
	Policy         RoutingPolicy          `json:"policy"`
	Cursor         uint64                 `json:"cursor"`
	Deficits       map[string]float64     `json:"deficits"`
	Dispatches     map[string]uint64      `json:"dispatches"`
	LastSelectedAt map[string]int64       `json:"lastSelectedAt"`
	Reservations   map[string]Reservation `json:"reservations"`
}

type AccountHealth struct {
	AccountID           string `json:"accountId"`
	State               string `json:"state"`
	ConsecutiveFailures int    `json:"consecutiveFailures"`
	OpenUntil           int64  `json:"openUntil,omitempty"`
	LastFailureAt       int64  `json:"lastFailureAt,omitempty"`
	LastSuccessAt       int64  `json:"lastSuccessAt,omitempty"`
	// BlockedResetAt records the quota-window generation that was active when
	// an upstream turn was rejected. A later snapshot with a newer reset epoch
	// is strong recovery evidence; merely polling the same cached percentage is
	// not. The value uses Unix seconds, matching Codex's rate-limit protocol.
	BlockedResetAt      int64  `json:"blockedResetAt,omitempty"`
	LastQuotaObservedAt int64  `json:"lastQuotaObservedAt,omitempty"`
	LastQuotaResetAt    int64  `json:"lastQuotaResetAt,omitempty"`
	RecoverySource      string `json:"recoverySource,omitempty"`
	Reason              string `json:"reason,omitempty"`
	LastFailureCategory string `json:"lastFailureCategory,omitempty"`
}

type AccountCapability struct {
	AccountID            string `json:"accountId"`
	Profile              string `json:"profile"`
	Known                bool   `json:"known"`
	ThreadRead           bool   `json:"threadRead"`
	ResumeByID           bool   `json:"resumeById"`
	IncompleteTurnResume bool   `json:"incompleteTurnResume"`
	ObservedAt           int64  `json:"observedAt"`
}

type ThreadRoute struct {
	ThreadID               string        `json:"threadId"`
	AccountID              string        `json:"accountId"`
	PreviousAccountID      string        `json:"previousOwnerAccountId,omitempty"`
	Generation             uint64        `json:"generation"`
	HistoryGeneration      uint64        `json:"authoritativeHistoryGeneration"`
	RolloutRelativePath    string        `json:"rolloutRelativePath,omitempty"`
	HistorySHA256          string        `json:"rolloutHash,omitempty"`
	HistorySize            int64         `json:"rolloutSize,omitempty"`
	LastCompletedTurnID    string        `json:"lastCompletedTurnId,omitempty"`
	LastCompletedTurnAt    int64         `json:"lastCompletedTurnAt,omitempty"`
	LastCompletedAccountID string        `json:"lastCompletedAccountId,omitempty"`
	LastQuotaAccountID     string        `json:"lastQuotaConsumingAccountId,omitempty"`
	FirstTurnPending       bool          `json:"firstTurnPending,omitempty"`
	ConsecutiveOwnerTurns  int           `json:"consecutiveTurnsOnOwner,omitempty"`
	Policy                 RoutingPolicy `json:"routingPolicy,omitempty"`
	CurrentState           string        `json:"currentState,omitempty"`
	ActiveMigrationID      string        `json:"activeMigrationId,omitempty"`
	ActiveAttemptID        string        `json:"activeAttemptId,omitempty"`
	RecoveryRequired       bool          `json:"recoveryRequired,omitempty"`
	UpdatedAt              int64         `json:"updatedAt"`
}

type TurnAttempt struct {
	ID                    string   `json:"id"`
	LogicalTurnID         string   `json:"logicalTurnId"`
	ThreadID              string   `json:"threadId"`
	RequestHash           string   `json:"requestHash"`
	AccountID             string   `json:"accountId"`
	Generation            uint64   `json:"generation"`
	RouteGeneration       uint64   `json:"routeGeneration"`
	AttemptNumber         int      `json:"attemptNumber"`
	Phase                 string   `json:"phase"`
	AcceptedAt            int64    `json:"acceptedAt,omitempty"`
	FirstVisibleOutputAt  int64    `json:"firstVisibleOutputAt,omitempty"`
	SideEffectsStarted    bool     `json:"sideEffectObserved"`
	SideEffectTypes       []string `json:"sideEffectTypes,omitempty"`
	StartedAt             int64    `json:"startedAt"`
	CompletedAt           int64    `json:"completedAt,omitempty"`
	UpdatedAt             int64    `json:"updatedAt"`
	FailureCategory       string   `json:"failureCategory,omitempty"`
	Failure               string   `json:"failureMessageSanitized,omitempty"`
	QuotaBeforeRemaining  *float64 `json:"quotaBeforeRemainingPercent,omitempty"`
	QuotaBeforeObservedAt int64    `json:"quotaBeforeObservedAt,omitempty"`
	QuotaAfterRemaining   *float64 `json:"quotaAfterRemainingPercent,omitempty"`
	QuotaAfterObservedAt  int64    `json:"quotaAfterObservedAt,omitempty"`
	QuotaAttribution      string   `json:"quotaAttribution,omitempty"`
}

type Handoff struct {
	ID               string `json:"id"`
	ThreadID         string `json:"threadId"`
	SourceAccountID  string `json:"sourceAccountId"`
	TargetAccountID  string `json:"targetAccountId"`
	SourceGeneration uint64 `json:"sourceGeneration"`
	TargetGeneration uint64 `json:"targetGeneration"`
	Phase            string `json:"phase"`
	HistorySHA256    string `json:"historySha256,omitempty"`
	HistorySize      int64  `json:"historySize,omitempty"`
	StartedAt        int64  `json:"startedAt"`
	UpdatedAt        int64  `json:"updatedAt"`
	ReasonCode       string `json:"reasonCode,omitempty"`
	Reason           string `json:"reason,omitempty"`
	Failure          string `json:"failure,omitempty"`
}

type CanonicalCheckpoint struct {
	ThreadID      string `json:"threadId"`
	Generation    uint64 `json:"generation"`
	HistorySHA256 string `json:"historySha256"`
	HistorySize   int64  `json:"historySize"`
	RolloutPath   string `json:"rolloutPath"`
	UpdatedAt     int64  `json:"updatedAt"`
}

type RoutingDecision struct {
	ID               string        `json:"id"`
	ThreadID         string        `json:"threadId,omitempty"`
	AttemptID        string        `json:"attemptId,omitempty"`
	FromAccountID    string        `json:"fromAccountId,omitempty"`
	ToAccountID      string        `json:"toAccountId,omitempty"`
	Policy           RoutingPolicy `json:"policy"`
	EventType        string        `json:"eventType,omitempty"`
	ReasonCode       string        `json:"reasonCode,omitempty"`
	Reason           string        `json:"reason"`
	Result           string        `json:"result,omitempty"`
	Generation       uint64        `json:"generation,omitempty"`
	QuotaSnapshotAt  int64         `json:"quotaSnapshotAt,omitempty"`
	RemainingPercent *float64      `json:"remainingPercent,omitempty"`
	Score            *float64      `json:"score,omitempty"`
	CreatedAt        int64         `json:"createdAt"`
}

func defaultSchedulerState() SchedulerState {
	return SchedulerState{
		Policy:         RoutingPolicyBalanced,
		Deficits:       make(map[string]float64),
		Dispatches:     make(map[string]uint64),
		LastSelectedAt: make(map[string]int64),
		Reservations:   make(map[string]Reservation),
	}
}

func normalizeScheduler(value SchedulerState) SchedulerState {
	if !value.Policy.Valid() {
		value.Policy = RoutingPolicyBalanced
	}
	if value.Deficits == nil {
		value.Deficits = make(map[string]float64)
	}
	if value.Dispatches == nil {
		value.Dispatches = make(map[string]uint64)
	}
	if value.LastSelectedAt == nil {
		value.LastSelectedAt = make(map[string]int64)
	}
	if value.Reservations == nil {
		value.Reservations = make(map[string]Reservation)
	}
	return value
}

func (s *Store) RoutingPolicy() RoutingPolicy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.scheduler.Policy
}

func (s *Store) SetRoutingPolicy(policy RoutingPolicy) error {
	if !policy.Valid() {
		return fmt.Errorf("unsupported routing policy %q", policy)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.scheduler.Policy
	s.scheduler.Policy = policy
	if err := s.saveLocked(); err != nil {
		s.scheduler.Policy = previous
		return err
	}
	return nil
}

func (s *Store) Scheduler() SchedulerState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := s.scheduler
	result.Deficits = cloneFloatMap(s.scheduler.Deficits)
	result.Dispatches = cloneUint64Map(s.scheduler.Dispatches)
	result.LastSelectedAt = cloneInt64Map(s.scheduler.LastSelectedAt)
	result.Reservations = cloneReservationMap(s.scheduler.Reservations)
	return result
}

func (s *Store) AccountHealth(accountID string) (AccountHealth, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.health[accountID]
	return value, ok
}

func (s *Store) PutAccountHealth(health AccountHealth) error {
	health.AccountID = strings.TrimSpace(health.AccountID)
	if health.AccountID == "" {
		return errors.New("health account ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.health[health.AccountID]
	s.health[health.AccountID] = health
	if err := s.saveLocked(); err != nil {
		if existed {
			s.health[health.AccountID] = previous
		} else {
			delete(s.health, health.AccountID)
		}
		return err
	}
	return nil
}

func (s *Store) AccountHealthAll() []AccountHealth {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]AccountHealth, 0, len(s.health))
	for _, value := range s.health {
		result = append(result, value)
	}
	slices.SortFunc(result, func(a, b AccountHealth) int { return strings.Compare(a.AccountID, b.AccountID) })
	return result
}

func (s *Store) AccountCapability(accountID string) (AccountCapability, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.capabilities[accountID]
	return value, ok
}
func (s *Store) PutAccountCapability(capability AccountCapability) error {
	capability.AccountID = strings.TrimSpace(capability.AccountID)
	if capability.AccountID == "" {
		return errors.New("capability account ID is required")
	}
	if capability.ObservedAt == 0 {
		capability.ObservedAt = time.Now().UnixMilli()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.capabilities[capability.AccountID]
	s.capabilities[capability.AccountID] = capability
	if err := s.saveLocked(); err != nil {
		if existed {
			s.capabilities[capability.AccountID] = previous
		} else {
			delete(s.capabilities, capability.AccountID)
		}
		return err
	}
	return nil
}
func (s *Store) AccountCapabilities() []AccountCapability {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]AccountCapability, 0, len(s.capabilities))
	for _, value := range s.capabilities {
		result = append(result, value)
	}
	slices.SortFunc(result, func(a, b AccountCapability) int { return strings.Compare(a.AccountID, b.AccountID) })
	return result
}

func (s *Store) UpdateScheduler(update func(*SchedulerState) error) error {
	if update == nil {
		return errors.New("scheduler update is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.scheduler
	previous.Deficits = cloneFloatMap(s.scheduler.Deficits)
	previous.Dispatches = cloneUint64Map(s.scheduler.Dispatches)
	previous.LastSelectedAt = cloneInt64Map(s.scheduler.LastSelectedAt)
	previous.Reservations = cloneReservationMap(s.scheduler.Reservations)
	next := previous
	next.Deficits = cloneFloatMap(previous.Deficits)
	next.Dispatches = cloneUint64Map(previous.Dispatches)
	next.LastSelectedAt = cloneInt64Map(previous.LastSelectedAt)
	next.Reservations = cloneReservationMap(previous.Reservations)
	if err := update(&next); err != nil {
		return err
	}
	s.scheduler = normalizeScheduler(next)
	if err := s.saveLocked(); err != nil {
		s.scheduler = previous
		return err
	}
	return nil
}

func (s *Store) PutReservation(reservation Reservation) error {
	if strings.TrimSpace(reservation.ID) == "" || strings.TrimSpace(reservation.AccountID) == "" {
		return errors.New("reservation and account IDs are required")
	}
	if reservation.Weight <= 0 {
		reservation.Weight = 1
	}
	if reservation.ExpiresAt == 0 {
		reservation.ExpiresAt = time.Now().Add(2 * time.Minute).UnixMilli()
	}
	return s.UpdateScheduler(func(scheduler *SchedulerState) error {
		if previous, ok := scheduler.Reservations[reservation.ID]; ok && previous.DispatchCharged && !reservation.DispatchCharged {
			reservation.DispatchCharged = true
			reservation.DispatchDelta = cloneFloatMap(previous.DispatchDelta)
			reservation.DispatchCounted = previous.DispatchCounted
			reservation.SelectedAt = previous.SelectedAt
			reservation.PreviousSelectedAt = previous.PreviousSelectedAt
		}
		scheduler.Reservations[reservation.ID] = reservation
		return nil
	})
}

// RollbackReservation releases a failed dispatch and reverses only the exact
// weighted-deficit delta recorded when it was selected. The inverse deltas
// commute with later concurrent selections, so one failed child send cannot
// erase another request's scheduler progress.
func (s *Store) RollbackReservation(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("reservation ID is required")
	}
	return s.UpdateScheduler(func(scheduler *SchedulerState) error {
		reservation, ok := scheduler.Reservations[id]
		if !ok {
			return nil
		}
		if reservation.DispatchCharged {
			for accountID, delta := range reservation.DispatchDelta {
				scheduler.Deficits[accountID] -= delta
				if math.Abs(scheduler.Deficits[accountID]) < 0.000000001 {
					delete(scheduler.Deficits, accountID)
				}
			}
			if scheduler.Cursor > 0 {
				scheduler.Cursor--
			}
		}
		rollbackDispatchMetadata(scheduler, reservation)
		delete(scheduler.Reservations, id)
		return nil
	})
}

func (s *Store) ReleaseReservation(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("reservation ID is required")
	}
	return s.UpdateScheduler(func(scheduler *SchedulerState) error {
		delete(scheduler.Reservations, id)
		return nil
	})
}

func (s *Store) ThreadRoute(threadID string) (ThreadRoute, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	route, ok := s.routes[threadID]
	return route, ok
}

func (s *Store) PutThreadRoute(route ThreadRoute) error {
	route.ThreadID = strings.TrimSpace(route.ThreadID)
	route.AccountID = strings.TrimSpace(route.AccountID)
	if route.ThreadID == "" || route.AccountID == "" {
		return errors.New("thread and account IDs are required")
	}
	if route.Generation == 0 {
		route.Generation = 1
	}
	if route.HistoryGeneration == 0 {
		route.HistoryGeneration = route.Generation
	}
	if !route.Policy.Valid() {
		route.Policy = s.RoutingPolicy()
	}
	if strings.TrimSpace(route.CurrentState) == "" {
		route.CurrentState = "idle"
	}
	if route.UpdatedAt == 0 {
		route.UpdatedAt = time.Now().UnixMilli()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.routes[route.ThreadID]
	s.routes[route.ThreadID] = route
	if err := s.saveLocked(); err != nil {
		if existed {
			s.routes[route.ThreadID] = previous
		} else {
			delete(s.routes, route.ThreadID)
		}
		return err
	}
	return nil
}

func (s *Store) ThreadRoutes() []ThreadRoute {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]ThreadRoute, 0, len(s.routes))
	for _, route := range s.routes {
		result = append(result, route)
	}
	slices.SortFunc(result, func(a, b ThreadRoute) int { return strings.Compare(a.ThreadID, b.ThreadID) })
	return result
}

func (s *Store) PutTurnAttempt(attempt TurnAttempt) error {
	if strings.TrimSpace(attempt.ID) == "" || strings.TrimSpace(attempt.ThreadID) == "" || strings.TrimSpace(attempt.AccountID) == "" {
		return errors.New("attempt, thread, and account IDs are required")
	}
	if attempt.UpdatedAt == 0 {
		attempt.UpdatedAt = time.Now().UnixMilli()
	}
	if attempt.LogicalTurnID == "" {
		attempt.LogicalTurnID = attempt.ID
	}
	if attempt.RouteGeneration == 0 {
		attempt.RouteGeneration = attempt.Generation
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	attempt.SideEffectTypes = slices.Clone(attempt.SideEffectTypes)
	previous, existed := s.attempts[attempt.ID]
	s.attempts[attempt.ID] = attempt
	if err := s.saveLocked(); err != nil {
		if existed {
			s.attempts[attempt.ID] = previous
		} else {
			delete(s.attempts, attempt.ID)
		}
		return err
	}
	return nil
}

func (s *Store) TurnAttempt(id string) (TurnAttempt, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.attempts[id]
	value.SideEffectTypes = slices.Clone(value.SideEffectTypes)
	return value, ok
}

func (s *Store) TurnAttempts() []TurnAttempt {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]TurnAttempt, 0, len(s.attempts))
	for _, value := range s.attempts {
		value.SideEffectTypes = slices.Clone(value.SideEffectTypes)
		result = append(result, value)
	}
	slices.SortFunc(result, func(a, b TurnAttempt) int { return strings.Compare(a.ID, b.ID) })
	return result
}

func (s *Store) PutHandoff(handoff Handoff) error {
	if strings.TrimSpace(handoff.ID) == "" || strings.TrimSpace(handoff.ThreadID) == "" {
		return errors.New("handoff and thread IDs are required")
	}
	if handoff.UpdatedAt == 0 {
		handoff.UpdatedAt = time.Now().UnixMilli()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.handoffs[handoff.ID]
	s.handoffs[handoff.ID] = handoff
	if err := s.saveLocked(); err != nil {
		if existed {
			s.handoffs[handoff.ID] = previous
		} else {
			delete(s.handoffs, handoff.ID)
		}
		return err
	}
	return nil
}

func (s *Store) Handoffs() []Handoff {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Handoff, 0, len(s.handoffs))
	for _, value := range s.handoffs {
		result = append(result, value)
	}
	slices.SortFunc(result, func(a, b Handoff) int { return strings.Compare(a.ID, b.ID) })
	return result
}

// TransitionHandoff persists the migration phase and its authoritative route
// in one state-file replacement. This is the commit record used for startup
// recovery; there is no observable state where ownership changed but the
// handoff journal still claims the preceding phase.
func (s *Store) TransitionHandoff(handoff Handoff, route ThreadRoute) error {
	if strings.TrimSpace(handoff.ID) == "" || strings.TrimSpace(handoff.ThreadID) == "" {
		return errors.New("handoff and thread IDs are required")
	}
	if route.ThreadID != handoff.ThreadID || strings.TrimSpace(route.AccountID) == "" {
		return errors.New("handoff route does not match its thread")
	}
	now := time.Now().UnixMilli()
	if handoff.UpdatedAt == 0 {
		handoff.UpdatedAt = now
	}
	if route.UpdatedAt == 0 {
		route.UpdatedAt = now
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previousHandoff, handoffExisted := s.handoffs[handoff.ID]
	previousRoute, routeExisted := s.routes[route.ThreadID]
	previousOwner, ownerExisted := s.owners[route.ThreadID]
	s.handoffs[handoff.ID] = handoff
	s.routes[route.ThreadID] = route
	s.owners[route.ThreadID] = route.AccountID
	if err := s.saveLocked(); err != nil {
		if handoffExisted {
			s.handoffs[handoff.ID] = previousHandoff
		} else {
			delete(s.handoffs, handoff.ID)
		}
		if routeExisted {
			s.routes[route.ThreadID] = previousRoute
		} else {
			delete(s.routes, route.ThreadID)
		}
		if ownerExisted {
			s.owners[route.ThreadID] = previousOwner
		} else {
			delete(s.owners, route.ThreadID)
		}
		return err
	}
	return nil
}

func (s *Store) PutCheckpoint(checkpoint CanonicalCheckpoint) error {
	if strings.TrimSpace(checkpoint.ThreadID) == "" || strings.TrimSpace(checkpoint.RolloutPath) == "" {
		return errors.New("checkpoint thread and rollout path are required")
	}
	if checkpoint.UpdatedAt == 0 {
		checkpoint.UpdatedAt = time.Now().UnixMilli()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.checkpoints[checkpoint.ThreadID]
	s.checkpoints[checkpoint.ThreadID] = checkpoint
	if err := s.saveLocked(); err != nil {
		if existed {
			s.checkpoints[checkpoint.ThreadID] = previous
		} else {
			delete(s.checkpoints, checkpoint.ThreadID)
		}
		return err
	}
	return nil
}

func (s *Store) Checkpoint(threadID string) (CanonicalCheckpoint, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.checkpoints[threadID]
	return value, ok
}

func (s *Store) RecordDecision(decision RoutingDecision) error {
	_, err := s.RecordDecisionIfNew(decision)
	return err
}

// RecordDecisionIfNew atomically deduplicates deterministic timeline IDs. The
// boolean is false when a replayed notification was already persisted.
func (s *Store) RecordDecisionIfNew(decision RoutingDecision) (bool, error) {
	if strings.TrimSpace(decision.ID) == "" {
		return false, errors.New("decision ID is required")
	}
	if !decision.Policy.Valid() {
		decision.Policy = s.RoutingPolicy()
	}
	if decision.CreatedAt == 0 {
		decision.CreatedAt = time.Now().UnixMilli()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Timeline producers use deterministic IDs (attempt + phase, or handoff +
	// phase). Replayed notifications and renderer reconnects must not grow the
	// persistent ledger or show the same logical event twice.
	for _, existing := range s.decisions {
		if existing.ID == decision.ID {
			return false, nil
		}
	}
	previous := slices.Clone(s.decisions)
	s.decisions = append(s.decisions, decision)
	if len(s.decisions) > 1000 {
		s.decisions = slices.Clone(s.decisions[len(s.decisions)-1000:])
	}
	if err := s.saveLocked(); err != nil {
		s.decisions = previous
		return false, err
	}
	return true, nil
}

func (s *Store) RoutingDecisions(limit int) []RoutingDecision {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.decisions) {
		limit = len(s.decisions)
	}
	return slices.Clone(s.decisions[len(s.decisions)-limit:])
}

func cloneFloatMap(source map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
func cloneUint64Map(source map[string]uint64) map[string]uint64 {
	result := make(map[string]uint64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
func cloneInt64Map(source map[string]int64) map[string]int64 {
	result := make(map[string]int64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
func rollbackDispatchMetadata(scheduler *SchedulerState, reservation Reservation) {
	if !reservation.DispatchCounted {
		return
	}
	if scheduler.Dispatches[reservation.AccountID] <= 1 {
		delete(scheduler.Dispatches, reservation.AccountID)
	} else {
		scheduler.Dispatches[reservation.AccountID]--
	}
	if scheduler.LastSelectedAt[reservation.AccountID] == reservation.SelectedAt {
		if reservation.PreviousSelectedAt == 0 {
			delete(scheduler.LastSelectedAt, reservation.AccountID)
		} else {
			scheduler.LastSelectedAt[reservation.AccountID] = reservation.PreviousSelectedAt
		}
	}
}
func cloneReservationMap(source map[string]Reservation) map[string]Reservation {
	result := make(map[string]Reservation, len(source))
	for key, value := range source {
		value.DispatchDelta = cloneFloatMap(value.DispatchDelta)
		result[key] = value
	}
	return result
}
