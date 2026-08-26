package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func boolPointer(value bool) *bool { return &value }

func newPoolTestStore(t *testing.T) (*Store, []string) {
	t.Helper()
	root := t.TempDir()
	store, err := Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"primary"}
	for _, label := range []string{"B", "C", "D"} {
		account, addErr := store.AddAccount(label)
		if addErr != nil {
			t.Fatal(addErr)
		}
		ids = append(ids, account.ID)
	}
	pool := store.PoolState()
	for _, sourceID := range ids {
		if _, updateErr := store.UpdateCredentialSource(sourceID, func(source *CredentialSourceState) error {
			source.Connected = true
			source.AuthState = "authenticated"
			source.MembershipState = SourceAvailable
			source.QuotaEvidence = QuotaEvidence{Allowed: boolPointer(true), ObservedAt: time.Now().UnixMilli(), Source: "test"}
			return nil
		}); updateErr != nil {
			t.Fatal(updateErr)
		}
	}
	updated := store.PoolState()
	if updated.Revision <= pool.Revision {
		t.Fatal("source probes did not advance pool revision")
	}
	return store, ids
}

func TestConcurrentQuotaRejectionsCommitOnePoolTransition(t *testing.T) {
	store, ids := newPoolTestStore(t)
	leases := make([]PoolLease, 8)
	for index := range leases {
		lease, err := store.AcquirePoolLease(PoolLease{
			LeaseID: fmt.Sprintf("parallel-%d", index), LogicalTurnID: fmt.Sprintf("turn-%d", index),
		}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		leases[index] = lease
	}
	var wait sync.WaitGroup
	errorsByLease := make(chan error, len(leases))
	for _, lease := range leases {
		wait.Add(1)
		go func(lease PoolLease) {
			defer wait.Done()
			_, err := store.MarkPoolQuotaRejected(lease.LeaseID, ids[0], "concurrent rejection", 123)
			errorsByLease <- err
		}(lease)
	}
	wait.Wait()
	close(errorsByLease)
	for err := range errorsByLease {
		if err != nil {
			t.Fatal(err)
		}
	}
	pool := store.PoolState()
	if pool.FailoverCount != 1 || pool.ActiveSourceID != ids[1] {
		t.Fatalf("concurrent rejection committed duplicate transitions: count=%d active=%q", pool.FailoverCount, pool.ActiveSourceID)
	}
}

func TestPoolStickyUntilExplicitQuotaRejection(t *testing.T) {
	store, ids := newPoolTestStore(t)
	lease1, err := store.AcquirePoolLease(PoolLease{LeaseID: "lease-1", LogicalSessionID: "session", LogicalTurnID: "turn-1", ThreadID: "thread"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if lease1.SourceID != ids[0] {
		t.Fatalf("first source=%q want %q", lease1.SourceID, ids[0])
	}
	if err := store.CompletePoolLease(lease1.LeaseID); err != nil {
		t.Fatal(err)
	}
	lease2, err := store.AcquirePoolLease(PoolLease{LeaseID: "lease-2", LogicalSessionID: "session", LogicalTurnID: "turn-2", ThreadID: "thread"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if lease2.SourceID != ids[0] {
		t.Fatalf("pool rotated while source A remained usable: %q", lease2.SourceID)
	}
	lease2, err = store.MarkPoolQuotaRejected(lease2.LeaseID, ids[0], "structured quota rejection", 123)
	if err != nil {
		t.Fatal(err)
	}
	if lease2.SourceID != ids[1] || lease2.FailoverCount != 1 {
		t.Fatalf("A did not transition once to B: %#v", lease2)
	}
	again, err := store.MarkPoolQuotaRejected(lease2.LeaseID, ids[0], "duplicate notification", 123)
	if err != nil {
		t.Fatal(err)
	}
	if again.SourceID != ids[1] || again.FailoverCount != 1 {
		t.Fatalf("duplicate rejection transitioned twice: %#v", again)
	}
}

func TestPoolProjectionTracksQuotaEvidenceAndHealth(t *testing.T) {
	store, ids := newPoolTestStore(t)
	pool := store.PoolState()
	if pool.MaximumHeadroom != 400 || pool.ConfirmedHeadroom != 400 || pool.UnknownHeadroom != 0 {
		t.Fatalf("healthy pool projection is stale: maximum=%v confirmed=%v unknown=%v", pool.MaximumHeadroom, pool.ConfirmedHeadroom, pool.UnknownHeadroom)
	}
	if pool.Health != "healthy" {
		t.Fatalf("healthy quota evidence produced health=%q", pool.Health)
	}

	for _, sourceID := range ids {
		if _, err := store.UpdateCredentialSource(sourceID, func(source *CredentialSourceState) error {
			source.Connected = true
			source.AuthState = "authenticated"
			source.MembershipState = SourceProbation
			source.QuotaEvidence = QuotaEvidence{}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	pool = store.PoolState()
	if pool.MaximumHeadroom != 400 || pool.ConfirmedHeadroom != 0 || pool.UnknownHeadroom != 400 {
		t.Fatalf("probationary pool projection is stale: maximum=%v confirmed=%v unknown=%v", pool.MaximumHeadroom, pool.ConfirmedHeadroom, pool.UnknownHeadroom)
	}
	if pool.Health != "warming" {
		t.Fatalf("unknown quota evidence produced health=%q", pool.Health)
	}
}

func TestPoolLeaseHonorsExcludedActiveSource(t *testing.T) {
	store, _ := newPoolTestStore(t)
	first, err := store.AcquirePoolLease(PoolLease{LeaseID: "active-source", LogicalTurnID: "turn-active"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AcquirePoolLease(PoolLease{
		LeaseID: "excluded-active-source", LogicalTurnID: "turn-next",
		ExcludedSources: []string{first.SourceID},
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if second.SourceID == first.SourceID {
		t.Fatalf("lease reused explicitly excluded active source %q", second.SourceID)
	}
}

func TestGoalIdentitySurvivesPoolCredentialFailover(t *testing.T) {
	store, ids := newPoolTestStore(t)
	if err := store.PutTaskRecord(TaskRecord{
		ThreadID: "goal-thread", GoalID: "goal-constant", CanonicalGeneration: 4,
	}); err != nil {
		t.Fatal(err)
	}
	lease, err := store.AcquirePoolLease(PoolLease{
		LeaseID: "goal-lease", LogicalSessionID: "relay-session", LogicalTurnID: "goal-turn-1", ThreadID: "goal-thread",
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if lease.SourceID != ids[0] {
		t.Fatalf("goal started on unexpected source: %q", lease.SourceID)
	}
	rebound, err := store.MarkPoolQuotaRejected(lease.LeaseID, ids[0], "goal source quota rejected", 9)
	if err != nil {
		t.Fatal(err)
	}
	if rebound.SourceID != ids[1] || rebound.LogicalSessionID != "relay-session" || rebound.ThreadID != "goal-thread" {
		t.Fatalf("credential failover changed logical task identity: %#v", rebound)
	}
	task := store.TaskRecords()["goal-thread"]
	if task.GoalID != "goal-constant" || task.CanonicalGeneration != 4 || task.ActiveLeaseID != rebound.LeaseID {
		t.Fatalf("goal state was not preserved across source failover: %#v", task)
	}
	if err := store.CompletePoolLease(rebound.LeaseID); err != nil {
		t.Fatal(err)
	}
	finalTask := store.TaskRecords()["goal-thread"]
	if finalTask.GoalID != "goal-constant" || finalTask.ActiveLeaseID != "" || finalTask.RecoveryState != "" {
		t.Fatalf("completed goal lost identity or retained stale lease: %#v", finalTask)
	}
}

func TestPoolCASHeartbeatAndCrashRecovery(t *testing.T) {
	store, _ := newPoolTestStore(t)
	pool := store.PoolState()
	if _, err := store.UpdatePool(pool.Revision+1, func(*PoolState) error { return nil }); err == nil {
		t.Fatal("stale pool revision was accepted")
	}
	lease, err := store.AcquirePoolLease(PoolLease{LeaseID: "lease-long", LogicalTurnID: "turn-long", ThreadID: "thread"}, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	heartbeat, err := store.HeartbeatPoolLease(lease.LeaseID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if heartbeat.ExpiresAt <= lease.ExpiresAt {
		t.Fatal("heartbeat did not extend the lease")
	}
	if err := store.RecoverPoolLeases(time.UnixMilli(heartbeat.ExpiresAt + 1)); err != nil {
		t.Fatal(err)
	}
	recovered := store.PoolState().ActiveLeases[lease.LeaseID]
	if recovered.State != PoolLeaseRecoveryRequired {
		t.Fatalf("expired uncertain lease=%q", recovered.State)
	}
}

func TestPoolWillNotReplayAfterVisibleOutput(t *testing.T) {
	store, ids := newPoolTestStore(t)
	lease, err := store.AcquirePoolLease(PoolLease{LeaseID: "lease-visible", LogicalTurnID: "turn-visible", ThreadID: "thread"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.UpdatePool(store.PoolState().Revision, func(pool *PoolState) error {
		value := pool.ActiveLeases[lease.LeaseID]
		value.FirstVisibleOutputAt = time.Now().UnixMilli()
		pool.ActiveLeases[lease.LeaseID] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.MarkPoolQuotaRejected(lease.LeaseID, ids[0], "quota after output", 1)
	if err == nil || got.State != PoolLeaseRecoveryRequired {
		t.Fatalf("unsafe replay was not blocked: lease=%#v err=%v", got, err)
	}
	if store.PoolState().ActiveSourceID != ids[1] || got.SourceID != ids[0] {
		t.Fatalf("future turns did not advance while preserving the failed turn binding: pool=%#v lease=%#v", store.PoolState(), got)
	}
	if task := store.TaskRecords()["thread"]; task.RecoveryState != "recovery-required" || task.ActiveLeaseID != lease.LeaseID {
		t.Fatalf("unsafe replay did not persist task recovery: %#v", task)
	}
}

func TestPoolDepletedRequestDoesNotLeaveLeaseOrFakeCompletedTurn(t *testing.T) {
	store, ids := newPoolTestStore(t)
	lease, err := store.AcquirePoolLease(PoolLease{LeaseID: "lease-empty", LogicalTurnID: "turn-empty", ThreadID: "thread-empty"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < len(ids); index++ {
		lease, err = store.MarkPoolQuotaRejected(lease.LeaseID, lease.SourceID, "quota rejected", 123)
		if index < len(ids)-1 && err != nil {
			t.Fatalf("source %d rejected too early: lease=%#v err=%v", index, lease, err)
		}
	}
	if err == nil || lease.State != PoolLeaseBound {
		t.Fatalf("final depletion did not return a bounded failed lease: %#v err=%v", lease, err)
	}
	if err := store.AbortPoolLease(lease.LeaseID, "pool-depleted"); err != nil {
		t.Fatal(err)
	}
	pool := store.PoolState()
	if len(pool.ActiveLeases) != 0 || pool.ActiveSourceID != "" || pool.Health != "depleted" {
		t.Fatalf("depleted request left live pool state: %#v", pool)
	}
	if task := store.TaskRecords()["thread-empty"]; task.LastCompletedTurnID != "" || task.ActiveLeaseID != "" || task.RecoveryState != "pool-depleted" {
		t.Fatalf("depleted request fabricated completion or lost recovery state: %#v", task)
	}
}

func TestV3ContinuouslyWritesRecoverySafeV2RollbackProjection(t *testing.T) {
	store, _ := newPoolTestStore(t)
	if err := store.PutTaskRecord(TaskRecord{
		ThreadID: "thread-rollback", CanonicalGeneration: 7,
		CheckpointSHA256: "abc123", CheckpointSize: 42, ActiveLeaseID: "lease-active",
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Root(), "state.json.v2.rollback")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var projected persistedV2Projection
	if err := json.Unmarshal(data, &projected); err != nil {
		t.Fatal(err)
	}
	route := projected.ThreadRoutes["thread-rollback"]
	if projected.Version != 2 || projected.ThreadOwner["thread-rollback"] != "primary" ||
		!route.RecoveryRequired || route.AccountID != "primary" || len(projected.Scheduler.Reservations) != 0 {
		t.Fatalf("unsafe rollback projection: %#v", projected)
	}
	manifestData, err := os.ReadFile(path + ".manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		SHA256                     string `json:"sha256"`
		ActiveTasksRequireRecovery int    `json:"activeTasksRequireRecovery"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if manifest.SHA256 != hex.EncodeToString(digest[:]) || manifest.ActiveTasksRequireRecovery != 1 {
		t.Fatalf("rollback manifest does not verify projection: %#v", manifest)
	}

	restoreRoot := filepath.Join(t.TempDir(), "restore")
	if err := os.MkdirAll(restoreRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(restoreRoot, "state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(restoreRoot, store.PrimaryCodexHome())
	if err != nil {
		t.Fatal(err)
	}
	task := restored.TaskRecords()["thread-rollback"]
	if task.ThreadID == "" || task.RecoveryState != "recovery-required" || task.CanonicalGeneration != 7 {
		t.Fatalf("v2 rollback did not remigrate safely: %#v", task)
	}
}
