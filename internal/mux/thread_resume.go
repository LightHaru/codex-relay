package mux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/LightHaru/codex-relay/internal/state"
)

// threadResumeInfo contains only the metadata needed to resume a local
// rollout on another isolated subscription. The history path is always
// resolved to one of the managed sessions directories before it is copied;
// credentials and configuration are never part of this structure.
type threadResumeInfo struct {
	historyHome   string
	historyPath   string
	cwd           string
	modelProvider string
}

// loadThreadResumeInfo first asks the source child for the rollout path, as
// that is the most precise source on app-server versions that have the thread
// loaded. Older or newly isolated children can legitimately answer
// "thread not loaded" while the same JSONL still exists in the former native
// CODEX_HOME. In that case, locate the read-only rollout on disk instead of
// surfacing a false failover error.
func (m *Multiplexer) loadThreadResumeInfo(
	ctx context.Context,
	threadID string,
	sourceAccount state.Account,
	targetAccount state.Account,
) (threadResumeInfo, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return threadResumeInfo{}, errors.New("thread id is required")
	}

	var readErr error
	if source, ok := m.child(sourceAccount.ID); ok {
		readParams, _ := json.Marshal(map[string]any{
			"threadId":     threadID,
			"includeTurns": true,
		})
		readResponse, err := source.Request(ctx, "thread/read", readParams)
		if err != nil {
			readErr = err
		} else {
			var decoded struct {
				Thread struct {
					ID            string `json:"id"`
					Path          string `json:"path"`
					CWD           string `json:"cwd"`
					ModelProvider string `json:"modelProvider"`
				} `json:"thread"`
			}
			if err := json.Unmarshal(readResponse.Result, &decoded); err != nil {
				readErr = fmt.Errorf("decode existing chat: %w", err)
			} else if decoded.Thread.ID == "" || decoded.Thread.Path == "" {
				readErr = errors.New("existing chat has no resumable history path")
			} else if home, path, ok := resolveManagedHistoryPath(decoded.Thread.Path, resumeHistoryHomes(sourceAccount, targetAccount, m.store)); ok {
				return threadResumeInfo{
					historyHome:   home,
					historyPath:   path,
					cwd:           decoded.Thread.CWD,
					modelProvider: decoded.Thread.ModelProvider,
				}, nil
			} else {
				readErr = errors.New("existing chat history is outside managed sessions directories")
			}
		}
	} else {
		readErr = errors.New("source subscription is unavailable")
	}

	// A source child may not have loaded a legacy thread even though its
	// SQLite row still points to it. Search only the source/target Relay homes,
	// the read-only native migration home, and other managed account homes.
	for _, home := range resumeHistoryHomes(sourceAccount, targetAccount, m.store) {
		if path, found := findThreadHistory(home, threadID); found {
			return threadResumeInfo{historyHome: home, historyPath: path}, nil
		}
	}
	if readErr != nil {
		return threadResumeInfo{}, readErr
	}
	return threadResumeInfo{}, errors.New("existing chat has no resumable history")
}

// resumeHistoryHomes returns deterministic, de-duplicated search roots. The
// source and target are considered first, then the old native home, then the
// remaining isolated subscriptions. This allows a stale owner mapping to be
// repaired without ever borrowing another account's credentials.
func resumeHistoryHomes(sourceAccount, targetAccount state.Account, store *state.Store) []string {
	result := make([]string, 0, len(store.Accounts())+3)
	seen := make(map[string]struct{})
	appendHome := func(home string) {
		home = strings.TrimSpace(home)
		if home == "" {
			return
		}
		absolute, err := filepath.Abs(home)
		if err != nil {
			return
		}
		key := strings.ToLower(filepath.Clean(absolute))
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		result = append(result, absolute)
	}
	appendHome(sourceAccount.CodexHome)
	appendHome(targetAccount.CodexHome)
	appendHome(store.LegacyPrimaryCodexHome())
	for _, account := range store.Accounts() {
		if account.Enabled {
			appendHome(account.CodexHome)
		}
	}
	return result
}

// resolveManagedHistoryPath validates an app-server supplied path against the
// explicitly managed history roots. It returns the canonical home and path
// only when the file exists there, preventing an absolute path from escaping
// into credentials, arbitrary user files, or another untrusted directory.
func resolveManagedHistoryPath(path string, homes []string) (string, string, bool) {
	for _, home := range homes {
		_, resolved, err := resolveSourceHistoryPath(home, path)
		if err != nil {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		absoluteHome, err := filepath.Abs(home)
		if err != nil {
			continue
		}
		return absoluteHome, resolved, true
	}
	return "", "", false
}
