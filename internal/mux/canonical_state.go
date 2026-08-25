package mux

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LightHaru/codex-relay/internal/state"
)

// canonicalGoalCheckpoint deliberately leaves unavailable product semantics
// as null. Relay never promotes an inferred LLM summary above the app-server's
// persisted event/tool ledger.
type canonicalGoalCheckpoint struct {
	GoalID                 *string  `json:"goalId"`
	ThreadID               string   `json:"threadId"`
	Title                  *string  `json:"title"`
	Objective              *string  `json:"objective"`
	CurrentStep            *int     `json:"currentStep"`
	TotalSteps             *int     `json:"totalSteps"`
	CompletedSteps         []string `json:"completedSteps"`
	ActiveStep             *string  `json:"activeStep"`
	PendingSteps           []string `json:"pendingSteps"`
	LastCompletedTurnID    *string  `json:"lastCompletedTurnId"`
	WorkspacePath          *string  `json:"workspacePath"`
	WorkspaceRevision      *string  `json:"workspaceRevision"`
	DirtyFilesBeforeTurn   []string `json:"dirtyFilesBeforeTurn"`
	ChangedFilesDuringTurn []string `json:"changedFilesDuringTurn"`
	CommandsCompleted      []string `json:"commandsCompleted"`
	CommandsPending        []string `json:"commandsPending"`
	ApprovalsPending       []string `json:"approvalsPending"`
	Blockers               []string `json:"blockers"`
	ContinuationSummary    *string  `json:"continuationSummary"`
	CreatedAt              int64    `json:"createdAt"`
	RouteGeneration        uint64   `json:"routeGeneration"`
	HistorySHA256          string   `json:"historySha256,omitempty"`
	HistorySize            int64    `json:"historySize,omitempty"`
}

func requestHash(method string, params []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(method))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(params)
	return hex.EncodeToString(hash.Sum(nil))
}

func (m *Multiplexer) canonicalThreadDirectory(threadID string) string {
	trimmed := strings.TrimSpace(threadID)
	safe := trimmed != "" && trimmed != "." && trimmed != ".."
	for _, character := range trimmed {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			continue
		}
		safe = false
		break
	}
	if !safe {
		digest := sha256.Sum256([]byte(trimmed))
		trimmed = "thread-" + hex.EncodeToString(digest[:16])
	}
	return filepath.Join(m.store.Root(), "threads", trimmed)
}

func writePrivateJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".relay-state-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		_ = temporary.Close()
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := replaceHistoryFile(temporaryPath, path); err != nil {
		return err
	}
	keep = true
	return nil
}

func appendPrivateJSONL(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

func writePrivateDecisionJSONL(path string, values []state.RoutingDecision) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data := make([]byte, 0, len(values)*256)
	for _, value := range values {
		line, err := json.Marshal(value)
		if err != nil {
			return err
		}
		data = append(data, line...)
		data = append(data, '\n')
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".relay-decisions-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		_ = temporary.Close()
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := replaceHistoryFile(temporaryPath, path); err != nil {
		return err
	}
	keep = true
	return nil
}

func (m *Multiplexer) persistCanonicalSnapshot(threadID string) error {
	threadRoute, ok := m.store.ThreadRoute(threadID)
	if !ok {
		return fmt.Errorf("thread %q has no route", threadID)
	}
	directory := m.canonicalThreadDirectory(threadID)
	if err := writePrivateJSON(filepath.Join(directory, "route.json"), threadRoute); err != nil {
		return err
	}
	checkpoint, _ := m.store.Checkpoint(threadID)
	var lastTurnID *string
	if threadRoute.LastCompletedTurnID != "" {
		value := threadRoute.LastCompletedTurnID
		lastTurnID = &value
	}
	goal := canonicalGoalCheckpoint{
		ThreadID: threadID, LastCompletedTurnID: lastTurnID,
		CreatedAt: time.Now().UnixMilli(), RouteGeneration: threadRoute.Generation,
		HistorySHA256: checkpoint.HistorySHA256, HistorySize: checkpoint.HistorySize,
	}
	if err := writePrivateJSON(filepath.Join(directory, "goal-checkpoint.json"), goal); err != nil {
		return err
	}
	if checkpoint.ThreadID != "" {
		generationDirectory := filepath.Join(directory, "generations", fmt.Sprintf("%06d", checkpoint.Generation))
		if err := writePrivateJSON(filepath.Join(generationDirectory, "checkpoint.json"), checkpoint); err != nil {
			return err
		}
	}
	return nil
}

func (m *Multiplexer) appendCanonicalTurn(attempt state.TurnAttempt) {
	m.canonicalMu.Lock()
	defer m.canonicalMu.Unlock()
	_ = appendPrivateJSONL(filepath.Join(m.canonicalThreadDirectory(attempt.ThreadID), "turn-ledger.jsonl"), attempt)
}

func (m *Multiplexer) appendCanonicalDecision(decision state.RoutingDecision) {
	if decision.ThreadID == "" {
		return
	}
	m.canonicalMu.Lock()
	defer m.canonicalMu.Unlock()
	m.rewriteCanonicalDecisionLedgerLocked(decision.ThreadID)
}

func (m *Multiplexer) rewriteCanonicalDecisionLedgerLocked(threadID string) {
	decisions := m.RoutingDecisions(threadID, 100)
	_ = writePrivateDecisionJSONL(filepath.Join(m.canonicalThreadDirectory(threadID), "routing-decisions.jsonl"), decisions)
}

func (m *Multiplexer) compactCanonicalDecisionLedgers() {
	m.canonicalMu.Lock()
	defer m.canonicalMu.Unlock()
	for _, route := range m.store.ThreadRoutes() {
		m.rewriteCanonicalDecisionLedgerLocked(route.ThreadID)
	}
}

func (m *Multiplexer) persistCanonicalHandoff(handoff state.Handoff) {
	m.canonicalMu.Lock()
	defer m.canonicalMu.Unlock()
	_ = writePrivateJSON(filepath.Join(m.canonicalThreadDirectory(handoff.ThreadID), "migrations", handoff.ID+".json"), handoff)
}
