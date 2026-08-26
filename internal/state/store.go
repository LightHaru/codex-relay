package state

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

const stateVersion = 3

type Account struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	CodexHome  string `json:"codexHome"`
	Enabled    bool   `json:"enabled"`
	Controller bool   `json:"controller"`
	// PendingLogin records an intentionally started secondary-account browser
	// sign-in. It is persisted so reopening Relay can restore the actionable
	// "waiting for sign-in" row instead of guessing from a disconnected child.
	// PendingLoginID is kept private to the control API and is never returned to
	// the renderer; it lets cancellation finish a flow after a Relay restart.
	PendingLogin   bool   `json:"pendingLogin,omitempty"`
	PendingLoginID string `json:"pendingLoginId,omitempty"`
	CreatedAt      int64  `json:"createdAt"`
}

type persistedState struct {
	Version      int                            `json:"version"`
	Accounts     []Account                      `json:"accounts"`
	ThreadOwner  map[string]string              `json:"threadOwner,omitempty"`
	ThreadRoutes map[string]ThreadRoute         `json:"threadRoutes"`
	Scheduler    SchedulerState                 `json:"scheduler"`
	Health       map[string]AccountHealth       `json:"accountHealth"`
	Attempts     map[string]TurnAttempt         `json:"turnAttempts"`
	Handoffs     map[string]Handoff             `json:"handoffs"`
	Checkpoints  map[string]CanonicalCheckpoint `json:"canonicalCheckpoints"`
	Decisions    []RoutingDecision              `json:"routingDecisions"`
	Capabilities map[string]AccountCapability   `json:"accountCapabilities"`
	Pool         PoolState                      `json:"pool"`
	Tasks        map[string]TaskRecord          `json:"tasks"`
}

type persistedV2Projection struct {
	Version      int                            `json:"version"`
	Accounts     []Account                      `json:"accounts"`
	ThreadOwner  map[string]string              `json:"threadOwner,omitempty"`
	ThreadRoutes map[string]ThreadRoute         `json:"threadRoutes"`
	Scheduler    SchedulerState                 `json:"scheduler"`
	Health       map[string]AccountHealth       `json:"accountHealth"`
	Attempts     map[string]TurnAttempt         `json:"turnAttempts"`
	Handoffs     map[string]Handoff             `json:"handoffs"`
	Checkpoints  map[string]CanonicalCheckpoint `json:"canonicalCheckpoints"`
	Decisions    []RoutingDecision              `json:"routingDecisions"`
	Capabilities map[string]AccountCapability   `json:"accountCapabilities"`
}

// Store persists only routing metadata. OAuth credentials and conversation
// databases remain inside each account's isolated Codex home.
type Store struct {
	mu               sync.RWMutex
	root             string
	path             string
	primaryCodexHome string
	// legacyPrimaryCodexHome is read-only migration context for rollout files
	// that an older Router build recorded under the native Store home. It is
	// never used for credentials, child processes, or managed-config syncing.
	legacyPrimaryCodexHome string
	accounts               []Account
	owners                 map[string]string
	routes                 map[string]ThreadRoute
	scheduler              SchedulerState
	health                 map[string]AccountHealth
	attempts               map[string]TurnAttempt
	handoffs               map[string]Handoff
	checkpoints            map[string]CanonicalCheckpoint
	decisions              []RoutingDecision
	capabilities           map[string]AccountCapability
	pool                   PoolState
	tasks                  map[string]TaskRecord
}

func Open(root, primaryCodexHome string) (*Store, error) {
	return open(root, primaryCodexHome, "", false)
}

// OpenIsolated opens Router state whose primary CODEX_HOME is owned by the
// independent Relay desktop. It migrates only the old Router metadata that
// pointed at the native ~/.codex home; the native directory itself is never
// copied, deleted, or modified.
func OpenIsolated(root, primaryCodexHome, legacyPrimaryHome string) (*Store, error) {
	return open(root, primaryCodexHome, legacyPrimaryHome, true)
}

func open(root, primaryCodexHome, legacyPrimaryHome string, isolatedPrimary bool) (*Store, error) {
	if root == "" {
		return nil, errors.New("state root is required")
	}
	if primaryCodexHome == "" {
		return nil, errors.New("primary Codex home is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create state root: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("secure state root: %w", err)
	}

	store := &Store{
		root:                   root,
		path:                   filepath.Join(root, "state.json"),
		primaryCodexHome:       primaryCodexHome,
		legacyPrimaryCodexHome: strings.TrimSpace(legacyPrimaryHome),
		owners:                 make(map[string]string),
		routes:                 make(map[string]ThreadRoute),
		scheduler:              defaultSchedulerState(),
		health:                 make(map[string]AccountHealth),
		attempts:               make(map[string]TurnAttempt),
		handoffs:               make(map[string]Handoff),
		checkpoints:            make(map[string]CanonicalCheckpoint),
		capabilities:           make(map[string]AccountCapability),
		tasks:                  make(map[string]TaskRecord),
	}
	stateNeedsSave := false
	data, err := os.ReadFile(store.path)
	switch {
	case err == nil:
		var persisted persistedState
		if err := json.Unmarshal(data, &persisted); err != nil {
			backupData, backupErr := os.ReadFile(store.path + ".backup")
			if backupErr != nil || json.Unmarshal(backupData, &persisted) != nil {
				return nil, fmt.Errorf("read state: %w", err)
			}
			data = backupData
			stateNeedsSave = true
		}
		if persisted.Version != 1 && persisted.Version != 2 && persisted.Version != stateVersion {
			return nil, fmt.Errorf("unsupported state version %d", persisted.Version)
		}
		store.accounts = persisted.Accounts
		if persisted.ThreadOwner != nil {
			store.owners = persisted.ThreadOwner
		}
		if persisted.ThreadRoutes != nil {
			store.routes = persisted.ThreadRoutes
		}
		store.scheduler = normalizeScheduler(persisted.Scheduler)
		if persisted.Health != nil {
			store.health = persisted.Health
		}
		if persisted.Attempts != nil {
			store.attempts = persisted.Attempts
		}
		if persisted.Handoffs != nil {
			store.handoffs = persisted.Handoffs
		}
		if persisted.Checkpoints != nil {
			store.checkpoints = persisted.Checkpoints
		}
		store.decisions = persisted.Decisions
		if persisted.Capabilities != nil {
			store.capabilities = persisted.Capabilities
		}
		if persisted.Tasks != nil {
			store.tasks = persisted.Tasks
		}
		if persisted.Version == 1 {
			if err := writeVersionedMigrationBackup(store.path, data, 1); err != nil {
				return nil, err
			}
			for threadID, accountID := range store.owners {
				store.routes[threadID] = ThreadRoute{ThreadID: threadID, AccountID: accountID, Generation: 1, UpdatedAt: time.Now().UnixMilli()}
			}
			stateNeedsSave = true
		}
		if persisted.Version <= 2 {
			if persisted.Version == 2 {
				if err := writeVersionedMigrationBackup(store.path, data, 2); err != nil {
					return nil, err
				}
			}
			store.pool = defaultPoolState(store.accounts)
			for threadID, route := range store.routes {
				recovery := ""
				if route.RecoveryRequired || route.ActiveAttemptID != "" || route.ActiveMigrationID != "" {
					recovery = "recovery-required"
				}
				store.tasks[threadID] = TaskRecord{
					ThreadID: threadID, CanonicalGeneration: route.HistoryGeneration,
					CheckpointSHA256: route.HistorySHA256, CheckpointSize: route.HistorySize,
					LastCompletedTurnID: route.LastCompletedTurnID, RecoveryState: recovery,
					CreatedAt: route.UpdatedAt, UpdatedAt: route.UpdatedAt,
				}
			}
			store.scheduler.Reservations = make(map[string]Reservation)
			stateNeedsSave = true
		} else {
			store.pool = normalizePoolState(persisted.Pool, store.accounts)
		}
	case errors.Is(err, os.ErrNotExist):
		store.accounts = []Account{{
			ID:         "primary",
			Label:      "Primary",
			CodexHome:  primaryCodexHome,
			Enabled:    true,
			Controller: true,
			CreatedAt:  time.Now().Unix(),
		}}
		store.pool = defaultPoolState(store.accounts)
		if err := store.saveLocked(); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("read state: %w", err)
	}
	if isolatedPrimary {
		if err := ensurePrivateCodexHome(primaryCodexHome); err != nil {
			return nil, err
		}
		if changed, migrateErr := store.migrateLegacyAccountHomesLocked(legacyPrimaryHome); migrateErr != nil {
			return nil, migrateErr
		} else if changed {
			stateNeedsSave = true
		}
		// Force the Relay primary onto file-backed credentials even when a
		// previous interrupted startup left a partial config behind. This call
		// only touches the dedicated Relay home, never the native ~/.codex.
		if err := syncIsolatedConfig(primaryCodexHome, primaryCodexHome); err != nil {
			return nil, fmt.Errorf("secure isolated primary config: %w", err)
		}
	}
	if store.ensureNativePrimaryLocked() {
		stateNeedsSave = true
	}
	store.pool = normalizePoolState(store.pool, store.accounts)
	if stateNeedsSave {
		if err := store.saveLocked(); err != nil {
			return nil, err
		}
	}
	for _, account := range store.accounts {
		if samePath(account.CodexHome, primaryCodexHome) {
			continue
		}
		if err := syncIsolatedConfig(primaryCodexHome, account.CodexHome); err != nil {
			return nil, fmt.Errorf("sync account %q config: %w", account.ID, err)
		}
	}
	return store, nil
}

func ensurePrivateCodexHome(codexHome string) error {
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return fmt.Errorf("create isolated primary Codex home: %w", err)
	}
	if err := os.Chmod(codexHome, 0o700); err != nil {
		return fmt.Errorf("secure isolated primary Codex home: %w", err)
	}
	return nil
}

// migrateLegacyAccountHomesLocked changes only Router metadata. Older Router
// builds could persist the native ~/.codex home for the Primary account, and a
// partially completed migration could even leave a secondary row pointing at
// that same home. Neither case is allowed in isolated mode: every matching row
// is moved to a Relay-owned home and its old thread-owner mappings are removed.
// The native home and its rollout/auth files are never copied, deleted, or
// modified, so the official Codex app keeps its own account and chat history.
func (s *Store) migrateLegacyAccountHomesLocked(legacyPrimaryHome string) (bool, error) {
	legacyPrimaryHome = strings.TrimSpace(legacyPrimaryHome)
	if legacyPrimaryHome == "" || samePath(legacyPrimaryHome, s.primaryCodexHome) {
		return false, nil
	}
	changed := false
	for index := range s.accounts {
		account := &s.accounts[index]
		if !samePath(account.CodexHome, legacyPrimaryHome) {
			continue
		}
		if account.ID == "primary" {
			account.CodexHome = s.primaryCodexHome
		} else {
			if account.ID == "" || filepath.Base(account.ID) != account.ID || account.ID == "." || account.ID == ".." {
				return false, fmt.Errorf("account %q has an unsafe ID during isolated migration", account.ID)
			}
			account.CodexHome = filepath.Join(s.root, "accounts", account.ID, "codex-home")
		}
		// The old rollout files live in the native home and are outside this
		// account's new source directory. Keeping the affinity would recreate
		// the "existing chat history is outside the source sessions directory"
		// failure and could route a request with the wrong credentials.
		changed = true
		for threadID, owner := range s.owners {
			if owner == account.ID {
				delete(s.owners, threadID)
				delete(s.routes, threadID)
			}
		}
	}
	return changed, nil
}

// ensureNativePrimaryLocked repairs state written by older Router builds that
// allowed the configured Relay Primary account to disappear after a Primary
// switch or account removal. The configured primary home is always retained as
// a non-controller subscription when another account is selected as Router
// Primary; otherwise it remains the controller. Keeping this metadata lets the
// active Relay home remain addressable across state migrations.
func (s *Store) ensureNativePrimaryLocked() bool {
	for _, account := range s.accounts {
		if samePath(account.CodexHome, s.primaryCodexHome) {
			return false
		}
	}
	hasController := false
	for _, account := range s.accounts {
		if account.Controller {
			hasController = true
			break
		}
	}
	s.accounts = append([]Account{{
		ID:         "primary",
		Label:      "Primary",
		CodexHome:  s.primaryCodexHome,
		Enabled:    true,
		Controller: !hasController,
		CreatedAt:  time.Now().Unix(),
	}}, s.accounts...)
	return true
}

func (s *Store) Root() string {
	return s.root
}

// SyncManagedConfig propagates desktop-managed configuration (including
// plugins, marketplaces, skills, and MCP server definitions) to every
// isolated subscription. Credential stores and project trust remain local to
// each account; syncIsolatedConfig deliberately excludes both.
func (s *Store) SyncManagedConfig() error {
	s.mu.RLock()
	accounts := slices.Clone(s.accounts)
	primaryCodexHome := s.primaryCodexHome
	s.mu.RUnlock()

	for _, account := range accounts {
		if samePath(account.CodexHome, primaryCodexHome) {
			continue
		}
		if err := syncIsolatedConfig(primaryCodexHome, account.CodexHome); err != nil {
			return fmt.Errorf("sync account %q config: %w", account.ID, err)
		}
	}
	return nil
}

func (s *Store) Accounts() []Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.accounts)
}

func (s *Store) Account(id string) (Account, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, account := range s.accounts {
		if account.ID == id {
			return account, true
		}
	}
	return Account{}, false
}

func (s *Store) Controller() (Account, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, account := range s.accounts {
		if account.Controller {
			return account, true
		}
	}
	if len(s.accounts) == 0 {
		return Account{}, false
	}
	return s.accounts[0], true
}

// PrimaryCodexHome returns the home owned by the Relay Primary account. It is
// used by the mux cleanup path to ensure account management can never remove
// the active primary credentials or conversation database.
func (s *Store) PrimaryCodexHome() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.primaryCodexHome
}

// LegacyPrimaryCodexHome returns the former native Store home only as a
// read-only rollout-history migration source. Relay never starts an
// app-server child there and never reads its auth/config files for routing.
func (s *Store) LegacyPrimaryCodexHome() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.legacyPrimaryCodexHome
}

// SetController makes exactly one existing account the Router Primary. The
// caller is responsible for checking that the account is connected and
// eligible; keeping this method state-only makes the persisted transition
// easy to test and avoids coupling the metadata store to app-server I/O.
func (s *Store) SetController(id string) (Account, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Account{}, errors.New("account ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var selected Account
	found := false
	previous := slices.Clone(s.accounts)
	for index := range s.accounts {
		if s.accounts[index].ID == id {
			selected = s.accounts[index]
			found = true
		}
		s.accounts[index].Controller = s.accounts[index].ID == id
	}
	if !found {
		s.accounts = previous
		return Account{}, fmt.Errorf("account %q not found", id)
	}
	if err := s.saveLocked(); err != nil {
		s.accounts = previous
		return Account{}, err
	}
	selected.Controller = true
	return selected, nil
}

func (s *Store) AddAccount(label string) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	label = strings.TrimSpace(label)
	if label == "" {
		label = fmt.Sprintf("Subscription %d", len(s.accounts)+1)
	}
	id, err := randomID()
	if err != nil {
		return Account{}, err
	}
	codexHome := filepath.Join(s.root, "accounts", id, "codex-home")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return Account{}, fmt.Errorf("create account home: %w", err)
	}
	if err := os.Chmod(codexHome, 0o700); err != nil {
		return Account{}, fmt.Errorf("secure account home: %w", err)
	}
	if err := syncIsolatedConfig(s.primaryCodexHome, codexHome); err != nil {
		return Account{}, fmt.Errorf("write account config: %w", err)
	}

	account := Account{
		ID:        id,
		Label:     label,
		CodexHome: codexHome,
		Enabled:   true,
		CreatedAt: time.Now().Unix(),
	}
	previousPool := clonePoolState(s.pool)
	s.accounts = append(s.accounts, account)
	s.syncAccountSourceLocked(account)
	if err := s.saveLocked(); err != nil {
		s.accounts = s.accounts[:len(s.accounts)-1]
		s.pool = previousPool
		return Account{}, err
	}
	return account, nil
}

func (s *Store) UpdateAccount(id string, label *string, enabled *bool) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.accounts {
		if s.accounts[index].ID != id {
			continue
		}
		previous := s.accounts[index]
		previousPool := clonePoolState(s.pool)
		if label != nil {
			trimmed := strings.TrimSpace(*label)
			if trimmed == "" {
				return Account{}, errors.New("account label cannot be empty")
			}
			s.accounts[index].Label = trimmed
		}
		if enabled != nil {
			s.accounts[index].Enabled = *enabled
		}
		s.syncAccountSourceLocked(s.accounts[index])
		if err := s.saveLocked(); err != nil {
			s.accounts[index] = previous
			s.pool = previousPool
			return Account{}, err
		}
		return s.accounts[index], nil
	}
	return Account{}, fmt.Errorf("account %q not found", id)
}

// SetPendingLogin marks an account as intentionally waiting for the official
// browser callback. The login identifier is optional because older Codex
// builds did not return one, but the pending marker itself is always persisted
// so a restart cannot turn an unfinished flow into an ambiguous disconnected
// account row.
func (s *Store) SetPendingLogin(id, loginID string) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.accounts {
		if s.accounts[index].ID != id {
			continue
		}
		previous := s.accounts[index]
		previousPool := clonePoolState(s.pool)
		s.accounts[index].PendingLogin = true
		s.accounts[index].PendingLoginID = strings.TrimSpace(loginID)
		s.syncAccountSourceLocked(s.accounts[index])
		if err := s.saveLocked(); err != nil {
			s.accounts[index] = previous
			s.pool = previousPool
			return Account{}, err
		}
		return s.accounts[index], nil
	}
	return Account{}, fmt.Errorf("account %q not found", id)
}

// ClearPendingLogin removes the persisted sign-in intent. It is idempotent so
// account/login/completed notifications and an account snapshot can race
// safely during the browser callback.
func (s *Store) ClearPendingLogin(id string) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.accounts {
		if s.accounts[index].ID != id {
			continue
		}
		if !s.accounts[index].PendingLogin && s.accounts[index].PendingLoginID == "" {
			return s.accounts[index], nil
		}
		previous := s.accounts[index]
		previousPool := clonePoolState(s.pool)
		s.accounts[index].PendingLogin = false
		s.accounts[index].PendingLoginID = ""
		s.syncAccountSourceLocked(s.accounts[index])
		if err := s.saveLocked(); err != nil {
			s.accounts[index] = previous
			s.pool = previousPool
			return Account{}, err
		}
		return s.accounts[index], nil
	}
	return Account{}, fmt.Errorf("account %q not found", id)
}

// DiscardProvisionalAccount removes a secondary subscription that never
// completed sign-in. Callers must first establish that the account is not
// connected. This narrow operation intentionally cannot remove the controller
// or an account that owns a thread, so a cancelled browser-login flow cannot
// accidentally erase the primary identity or a live conversation assignment.
func (s *Store) DiscardProvisionalAccount(id string) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for index, account := range s.accounts {
		if account.ID != id {
			continue
		}
		if account.Controller || samePath(account.CodexHome, s.primaryCodexHome) {
			return Account{}, errors.New("the Relay primary account cannot be discarded")
		}
		for threadID, route := range s.routes {
			if route.AccountID == id {
				return Account{}, fmt.Errorf(
					"account %q owns thread %q and cannot be discarded", id, threadID,
				)
			}
		}

		previous := s.accounts
		previousPool := clonePoolState(s.pool)
		s.accounts = append(slices.Clone(s.accounts[:index]), s.accounts[index+1:]...)
		s.markSourceRemovedLocked(id)
		if err := s.saveLocked(); err != nil {
			s.accounts = previous
			s.pool = previousPool
			return Account{}, err
		}
		return account, nil
	}
	return Account{}, fmt.Errorf("account %q not found", id)
}

// RemoveAccount removes an account from persistent routing state. Removing
// the controller account is intentionally forbidden; callers must select
// another controller first so configuration and old-chat history never point
// at an ambiguous account. Thread ownership is also protected by default and
// may only be discarded when the caller explicitly passes force=true.
func (s *Store) RemoveAccount(id string, force bool) (Account, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Account{}, errors.New("account ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, account := range s.accounts {
		if account.ID != id {
			continue
		}
		if account.Controller {
			return Account{}, errors.New("choose another Primary account before removing this account")
		}
		if samePath(account.CodexHome, s.primaryCodexHome) {
			return Account{}, errors.New("the Relay primary account cannot be removed")
		}
		if len(s.accounts) <= 1 {
			return Account{}, errors.New("at least one subscription must remain")
		}
		owned := make([]string, 0)
		for threadID, ownerID := range s.owners {
			if ownerID == id {
				owned = append(owned, threadID)
			}
		}
		if len(owned) > 0 && !force {
			return Account{}, fmt.Errorf(
				"account %q owns %d chat(s); confirm removal with force=true", id, len(owned),
			)
		}

		previousAccounts := slices.Clone(s.accounts)
		previousOwners := make(map[string]string, len(s.owners))
		for threadID, ownerID := range s.owners {
			previousOwners[threadID] = ownerID
		}
		previousRoutes := make(map[string]ThreadRoute, len(s.routes))
		for threadID, route := range s.routes {
			previousRoutes[threadID] = route
		}
		previousPool := clonePoolState(s.pool)
		s.accounts = append(slices.Clone(s.accounts[:index]), s.accounts[index+1:]...)
		if force {
			for _, threadID := range owned {
				delete(s.owners, threadID)
				delete(s.routes, threadID)
			}
		}
		s.markSourceRemovedLocked(id)
		if err := s.saveLocked(); err != nil {
			s.accounts = previousAccounts
			s.owners = previousOwners
			s.routes = previousRoutes
			s.pool = previousPool
			return Account{}, err
		}
		return account, nil
	}
	return Account{}, fmt.Errorf("account %q not found", id)
}

func (s *Store) ThreadOwner(threadID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if route, ok := s.routes[threadID]; ok {
		return route.AccountID, true
	}
	owner, ok := s.owners[threadID]
	return owner, ok
}

func (s *Store) SetThreadOwner(threadID, accountID string) error {
	if threadID == "" || accountID == "" {
		return errors.New("thread and account IDs are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owners[threadID] == accountID {
		if route, ok := s.routes[threadID]; ok && route.AccountID == accountID {
			return nil
		}
	}
	previousOwner, ownerExisted := s.owners[threadID]
	previousRoute, routeExisted := s.routes[threadID]
	s.owners[threadID] = accountID
	route := s.routes[threadID]
	route.ThreadID = threadID
	if route.Generation == 0 {
		route.Generation = 1
	} else if route.AccountID != "" && route.AccountID != accountID {
		route.PreviousAccountID = route.AccountID
		route.Generation++
		route.HistoryGeneration = route.Generation
		route.ConsecutiveOwnerTurns = 0
	}
	route.AccountID = accountID
	if route.HistoryGeneration == 0 {
		route.HistoryGeneration = route.Generation
	}
	if !route.Policy.Valid() {
		route.Policy = s.scheduler.Policy
	}
	if route.CurrentState == "" {
		route.CurrentState = "idle"
	}
	route.UpdatedAt = time.Now().UnixMilli()
	s.routes[threadID] = route
	if err := s.saveLocked(); err != nil {
		if ownerExisted {
			s.owners[threadID] = previousOwner
		} else {
			delete(s.owners, threadID)
		}
		if routeExisted {
			s.routes[threadID] = previousRoute
		} else {
			delete(s.routes, threadID)
		}
		return err
	}
	return nil
}

func (s *Store) ThreadCounts() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	counts := make(map[string]int)
	for _, route := range s.routes {
		counts[route.AccountID]++
	}
	return counts
}

// ThreadIDsForAccount returns a snapshot of chat ownership for one account.
// It is used only for an explicit account-removal confirmation/error; callers
// cannot mutate the store through the returned slice.
func (s *Store) ThreadIDsForAccount(accountID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]string, 0)
	for threadID, route := range s.routes {
		if route.AccountID == accountID {
			result = append(result, threadID)
		}
	}
	return result
}

func (s *Store) saveLocked() error {
	persisted := persistedState{
		Version:      stateVersion,
		Accounts:     s.accounts,
		ThreadOwner:  s.owners,
		ThreadRoutes: s.routes,
		Scheduler:    s.scheduler,
		Health:       s.health,
		Attempts:     s.attempts,
		Handoffs:     s.handoffs,
		Checkpoints:  s.checkpoints,
		Decisions:    s.decisions,
		Capabilities: s.capabilities,
		Pool:         s.pool,
		Tasks:        s.tasks,
	}
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		return fmt.Errorf("secure state: %w", err)
	}
	if current, readErr := os.ReadFile(s.path); readErr == nil && json.Valid(current) {
		backupTemporary := s.path + ".backup.tmp"
		if err := os.WriteFile(backupTemporary, current, 0o600); err != nil {
			return fmt.Errorf("write state backup: %w", err)
		}
		if err := os.Chmod(backupTemporary, 0o600); err != nil {
			return fmt.Errorf("secure state backup: %w", err)
		}
		if err := renameStateFile(backupTemporary, s.path+".backup"); err != nil {
			return fmt.Errorf("commit state backup: %w", err)
		}
	}
	// Publish the conservative v2 rollback view before committing the v3
	// document. A rollback projection failure must never be reported after the
	// caller-visible v3 commit has already happened, because callers restore
	// their in-memory mutation when saveLocked returns an error.
	if err := s.writeV2RollbackProjectionLocked(); err != nil {
		return err
	}
	if err := renameStateFile(temporary, s.path); err != nil {
		return fmt.Errorf("commit state: %w", err)
	}
	return nil
}

func (s *Store) writeV2RollbackProjectionLocked() error {
	authorityID := ""
	for _, account := range s.accounts {
		if samePath(account.CodexHome, s.primaryCodexHome) {
			authorityID = account.ID
			break
		}
	}
	if authorityID == "" && len(s.accounts) > 0 {
		authorityID = s.accounts[0].ID
	}
	owners := make(map[string]string, len(s.routes)+len(s.tasks))
	routes := make(map[string]ThreadRoute, len(s.routes)+len(s.tasks))
	for threadID, existing := range s.routes {
		route := existing
		route.AccountID = authorityID
		route.PreviousAccountID = ""
		route.ActiveAttemptID = ""
		route.ActiveMigrationID = ""
		if task, ok := s.tasks[threadID]; ok && task.ActiveLeaseID != "" {
			route.RecoveryRequired = true
			route.CurrentState = "recovery-required"
		} else if !route.RecoveryRequired {
			route.CurrentState = "idle"
		}
		owners[threadID] = authorityID
		routes[threadID] = route
	}
	for threadID, task := range s.tasks {
		if _, exists := routes[threadID]; exists {
			continue
		}
		route := ThreadRoute{
			ThreadID: threadID, AccountID: authorityID, Generation: max(task.CanonicalGeneration, 1),
			HistoryGeneration: task.CanonicalGeneration, HistorySHA256: task.CheckpointSHA256,
			HistorySize: task.CheckpointSize, LastCompletedTurnID: task.LastCompletedTurnID,
			CurrentState: "idle", UpdatedAt: task.UpdatedAt,
		}
		if task.ActiveLeaseID != "" || task.RecoveryState != "" {
			route.RecoveryRequired = true
			route.CurrentState = "recovery-required"
		}
		owners[threadID] = authorityID
		routes[threadID] = route
	}
	scheduler := normalizeScheduler(s.scheduler)
	scheduler.Policy = RoutingPolicySticky
	scheduler.Reservations = make(map[string]Reservation)
	projection := persistedV2Projection{
		Version: 2, Accounts: slices.Clone(s.accounts), ThreadOwner: owners,
		ThreadRoutes: routes, Scheduler: scheduler, Health: cloneMap(s.health),
		Attempts: make(map[string]TurnAttempt), Handoffs: make(map[string]Handoff),
		Checkpoints: cloneMap(s.checkpoints), Decisions: slices.Clone(s.decisions),
		Capabilities: cloneMap(s.capabilities),
	}
	data, err := json.MarshalIndent(projection, "", "  ")
	if err != nil {
		return fmt.Errorf("encode v2 rollback projection: %w", err)
	}
	data = append(data, '\n')
	path := s.path + ".v2.rollback"
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write v2 rollback projection: %w", err)
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		return fmt.Errorf("secure v2 rollback projection: %w", err)
	}
	if err := renameStateFile(temporary, path); err != nil {
		return fmt.Errorf("commit v2 rollback projection: %w", err)
	}
	digest := sha256.Sum256(data)
	manifest := map[string]any{
		"format": "codex-relay-v2-rollback", "sourceVersion": stateVersion,
		"sourcePoolRevision": s.pool.Revision,
		"createdAt":          time.Now().UnixMilli(), "sha256": hex.EncodeToString(digest[:]),
		"activeTasksRequireRecovery": activeTaskCount(s.tasks),
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode v2 rollback manifest: %w", err)
	}
	manifestPath := path + ".manifest.json"
	manifestTemporary := manifestPath + ".tmp"
	if err := os.WriteFile(manifestTemporary, append(manifestData, '\n'), 0o600); err != nil {
		return fmt.Errorf("write v2 rollback manifest: %w", err)
	}
	if err := renameStateFile(manifestTemporary, manifestPath); err != nil {
		return fmt.Errorf("commit v2 rollback manifest: %w", err)
	}
	return nil
}

func activeTaskCount(tasks map[string]TaskRecord) int {
	count := 0
	for _, task := range tasks {
		if task.ActiveLeaseID != "" || task.RecoveryState != "" {
			count++
		}
	}
	return count
}

func writeVersionedMigrationBackup(path string, data []byte, version int) error {
	backup := fmt.Sprintf("%s.v%d.backup", path, version)
	if _, err := os.Stat(backup); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect state backup: %w", err)
	}
	if err := os.WriteFile(backup, data, 0o600); err != nil {
		return fmt.Errorf("backup v%d state: %w", version, err)
	}
	if err := os.Chmod(backup, 0o600); err != nil {
		return fmt.Errorf("secure v%d state backup: %w", version, err)
	}
	return nil
}

// renameStateFile retries short Windows sharing/access races (for example,
// Defender or the desktop renderer briefly opening state.json). The write is
// already complete and the destination is replaced atomically; a bounded
// retry keeps account/thread metadata durable without weakening the failure
// path for persistent errors.
func renameStateFile(temporary, destination string) error {
	const attempts = 8
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		err = os.Rename(temporary, destination)
		if err == nil {
			return nil
		}
		if runtime.GOOS != "windows" {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
	return err
}

func randomID() (string, error) {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate account ID: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}
