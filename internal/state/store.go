package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const stateVersion = 1

type Account struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	CodexHome  string `json:"codexHome"`
	Enabled    bool   `json:"enabled"`
	Controller bool   `json:"controller"`
	CreatedAt  int64  `json:"createdAt"`
}

type persistedState struct {
	Version     int               `json:"version"`
	Accounts    []Account         `json:"accounts"`
	ThreadOwner map[string]string `json:"threadOwner"`
}

// Store persists only routing metadata. OAuth credentials and conversation
// databases remain inside each account's isolated Codex home.
type Store struct {
	mu               sync.RWMutex
	root             string
	path             string
	primaryCodexHome string
	accounts         []Account
	owners           map[string]string
}

func Open(root, primaryCodexHome string) (*Store, error) {
	if root == "" {
		return nil, errors.New("state root is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create state root: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("secure state root: %w", err)
	}

	store := &Store{
		root:             root,
		path:             filepath.Join(root, "state.json"),
		primaryCodexHome: primaryCodexHome,
		owners:           make(map[string]string),
	}
	data, err := os.ReadFile(store.path)
	switch {
	case err == nil:
		var persisted persistedState
		if err := json.Unmarshal(data, &persisted); err != nil {
			return nil, fmt.Errorf("read state: %w", err)
		}
		if persisted.Version != stateVersion {
			return nil, fmt.Errorf("unsupported state version %d", persisted.Version)
		}
		store.accounts = persisted.Accounts
		if persisted.ThreadOwner != nil {
			store.owners = persisted.ThreadOwner
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
		if err := store.saveLocked(); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("read state: %w", err)
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

// PrimaryCodexHome returns the home owned by the original Codex installation.
// It is used by the mux cleanup path to ensure account management can never
// remove the user's native Codex credentials or conversation database.
func (s *Store) PrimaryCodexHome() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.primaryCodexHome
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
	s.accounts = append(s.accounts, account)
	if err := s.saveLocked(); err != nil {
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
		if err := s.saveLocked(); err != nil {
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
		if account.Controller {
			return Account{}, errors.New("the primary account cannot be discarded")
		}
		for threadID, ownerID := range s.owners {
			if ownerID == id {
				return Account{}, fmt.Errorf(
					"account %q owns thread %q and cannot be discarded", id, threadID,
				)
			}
		}

		previous := s.accounts
		s.accounts = append(slices.Clone(s.accounts[:index]), s.accounts[index+1:]...)
		if err := s.saveLocked(); err != nil {
			s.accounts = previous
			return Account{}, err
		}
		return account, nil
	}
	return Account{}, fmt.Errorf("account %q not found", id)
}

// RemoveAccount removes an account from persistent routing state. Removing a
// Primary account is intentionally forbidden; callers must select another
// Primary first so a concurrent new chat can never be routed through an
// ambiguous controller. Thread ownership is also protected by default and
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
		s.accounts = append(slices.Clone(s.accounts[:index]), s.accounts[index+1:]...)
		if force {
			for _, threadID := range owned {
				delete(s.owners, threadID)
			}
		}
		if err := s.saveLocked(); err != nil {
			s.accounts = previousAccounts
			s.owners = previousOwners
			return Account{}, err
		}
		return account, nil
	}
	return Account{}, fmt.Errorf("account %q not found", id)
}

func (s *Store) ThreadOwner(threadID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
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
		return nil
	}
	s.owners[threadID] = accountID
	return s.saveLocked()
}

func (s *Store) ThreadCounts() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	counts := make(map[string]int)
	for _, accountID := range s.owners {
		counts[accountID]++
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
	for threadID, ownerID := range s.owners {
		if ownerID == accountID {
			result = append(result, threadID)
		}
	}
	return result
}

func (s *Store) saveLocked() error {
	persisted := persistedState{
		Version:     stateVersion,
		Accounts:    s.accounts,
		ThreadOwner: s.owners,
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
	if err := os.Rename(temporary, s.path); err != nil {
		return fmt.Errorf("commit state: %w", err)
	}
	return nil
}

func randomID() (string, error) {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate account ID: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}
