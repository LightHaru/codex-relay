package state

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	PoolSchemaVersion = 3
	DefaultPoolID     = "relay"
)

type SourceMembershipState string

const (
	SourceProvisioning  SourceMembershipState = "PROVISIONING"
	SourceLoginPending  SourceMembershipState = "LOGIN_PENDING"
	SourceAuthenticated SourceMembershipState = "AUTHENTICATED"
	SourceProbing       SourceMembershipState = "PROBING"
	SourceAvailable     SourceMembershipState = "AVAILABLE"
	SourceActive        SourceMembershipState = "ACTIVE_SOURCE"
	SourceDepleted      SourceMembershipState = "DEPLETED"
	SourceProbation     SourceMembershipState = "PROBATION"
	SourceDraining      SourceMembershipState = "DRAINING"
	SourceRemoved       SourceMembershipState = "REMOVED"
)

type PoolLeaseState string

const (
	PoolLeasePrepared         PoolLeaseState = "PREPARED"
	PoolLeaseBound            PoolLeaseState = "BOUND"
	PoolLeaseDispatched       PoolLeaseState = "DISPATCHED"
	PoolLeaseAccepted         PoolLeaseState = "ACCEPTED"
	PoolLeaseStreaming        PoolLeaseState = "STREAMING"
	PoolLeaseCompleted        PoolLeaseState = "COMPLETED"
	PoolLeaseRolledBack       PoolLeaseState = "ROLLED_BACK"
	PoolLeaseRecoveryRequired PoolLeaseState = "RECOVERY_REQUIRED"
)

type QuotaEvidence struct {
	Allowed      *bool    `json:"allowed,omitempty"`
	LimitReached bool     `json:"limitReached,omitempty"`
	ShortUsed    *float64 `json:"shortWindowUsedPercent,omitempty"`
	LongUsed     *float64 `json:"longWindowUsedPercent,omitempty"`
	ResetEpoch   int64    `json:"resetEpoch,omitempty"`
	ObservedAt   int64    `json:"observedAt,omitempty"`
	Source       string   `json:"source,omitempty"`
	CircuitState string   `json:"circuitState,omitempty"`
	AuthState    string   `json:"authenticationState,omitempty"`
}

func (e QuotaEvidence) ExplicitlyDepleted() bool {
	if e.LimitReached || (e.Allowed != nil && !*e.Allowed) {
		return true
	}
	return (e.ShortUsed != nil && *e.ShortUsed >= 100) ||
		(e.LongUsed != nil && *e.LongUsed >= 100)
}

func (e QuotaEvidence) ConfirmedAvailable(now time.Time, staleAfter time.Duration) bool {
	if e.ExplicitlyDepleted() || e.Allowed == nil || !*e.Allowed || e.ObservedAt <= 0 {
		return false
	}
	if staleAfter > 0 && now.Sub(time.UnixMilli(e.ObservedAt)) > staleAfter {
		return false
	}
	return true
}

type CredentialSourceState struct {
	SourceID        string                `json:"sourceId"`
	Enabled         bool                  `json:"enabled"`
	Connected       bool                  `json:"connected"`
	AuthState       string                `json:"authState,omitempty"`
	MembershipState SourceMembershipState `json:"membershipState"`
	QuotaState      string                `json:"quotaState,omitempty"`
	QuotaEvidence   QuotaEvidence         `json:"quotaEvidence,omitempty"`
	ResetEpoch      int64                 `json:"resetEpoch,omitempty"`
	Revision        uint64                `json:"revision"`
	LastObservedAt  int64                 `json:"lastObservedAt,omitempty"`
	DepletedAt      int64                 `json:"depletedAt,omitempty"`
	RecoveredAt     int64                 `json:"recoveredAt,omitempty"`
	CircuitState    string                `json:"circuitState,omitempty"`
	Compatibility   string                `json:"compatibility,omitempty"`
}

type PoolLease struct {
	LeaseID              string         `json:"leaseId"`
	PoolID               string         `json:"poolId"`
	LogicalSessionID     string         `json:"logicalSessionId"`
	LogicalTurnID        string         `json:"logicalTurnId"`
	ThreadID             string         `json:"threadId"`
	SourceID             string         `json:"sourceId"`
	PoolRevision         uint64         `json:"poolRevision"`
	SourceRevision       uint64         `json:"sourceRevision"`
	State                PoolLeaseState `json:"state"`
	CreatedAt            int64          `json:"createdAt"`
	AcceptedAt           int64          `json:"acceptedAt,omitempty"`
	FirstVisibleOutputAt int64          `json:"firstVisibleOutputAt,omitempty"`
	SideEffectsObserved  bool           `json:"sideEffectsObserved,omitempty"`
	HeartbeatAt          int64          `json:"heartbeatAt"`
	ExpiresAt            int64          `json:"expiresAt"`
	FailoverCount        uint64         `json:"failoverCount"`
	RetryCount           uint64         `json:"retryCount"`
	ExcludedSources      []string       `json:"excludedSources,omitempty"`
}

type PoolTransition struct {
	FromSourceID string `json:"fromSourceId,omitempty"`
	ToSourceID   string `json:"toSourceId,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Revision     uint64 `json:"revision"`
	OccurredAt   int64  `json:"occurredAt"`
}

type PoolState struct {
	PoolID            string                           `json:"poolId"`
	SchemaVersion     int                              `json:"schemaVersion"`
	Revision          uint64                           `json:"revision"`
	MembershipEpoch   uint64                           `json:"membershipEpoch"`
	QuotaEpoch        uint64                           `json:"quotaEpoch"`
	ActiveSourceID    string                           `json:"activeSourceId,omitempty"`
	SourceOrder       []string                         `json:"sourceOrder"`
	Sources           map[string]CredentialSourceState `json:"sources"`
	ActiveLeases      map[string]PoolLease             `json:"activeLeases"`
	ConfirmedHeadroom float64                          `json:"confirmedHeadroom"`
	UnknownHeadroom   float64                          `json:"unknownHeadroom"`
	MaximumHeadroom   float64                          `json:"maximumHeadroom"`
	NextResetAt       int64                            `json:"nextResetAt,omitempty"`
	Health            string                           `json:"health"`
	FailoverCount     uint64                           `json:"failoverCount"`
	LastTransition    PoolTransition                   `json:"lastTransition,omitempty"`
}

type TaskRecord struct {
	ThreadID            string `json:"threadId"`
	CanonicalGeneration uint64 `json:"canonicalGeneration"`
	CheckpointSHA256    string `json:"checkpointSha256,omitempty"`
	CheckpointSize      int64  `json:"checkpointSize,omitempty"`
	CheckpointPath      string `json:"checkpointPath,omitempty"`
	LastCompletedTurnID string `json:"lastCompletedTurnId,omitempty"`
	GoalID              string `json:"goalId,omitempty"`
	ActiveLeaseID       string `json:"activeLeaseId,omitempty"`
	RecoveryState       string `json:"recoveryState,omitempty"`
	CreatedAt           int64  `json:"createdAt"`
	UpdatedAt           int64  `json:"updatedAt"`
}

func defaultPoolState(accounts []Account) PoolState {
	now := time.Now().UnixMilli()
	pool := PoolState{
		PoolID: DefaultPoolID, SchemaVersion: PoolSchemaVersion, Revision: 1,
		MembershipEpoch: 1, Sources: make(map[string]CredentialSourceState),
		ActiveLeases: make(map[string]PoolLease), Health: "warming",
	}
	ordered := slices.Clone(accounts)
	slices.SortStableFunc(ordered, func(a, b Account) int {
		if a.Controller != b.Controller {
			if a.Controller {
				return -1
			}
			return 1
		}
		if a.CreatedAt < b.CreatedAt {
			return -1
		}
		if a.CreatedAt > b.CreatedAt {
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	for _, account := range ordered {
		if strings.TrimSpace(account.ID) == "" {
			continue
		}
		membership := SourceProvisioning
		if account.PendingLogin {
			membership = SourceLoginPending
		} else if account.Enabled {
			membership = SourceProbing
		}
		pool.SourceOrder = append(pool.SourceOrder, account.ID)
		pool.Sources[account.ID] = CredentialSourceState{
			SourceID: account.ID, Enabled: account.Enabled, MembershipState: membership,
			Revision: 1, LastObservedAt: now,
		}
		pool.MaximumHeadroom += 100
	}
	return pool
}

func normalizePoolState(value PoolState, accounts []Account) PoolState {
	if value.PoolID == "" {
		value.PoolID = DefaultPoolID
	}
	value.SchemaVersion = PoolSchemaVersion
	if value.Revision == 0 {
		value.Revision = 1
	}
	if value.Sources == nil {
		value.Sources = make(map[string]CredentialSourceState)
	}
	if value.ActiveLeases == nil {
		value.ActiveLeases = make(map[string]PoolLease)
	}
	accountIDs := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		accountIDs[account.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(value.SourceOrder))
	order := make([]string, 0, len(accounts))
	for _, sourceID := range value.SourceOrder {
		if _, exists := accountIDs[sourceID]; !exists {
			continue
		}
		if _, ok := value.Sources[sourceID]; !ok {
			continue
		}
		if _, duplicate := seen[sourceID]; duplicate {
			continue
		}
		seen[sourceID] = struct{}{}
		order = append(order, sourceID)
	}
	for _, account := range accounts {
		if _, ok := seen[account.ID]; !ok {
			order = append(order, account.ID)
			seen[account.ID] = struct{}{}
		}
		source := value.Sources[account.ID]
		source.SourceID = account.ID
		source.Enabled = account.Enabled
		if source.Revision == 0 {
			source.Revision = 1
		}
		if source.MembershipState == "" {
			if account.PendingLogin {
				source.MembershipState = SourceLoginPending
			} else {
				source.MembershipState = SourceProbing
			}
		}
		value.Sources[account.ID] = source
	}
	for sourceID, source := range value.Sources {
		if _, exists := accountIDs[sourceID]; exists {
			continue
		}
		source.Enabled = false
		source.MembershipState = SourceRemoved
		value.Sources[sourceID] = source
	}
	if _, exists := accountIDs[value.ActiveSourceID]; !exists {
		value.ActiveSourceID = ""
	}
	value.SourceOrder = order
	recomputePoolMetrics(&value)
	return value
}

// recomputePoolMetrics keeps the persisted pool projection in lockstep with
// its private source evidence. Older builds only calculated these values in
// the HTTP status handler, which meant a freshly started Relay could expose a
// stale `warming/0%` state in state.json even after every account had returned
// a healthy quota snapshot. The same projection is now updated on every state
// mutation, so crash recovery, diagnostics and the live status endpoint agree.
func recomputePoolMetrics(pool *PoolState) {
	if pool == nil {
		return
	}
	maximum, confirmed, unknown := 0.0, 0.0, 0.0
	connected, eligible, known := 0, 0, 0
	for _, sourceID := range pool.SourceOrder {
		source, ok := pool.Sources[sourceID]
		if !ok || !source.Enabled || source.MembershipState == SourceRemoved || source.MembershipState == SourceDraining {
			continue
		}
		maximum += 100
		if source.Connected && source.AuthState == "authenticated" {
			connected++
		}
		evidence := source.QuotaEvidence
		evidenceKnown := evidence.Allowed != nil || evidence.LimitReached || evidence.ShortUsed != nil || evidence.LongUsed != nil
		if evidenceKnown {
			known++
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
			if used < 0 {
				used = 0
			}
			if used > 100 {
				used = 100
			}
			confirmed += 100 - used
		} else {
			unknown += 100
		}
		if sourceEligible(source) {
			eligible++
		}
	}
	pool.MaximumHeadroom = maximum
	pool.ConfirmedHeadroom = confirmed
	pool.UnknownHeadroom = unknown
	switch {
	case eligible > 0 && known > 0:
		pool.Health = "healthy"
	case eligible > 0:
		pool.Health = "warming"
	case connected > 0:
		pool.Health = "depleted"
	default:
		pool.Health = "warming"
	}
}

func clonePoolState(value PoolState) PoolState {
	value.SourceOrder = slices.Clone(value.SourceOrder)
	value.Sources = cloneMap(value.Sources)
	value.ActiveLeases = cloneMap(value.ActiveLeases)
	for id, lease := range value.ActiveLeases {
		lease.ExcludedSources = slices.Clone(lease.ExcludedSources)
		value.ActiveLeases[id] = lease
	}
	return value
}

func cloneMap[K comparable, V any](input map[K]V) map[K]V {
	result := make(map[K]V, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func (s *Store) syncAccountSourceLocked(account Account) {
	s.pool = normalizePoolState(s.pool, s.accounts)
	source := s.pool.Sources[account.ID]
	source.SourceID = account.ID
	source.Enabled = account.Enabled
	if account.PendingLogin {
		source.MembershipState = SourceLoginPending
	} else if !account.Enabled {
		source.MembershipState = SourceDraining
	} else if source.MembershipState == "" || source.MembershipState == SourceRemoved || source.MembershipState == SourceLoginPending {
		source.MembershipState = SourceProbing
	}
	if source.Revision == 0 {
		source.Revision = 1
	} else {
		source.Revision++
	}
	s.pool.Sources[account.ID] = source
	s.pool.MembershipEpoch++
	s.pool.Revision++
	recomputePoolMetrics(&s.pool)
}

func (s *Store) markSourceRemovedLocked(sourceID string) {
	source, ok := s.pool.Sources[sourceID]
	if !ok {
		return
	}
	source.Enabled = false
	source.Connected = false
	source.MembershipState = SourceRemoved
	source.Revision++
	s.pool.Sources[sourceID] = source
	if s.pool.ActiveSourceID == sourceID {
		s.pool.ActiveSourceID = ""
	}
	s.pool.MembershipEpoch++
	s.pool.Revision++
	s.pool = normalizePoolState(s.pool, s.accounts)
}

func (s *Store) PoolState() PoolState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clonePoolState(s.pool)
}

func (s *Store) TaskRecords() map[string]TaskRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneMap(s.tasks)
}

func (s *Store) PutTaskRecord(task TaskRecord) error {
	task.ThreadID = strings.TrimSpace(task.ThreadID)
	if task.ThreadID == "" {
		return errors.New("thread ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.tasks[task.ThreadID]
	if task.CreatedAt == 0 {
		if existed {
			task.CreatedAt = previous.CreatedAt
		} else {
			task.CreatedAt = time.Now().UnixMilli()
		}
	}
	task.UpdatedAt = time.Now().UnixMilli()
	s.tasks[task.ThreadID] = task
	if err := s.saveLocked(); err != nil {
		if existed {
			s.tasks[task.ThreadID] = previous
		} else {
			delete(s.tasks, task.ThreadID)
		}
		return err
	}
	return nil
}

func (s *Store) UpdatePool(expectedRevision uint64, update func(*PoolState) error) (PoolState, error) {
	if update == nil {
		return PoolState{}, errors.New("pool update is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if expectedRevision != 0 && s.pool.Revision != expectedRevision {
		return clonePoolState(s.pool), fmt.Errorf("pool revision mismatch: have %d want %d", s.pool.Revision, expectedRevision)
	}
	previous := clonePoolState(s.pool)
	next := clonePoolState(s.pool)
	if err := update(&next); err != nil {
		return previous, err
	}
	next = normalizePoolState(next, s.accounts)
	next.Revision = previous.Revision + 1
	s.pool = next
	if err := s.saveLocked(); err != nil {
		s.pool = previous
		return previous, err
	}
	return clonePoolState(s.pool), nil
}

func sourceEligible(source CredentialSourceState) bool {
	if !source.Enabled || !source.Connected || source.AuthState != "authenticated" {
		return false
	}
	switch source.MembershipState {
	case SourceAvailable, SourceActive, SourceProbation:
		return !source.QuotaEvidence.ExplicitlyDepleted()
	default:
		return false
	}
}

func (s *Store) UpdateCredentialSource(sourceID string, update func(*CredentialSourceState) error) (CredentialSourceState, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" || update == nil {
		return CredentialSourceState{}, errors.New("source ID and update are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previousPool := clonePoolState(s.pool)
	source, ok := s.pool.Sources[sourceID]
	if !ok {
		return CredentialSourceState{}, fmt.Errorf("quota source %q not found", sourceID)
	}
	if err := update(&source); err != nil {
		return CredentialSourceState{}, err
	}
	source.SourceID = sourceID
	source.Revision++
	s.pool.Sources[sourceID] = source
	recomputePoolMetrics(&s.pool)
	s.pool.Revision++
	if err := s.saveLocked(); err != nil {
		s.pool = previousPool
		return CredentialSourceState{}, err
	}
	return source, nil
}

func nextEligibleSource(pool *PoolState, excluded map[string]struct{}) string {
	for _, sourceID := range pool.SourceOrder {
		if _, skip := excluded[sourceID]; skip {
			continue
		}
		if source, ok := pool.Sources[sourceID]; ok && sourceEligible(source) {
			return sourceID
		}
	}
	return ""
}

func (s *Store) AcquirePoolLease(lease PoolLease, ttl time.Duration) (PoolLease, error) {
	if strings.TrimSpace(lease.LeaseID) == "" || strings.TrimSpace(lease.LogicalTurnID) == "" {
		return PoolLease{}, errors.New("lease ID and logical turn ID are required")
	}
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previousPool := clonePoolState(s.pool)
	previousTasks := cloneMap(s.tasks)
	if existing, ok := s.pool.ActiveLeases[lease.LeaseID]; ok {
		return existing, nil
	}
	for _, active := range s.pool.ActiveLeases {
		if active.LogicalTurnID == lease.LogicalTurnID && active.State != PoolLeaseCompleted && active.State != PoolLeaseRolledBack {
			return PoolLease{}, fmt.Errorf("logical turn %q already has active lease %q", lease.LogicalTurnID, active.LeaseID)
		}
	}
	excluded := make(map[string]struct{}, len(lease.ExcludedSources))
	for _, sourceID := range lease.ExcludedSources {
		excluded[sourceID] = struct{}{}
	}
	sourceID := s.pool.ActiveSourceID
	_, activeExcluded := excluded[sourceID]
	if source, ok := s.pool.Sources[sourceID]; sourceID == "" || activeExcluded || !ok || !sourceEligible(source) {
		sourceID = nextEligibleSource(&s.pool, excluded)
	}
	if sourceID == "" {
		return PoolLease{}, errors.New("Relay Pool has no usable quota source")
	}
	now := time.Now().UnixMilli()
	source := s.pool.Sources[sourceID]
	if source.MembershipState != SourceActive {
		source.MembershipState = SourceActive
		source.Revision++
		s.pool.Sources[sourceID] = source
	}
	s.pool.ActiveSourceID = sourceID
	s.pool.Health = "healthy"
	s.pool.Revision++
	lease.PoolID = s.pool.PoolID
	lease.SourceID = sourceID
	lease.PoolRevision = s.pool.Revision
	lease.SourceRevision = source.Revision
	lease.State = PoolLeaseBound
	lease.CreatedAt = now
	lease.HeartbeatAt = now
	lease.ExpiresAt = now + ttl.Milliseconds()
	s.pool.ActiveLeases[lease.LeaseID] = lease
	if lease.ThreadID != "" {
		task := s.tasks[lease.ThreadID]
		task.ThreadID = lease.ThreadID
		if task.CreatedAt == 0 {
			task.CreatedAt = now
		}
		task.ActiveLeaseID = lease.LeaseID
		task.UpdatedAt = now
		s.tasks[lease.ThreadID] = task
	}
	recomputePoolMetrics(&s.pool)
	if err := s.saveLocked(); err != nil {
		s.pool = previousPool
		s.tasks = previousTasks
		return PoolLease{}, err
	}
	return lease, nil
}

func (s *Store) HeartbeatPoolLease(leaseID string, ttl time.Duration) (PoolLease, error) {
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.pool.ActiveLeases[leaseID]
	if !ok {
		return PoolLease{}, fmt.Errorf("pool lease %q not found", leaseID)
	}
	previous := lease
	now := time.Now().UnixMilli()
	lease.HeartbeatAt = now
	lease.ExpiresAt = now + ttl.Milliseconds()
	s.pool.ActiveLeases[leaseID] = lease
	if err := s.saveLocked(); err != nil {
		s.pool.ActiveLeases[leaseID] = previous
		return PoolLease{}, err
	}
	return lease, nil
}

func (s *Store) MarkPoolLeaseProgress(leaseID string, state PoolLeaseState, visibleOutput, sideEffect bool) (PoolLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.pool.ActiveLeases[leaseID]
	if !ok {
		return PoolLease{}, fmt.Errorf("pool lease %q not found", leaseID)
	}
	previousPool := clonePoolState(s.pool)
	previousTasks := cloneMap(s.tasks)
	now := time.Now().UnixMilli()
	lease.State = state
	lease.HeartbeatAt = now
	if visibleOutput && lease.FirstVisibleOutputAt == 0 {
		lease.FirstVisibleOutputAt = now
	}
	if sideEffect {
		lease.SideEffectsObserved = true
	}
	s.pool.ActiveLeases[leaseID] = lease
	if state == PoolLeaseRecoveryRequired && lease.ThreadID != "" {
		task := s.tasks[lease.ThreadID]
		task.ThreadID = lease.ThreadID
		if task.CreatedAt == 0 {
			task.CreatedAt = now
		}
		task.ActiveLeaseID = lease.LeaseID
		task.RecoveryState = "recovery-required"
		task.UpdatedAt = now
		s.tasks[lease.ThreadID] = task
	}
	if err := s.saveLocked(); err != nil {
		s.pool = previousPool
		s.tasks = previousTasks
		return PoolLease{}, err
	}
	return lease, nil
}

func (s *Store) MarkPoolQuotaRejected(leaseID, sourceID, reason string, resetEpoch int64) (PoolLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.pool.ActiveLeases[leaseID]
	if !ok {
		return PoolLease{}, fmt.Errorf("pool lease %q not found", leaseID)
	}
	if lease.SourceID != sourceID {
		if slices.Contains(lease.ExcludedSources, sourceID) {
			return lease, nil
		}
		return PoolLease{}, fmt.Errorf("pool lease source mismatch: have %q want %q", lease.SourceID, sourceID)
	}
	previousPool := clonePoolState(s.pool)
	previousTasks := cloneMap(s.tasks)
	now := time.Now().UnixMilli()
	source, exists := s.pool.Sources[sourceID]
	if !exists {
		return PoolLease{}, fmt.Errorf("quota source %q not found", sourceID)
	}
	unsafeReplay := lease.FirstVisibleOutputAt != 0 || lease.SideEffectsObserved
	if source.MembershipState == SourceDepleted {
		if unsafeReplay {
			lease.State = PoolLeaseRecoveryRequired
			s.pool.ActiveLeases[leaseID] = lease
			s.markTaskRecoveryLocked(lease, now)
			if err := s.saveLocked(); err != nil {
				s.pool = previousPool
				s.tasks = previousTasks
				return PoolLease{}, err
			}
			return lease, errors.New("quota rejection occurred after output or side effect; replay is unsafe")
		}
		// Another concurrent lease already committed the one authoritative
		// source transition. Rebind this lease to the current active source
		// without duplicating the pool failover counter or transition ledger.
		excluded := make(map[string]struct{}, len(lease.ExcludedSources)+1)
		for _, id := range lease.ExcludedSources {
			excluded[id] = struct{}{}
		}
		excluded[sourceID] = struct{}{}
		next := s.pool.ActiveSourceID
		_, nextExcluded := excluded[next]
		if nextSource, ok := s.pool.Sources[next]; next == "" || nextExcluded || !ok || !sourceEligible(nextSource) {
			next = nextEligibleSource(&s.pool, excluded)
		}
		lease.ExcludedSources = appendUniqueString(lease.ExcludedSources, sourceID)
		lease.SourceID = next
		lease.FailoverCount++
		lease.RetryCount++
		lease.State = PoolLeaseBound
		if next != "" {
			lease.SourceRevision = s.pool.Sources[next].Revision
		}
		s.pool.ActiveLeases[leaseID] = lease
		recomputePoolMetrics(&s.pool)
		if err := s.saveLocked(); err != nil {
			s.pool = previousPool
			s.tasks = previousTasks
			return PoolLease{}, err
		}
		if next == "" {
			return lease, errors.New("Relay Pool has exhausted every quota source")
		}
		return lease, nil
	}
	source.MembershipState = SourceDepleted
	source.QuotaState = "depleted"
	source.QuotaEvidence.LimitReached = true
	source.QuotaEvidence.ResetEpoch = resetEpoch
	source.QuotaEvidence.ObservedAt = now
	source.DepletedAt = now
	source.ResetEpoch = resetEpoch
	source.Revision++
	s.pool.Sources[sourceID] = source
	excluded := make(map[string]struct{}, len(lease.ExcludedSources)+1)
	for _, id := range lease.ExcludedSources {
		excluded[id] = struct{}{}
	}
	excluded[sourceID] = struct{}{}
	next := nextEligibleSource(&s.pool, excluded)
	s.pool.Revision++
	s.pool.FailoverCount++
	s.pool.ActiveSourceID = next
	s.pool.LastTransition = PoolTransition{FromSourceID: sourceID, ToSourceID: next, Reason: strings.TrimSpace(reason), Revision: s.pool.Revision, OccurredAt: now}
	if next == "" {
		s.pool.Health = "depleted"
	} else {
		nextSource := s.pool.Sources[next]
		nextSource.MembershipState = SourceActive
		nextSource.Revision++
		s.pool.Sources[next] = nextSource
		s.pool.Health = "healthy"
	}
	if unsafeReplay {
		// The pool may advance for future turns, but this already-observable
		// logical turn remains bound to its original source and is never replayed.
		lease.State = PoolLeaseRecoveryRequired
		lease.ExcludedSources = appendUniqueString(lease.ExcludedSources, sourceID)
		s.markTaskRecoveryLocked(lease, now)
	} else {
		lease.SourceID = next
		lease.PoolRevision = s.pool.Revision
		lease.FailoverCount++
		lease.RetryCount++
		lease.ExcludedSources = appendUniqueString(lease.ExcludedSources, sourceID)
		lease.State = PoolLeaseBound
		if next != "" {
			lease.SourceRevision = s.pool.Sources[next].Revision
		}
	}
	s.pool.ActiveLeases[leaseID] = lease
	recomputePoolMetrics(&s.pool)
	if err := s.saveLocked(); err != nil {
		s.pool = previousPool
		s.tasks = previousTasks
		return PoolLease{}, err
	}
	if unsafeReplay {
		return lease, errors.New("quota rejection occurred after output or side effect; replay is unsafe")
	}
	if next == "" {
		return lease, errors.New("Relay Pool has exhausted every quota source")
	}
	return lease, nil
}

// MarkPoolSourceUnavailable removes an unusable credential source from the
// current request without misclassifying an authentication/configuration
// failure as quota exhaustion. Like quota failover, it is permitted only
// before visible output or tool side effects.
func (s *Store) MarkPoolSourceUnavailable(leaseID, sourceID, reason string) (PoolLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.pool.ActiveLeases[leaseID]
	if !ok {
		return PoolLease{}, fmt.Errorf("pool lease %q not found", leaseID)
	}
	if lease.SourceID != sourceID {
		if slices.Contains(lease.ExcludedSources, sourceID) {
			return lease, nil
		}
		return PoolLease{}, fmt.Errorf("pool lease source mismatch: have %q want %q", lease.SourceID, sourceID)
	}
	previousPool := clonePoolState(s.pool)
	previousTasks := cloneMap(s.tasks)
	if lease.FirstVisibleOutputAt != 0 || lease.SideEffectsObserved {
		lease.State = PoolLeaseRecoveryRequired
		s.pool.ActiveLeases[leaseID] = lease
		now := time.Now().UnixMilli()
		s.markTaskRecoveryLocked(lease, now)
		if err := s.saveLocked(); err != nil {
			s.pool = previousPool
			s.tasks = previousTasks
			return PoolLease{}, err
		}
		return lease, errors.New("source failure occurred after output or side effect; replay is unsafe")
	}
	now := time.Now().UnixMilli()
	source, exists := s.pool.Sources[sourceID]
	if !exists {
		return PoolLease{}, fmt.Errorf("quota source %q not found", sourceID)
	}
	source.Connected = false
	source.AuthState = "disconnected"
	source.MembershipState = SourceProvisioning
	source.LastObservedAt = now
	source.Revision++
	s.pool.Sources[sourceID] = source
	excluded := make(map[string]struct{}, len(lease.ExcludedSources)+1)
	for _, id := range lease.ExcludedSources {
		excluded[id] = struct{}{}
	}
	excluded[sourceID] = struct{}{}
	next := nextEligibleSource(&s.pool, excluded)
	s.pool.Revision++
	s.pool.FailoverCount++
	s.pool.ActiveSourceID = next
	s.pool.LastTransition = PoolTransition{
		FromSourceID: sourceID, ToSourceID: next, Reason: strings.TrimSpace(reason),
		Revision: s.pool.Revision, OccurredAt: now,
	}
	if next == "" {
		s.pool.Health = "depleted"
	} else {
		nextSource := s.pool.Sources[next]
		nextSource.MembershipState = SourceActive
		nextSource.Revision++
		s.pool.Sources[next] = nextSource
		s.pool.Health = "healthy"
	}
	lease.SourceID = next
	lease.PoolRevision = s.pool.Revision
	lease.FailoverCount++
	lease.RetryCount++
	lease.ExcludedSources = appendUniqueString(lease.ExcludedSources, sourceID)
	lease.State = PoolLeaseBound
	if next != "" {
		lease.SourceRevision = s.pool.Sources[next].Revision
	}
	s.pool.ActiveLeases[leaseID] = lease
	recomputePoolMetrics(&s.pool)
	if err := s.saveLocked(); err != nil {
		s.pool = previousPool
		return PoolLease{}, err
	}
	if next == "" {
		return lease, errors.New("Relay Pool has exhausted every usable source")
	}
	return lease, nil
}

func (s *Store) CompletePoolLease(leaseID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.pool.ActiveLeases[leaseID]
	if !ok {
		return nil
	}
	previousPool := clonePoolState(s.pool)
	previousTasks := cloneMap(s.tasks)
	lease.State = PoolLeaseCompleted
	if source, exists := s.pool.Sources[lease.SourceID]; exists {
		allowed := true
		now := time.Now().UnixMilli()
		source.MembershipState = SourceActive
		source.QuotaState = "available"
		source.QuotaEvidence.Allowed = &allowed
		source.QuotaEvidence.LimitReached = false
		source.QuotaEvidence.ObservedAt = now
		source.LastObservedAt = now
		source.RecoveredAt = now
		source.Revision++
		s.pool.Sources[lease.SourceID] = source
		s.pool.ActiveSourceID = lease.SourceID
		s.pool.Health = "healthy"
		s.pool.Revision++
	}
	recomputePoolMetrics(&s.pool)
	s.pool.ActiveLeases[leaseID] = lease
	delete(s.pool.ActiveLeases, leaseID)
	if lease.ThreadID != "" {
		task := s.tasks[lease.ThreadID]
		if task.ThreadID == "" {
			task.ThreadID = lease.ThreadID
			task.CreatedAt = time.Now().UnixMilli()
		}
		if task.ActiveLeaseID == leaseID {
			task.ActiveLeaseID = ""
		}
		task.LastCompletedTurnID = lease.LogicalTurnID
		task.RecoveryState = ""
		task.UpdatedAt = time.Now().UnixMilli()
		s.tasks[lease.ThreadID] = task
	}
	if err := s.saveLocked(); err != nil {
		s.pool = previousPool
		s.tasks = previousTasks
		return err
	}
	return nil
}

// AbortPoolLease closes a request that failed before any output or side
// effect. It never records the logical turn as completed. The TaskRecord is
// retained so a reset or newly added source can continue the same thread.
func (s *Store) AbortPoolLease(leaseID, recoveryState string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.pool.ActiveLeases[leaseID]
	if !ok {
		return nil
	}
	previousPool := clonePoolState(s.pool)
	previousTasks := cloneMap(s.tasks)
	delete(s.pool.ActiveLeases, leaseID)
	if lease.ThreadID != "" {
		task := s.tasks[lease.ThreadID]
		if task.ThreadID == "" {
			task.ThreadID = lease.ThreadID
			task.CreatedAt = time.Now().UnixMilli()
		}
		if task.ActiveLeaseID == leaseID {
			task.ActiveLeaseID = ""
		}
		task.RecoveryState = strings.TrimSpace(recoveryState)
		task.UpdatedAt = time.Now().UnixMilli()
		s.tasks[lease.ThreadID] = task
	}
	if err := s.saveLocked(); err != nil {
		s.pool = previousPool
		s.tasks = previousTasks
		return err
	}
	return nil
}

func appendUniqueString(values []string, value string) []string {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}

func (s *Store) RecoverPoolLeases(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for leaseID, lease := range s.pool.ActiveLeases {
		if lease.State == PoolLeaseCompleted || lease.State == PoolLeaseRolledBack {
			delete(s.pool.ActiveLeases, leaseID)
			changed = true
			continue
		}
		if lease.ExpiresAt > 0 && lease.ExpiresAt <= now.UnixMilli() {
			lease.State = PoolLeaseRecoveryRequired
			s.pool.ActiveLeases[leaseID] = lease
			s.markTaskRecoveryLocked(lease, now.UnixMilli())
			changed = true
		}
	}
	if !changed {
		return nil
	}
	s.pool.Revision++
	return s.saveLocked()
}

func (s *Store) markTaskRecoveryLocked(lease PoolLease, now int64) {
	if lease.ThreadID == "" {
		return
	}
	task := s.tasks[lease.ThreadID]
	task.ThreadID = lease.ThreadID
	if task.CreatedAt == 0 {
		task.CreatedAt = now
	}
	task.ActiveLeaseID = lease.LeaseID
	task.RecoveryState = "recovery-required"
	task.UpdatedAt = now
	s.tasks[lease.ThreadID] = task
}
