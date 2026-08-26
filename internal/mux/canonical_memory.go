package mux

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/LightHaru/codex-relay/internal/state"
)

type historyMaterialization struct {
	Path        string
	SHA256      string
	Size        int64
	Incremental bool
}

func (m *Multiplexer) checkpointCompletedTurn(threadID, accountID string) {
	account, ok := m.store.Account(accountID)
	if !ok {
		return
	}
	historyHome := account.CodexHome
	path := ""
	// A loaded app-server knows the exact rollout generation it is appending
	// to. Query that path first; a Windows locked-file handoff may leave several
	// valid sibling files with the same thread ID, and the lexical/original file
	// is not necessarily authoritative anymore.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	resumeInfo, resumeErr := m.loadThreadResumeInfo(ctx, threadID, account, account)
	cancel()
	if resumeErr == nil {
		historyHome, path = resumeInfo.historyHome, resumeInfo.historyPath
	} else if fallback, found := findThreadHistory(account.CodexHome, threadID); found {
		path = fallback
	}
	if path == "" {
		return
	}
	canonicalHome := filepath.Join(m.store.Root(), "relay-memory")
	checkpoint, err := syncThreadHistoryIncremental(historyHome, canonicalHome, path)
	if err != nil {
		m.publish(Event{Type: "routing-checkpoint-failed", ThreadID: threadID, AccountID: accountID, Message: err.Error()})
		return
	}
	route, ok := m.store.ThreadRoute(threadID)
	if !ok || route.AccountID != accountID {
		return
	}
	if err := m.store.PutCheckpoint(state.CanonicalCheckpoint{ThreadID: threadID, Generation: route.Generation, HistorySHA256: checkpoint.SHA256, HistorySize: checkpoint.Size, RolloutPath: checkpoint.Path}); err != nil {
		return
	}
	route.HistorySHA256 = checkpoint.SHA256
	route.HistorySize = checkpoint.Size
	route.HistoryGeneration = route.Generation
	if relative, relativeErr := filepath.Rel(canonicalHome, checkpoint.Path); relativeErr == nil && isSafeRelativePath(relative) {
		route.RolloutRelativePath = relative
	}
	if err := m.store.PutThreadRoute(route); err == nil {
		if m.unifiedPoolEnabled() {
			task := m.store.TaskRecords()[threadID]
			task.ThreadID = threadID
			task.CanonicalGeneration = route.Generation
			task.CheckpointSHA256 = checkpoint.SHA256
			task.CheckpointSize = checkpoint.Size
			task.CheckpointPath = checkpoint.Path
			task.LastCompletedTurnID = route.LastCompletedTurnID
			task.ActiveLeaseID = ""
			task.RecoveryState = ""
			task.UpdatedAt = time.Now().UnixMilli()
			_ = m.store.PutTaskRecord(task)
		}
		_ = m.persistCanonicalSnapshot(threadID)
		m.publish(Event{Type: "routing-checkpoint-updated", ThreadID: threadID, AccountID: accountID, RouteGeneration: route.Generation, Data: map[string]any{"historySize": checkpoint.Size, "incremental": checkpoint.Incremental}})
	}
}

// checkpointAndMaterialize first updates Relay's account-neutral canonical
// rollout, then materializes that exact checkpoint into the Relay task
// authority. Credential sources are transport-only and never own task memory.
// Workers therefore never become the authority for the logical conversation.
func (m *Multiplexer) checkpointAndMaterialize(
	threadID, sourceHome, sourcePath, targetHome string,
) (historyMaterialization, error) {
	canonicalHome := filepath.Join(m.store.Root(), "relay-memory")
	checkpoint, err := syncThreadHistoryIncremental(sourceHome, canonicalHome, sourcePath)
	if err != nil {
		return historyMaterialization{}, fmt.Errorf("checkpoint canonical history: %w", err)
	}
	route, _ := m.store.ThreadRoute(threadID)
	if route.Generation == 0 {
		route.Generation = 1
	}
	if err := m.store.PutCheckpoint(state.CanonicalCheckpoint{
		ThreadID:      threadID,
		Generation:    route.Generation,
		HistorySHA256: checkpoint.SHA256,
		HistorySize:   checkpoint.Size,
		RolloutPath:   checkpoint.Path,
	}); err != nil {
		return historyMaterialization{}, fmt.Errorf("persist canonical checkpoint: %w", err)
	}
	materialized, err := syncThreadHistoryIncremental(canonicalHome, targetHome, checkpoint.Path)
	if err != nil {
		return historyMaterialization{}, fmt.Errorf("materialize canonical history: %w", err)
	}
	if err := validateCheckpointMaterialization(checkpoint, materialized); err != nil {
		return historyMaterialization{}, err
	}
	return materialized, nil
}

func validateCheckpointMaterialization(checkpoint, materialized historyMaterialization) error {
	if materialized.SHA256 != checkpoint.SHA256 || materialized.Size != checkpoint.Size {
		return errors.New("materialized history does not match canonical checkpoint")
	}
	return nil
}

// syncThreadHistoryIncremental copies only a verified append-only suffix when
// possible. Any prefix mismatch falls back to a full copy. Both paths write a
// sibling temporary file, fsync it, verify the stable source snapshot, and
// replace the destination atomically.
func syncThreadHistoryIncremental(sourceHome, targetHome, sourcePath string) (historyMaterialization, error) {
	if sourceHome == "" || targetHome == "" || sourcePath == "" {
		return historyMaterialization{}, errors.New("source home, target home, and history path are required")
	}
	sourceHome = normalizeWindowsHistoryPath(sourceHome)
	targetHome = normalizeWindowsHistoryPath(targetHome)
	sourceHome, err := filepath.Abs(sourceHome)
	if err != nil {
		return historyMaterialization{}, fmt.Errorf("resolve source CODEX_HOME: %w", err)
	}
	targetHome, err = filepath.Abs(targetHome)
	if err != nil {
		return historyMaterialization{}, fmt.Errorf("resolve target CODEX_HOME: %w", err)
	}

	historyRoot, sourceFile, err := resolveSourceHistoryPath(sourceHome, sourcePath)
	if err != nil {
		return historyMaterialization{}, err
	}
	if err := rejectReparsePath(historyRoot.path, sourceFile, "source history"); err != nil {
		return historyMaterialization{}, err
	}
	resolvedRoot, err := filepath.EvalSymlinks(historyRoot.path)
	if err != nil {
		return historyMaterialization{}, fmt.Errorf("resolve source history directory: %w", err)
	}
	resolvedSource, err := filepath.EvalSymlinks(sourceFile)
	if err != nil {
		return historyMaterialization{}, fmt.Errorf("resolve existing chat history: %w", err)
	}
	if !isPathWithin(resolvedRoot, resolvedSource) || strings.ToLower(filepath.Ext(resolvedSource)) != ".jsonl" {
		return historyMaterialization{}, errors.New("existing chat history resolves outside the source sessions directory")
	}
	relativePath, err := filepath.Rel(historyRoot.path, sourceFile)
	if err != nil || !isSafeRelativePath(relativePath) {
		return historyMaterialization{}, errors.New("existing chat history has an unsafe sessions-relative path")
	}
	targetRoot := filepath.Join(targetHome, historyRoot.name)
	targetFile := filepath.Join(targetRoot, relativePath)
	if !isPathWithin(targetRoot, targetFile) {
		return historyMaterialization{}, errors.New("target history path escapes the target sessions directory")
	}
	if samePath(resolvedSource, targetFile) {
		hash, size, err := hashRegularFile(resolvedSource)
		return historyMaterialization{Path: resolvedSource, SHA256: hash, Size: size}, err
	}
	if err := os.MkdirAll(filepath.Dir(targetFile), 0o700); err != nil {
		return historyMaterialization{}, fmt.Errorf("create target history directory: %w", err)
	}
	if err := rejectSymlinkTarget(targetRoot, targetFile); err != nil {
		return historyMaterialization{}, err
	}

	for attempt := 0; attempt < 3; attempt++ {
		before, err := os.Stat(resolvedSource)
		if err != nil || !before.Mode().IsRegular() {
			return historyMaterialization{}, fmt.Errorf("inspect existing chat history: %w", err)
		}
		if before.Size() > maxThreadHistoryBytes {
			return historyMaterialization{}, fmt.Errorf("existing chat history is too large to migrate (%d bytes)", before.Size())
		}
		result, err := materializeStableSnapshot(resolvedSource, targetFile, before.Size())
		if err != nil {
			return historyMaterialization{}, err
		}
		after, err := os.Stat(resolvedSource)
		if err != nil {
			_ = os.Remove(result.Path)
			return historyMaterialization{}, fmt.Errorf("reinspect existing chat history: %w", err)
		}
		if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			_ = os.Remove(result.Path)
			continue
		}
		installedPath, err := installMaterializedHistory(result.Path, targetFile, replaceHistoryFile)
		if err != nil {
			_ = os.Remove(result.Path)
			return historyMaterialization{}, fmt.Errorf("install materialized history: %w", err)
		}
		result.Path = installedPath
		return result, nil
	}
	return historyMaterialization{}, errors.New("rollout did not reach a stable checkpoint")
}

// installMaterializedHistory keeps the normal atomic replacement path, but a
// completed Windows app-server thread can retain a non-delete-sharing handle
// to its old rollout. In that case replacing the pathname fails with access
// denied even though a new verified generation is ready. Install that immutable
// generation at a unique sibling rollout instead; thread/resume receives this
// exact path and the post-resume hash verifier remains authoritative.
func installMaterializedHistory(
	source, destination string,
	replace func(string, string) error,
) (string, error) {
	if replace == nil {
		replace = replaceHistoryFile
	}
	if err := replace(source, destination); err == nil {
		return destination, nil
	} else if runtime.GOOS != "windows" && !errors.Is(err, fs.ErrPermission) {
		return "", err
	} else {
		initialErr := err
		for attempt := 0; attempt < 16; attempt++ {
			alternate := nextHistoryReplicaPath(destination, attempt)
			if _, statErr := os.Lstat(alternate); statErr == nil {
				continue
			} else if !os.IsNotExist(statErr) {
				return "", errors.Join(initialErr, statErr)
			}
			if alternateErr := replace(source, alternate); alternateErr == nil {
				return alternate, nil
			} else if !errors.Is(alternateErr, fs.ErrExist) {
				return "", errors.Join(initialErr, alternateErr)
			}
		}
		return "", fmt.Errorf("%w: could not allocate a replacement rollout generation", initialErr)
	}
}

func nextHistoryReplicaPath(destination string, attempt int) string {
	directory := filepath.Dir(destination)
	extension := filepath.Ext(destination)
	stem := strings.TrimSuffix(filepath.Base(destination), extension)
	tail := strings.TrimPrefix(stem, "rollout-")
	name := fmt.Sprintf("rollout-relay-%d-%02d-%s%s", time.Now().UnixNano(), attempt, tail, extension)
	return filepath.Join(directory, name)
}

func materializeStableSnapshot(sourceFile, targetFile string, sourceSize int64) (historyMaterialization, error) {
	input, err := os.Open(sourceFile)
	if err != nil {
		return historyMaterialization{}, fmt.Errorf("open existing chat history: %w", err)
	}
	defer input.Close()
	temporary, err := os.CreateTemp(filepath.Dir(targetFile), ".codex-relay-materialize-*")
	if err != nil {
		return historyMaterialization{}, fmt.Errorf("create temporary chat history: %w", err)
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
		return historyMaterialization{}, fmt.Errorf("secure temporary chat history: %w", err)
	}

	prefixSize := int64(0)
	incremental := false
	if target, err := os.Open(targetFile); err == nil {
		if info, statErr := target.Stat(); statErr == nil && info.Mode().IsRegular() && info.Size() > 0 && info.Size() <= sourceSize {
			matches, compareErr := equalFilePrefix(input, target, info.Size())
			if compareErr != nil {
				return historyMaterialization{}, compareErr
			}
			if matches {
				prefixSize = info.Size()
				incremental = prefixSize < sourceSize
				if _, err := target.Seek(0, io.SeekStart); err != nil {
					return historyMaterialization{}, err
				}
				if _, err := io.CopyN(temporary, target, prefixSize); err != nil {
					return historyMaterialization{}, fmt.Errorf("copy verified history prefix: %w", err)
				}
			}
		}
		_ = target.Close()
	}
	if _, err := input.Seek(prefixSize, io.SeekStart); err != nil {
		return historyMaterialization{}, fmt.Errorf("seek source history: %w", err)
	}
	if _, err := io.CopyN(temporary, input, sourceSize-prefixSize); err != nil {
		return historyMaterialization{}, fmt.Errorf("copy history snapshot: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return historyMaterialization{}, fmt.Errorf("flush materialized history: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return historyMaterialization{}, fmt.Errorf("close materialized history: %w", err)
	}
	hash, size, err := hashRegularFile(temporaryPath)
	if err != nil {
		return historyMaterialization{}, err
	}
	keep = true
	return historyMaterialization{Path: temporaryPath, SHA256: hash, Size: size, Incremental: incremental}, nil
}

func equalFilePrefix(source, target *os.File, count int64) (bool, error) {
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	if _, err := target.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	sourceHash, targetHash := sha256.New(), sha256.New()
	if _, err := io.CopyN(sourceHash, source, count); err != nil {
		return false, fmt.Errorf("hash source prefix: %w", err)
	}
	if _, err := io.CopyN(targetHash, target, count); err != nil {
		return false, fmt.Errorf("hash target prefix: %w", err)
	}
	return string(sourceHash.Sum(nil)) == string(targetHash.Sum(nil)), nil
}

func hashRegularFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("history is not a regular file: %w", err)
	}
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maxThreadHistoryBytes+1))
	if err != nil {
		return "", 0, err
	}
	if written > maxThreadHistoryBytes {
		return "", 0, errors.New("history exceeded the migration limit")
	}
	return hex.EncodeToString(hash.Sum(nil)), written, nil
}

func rejectSymlinkTarget(root, target string) error {
	if err := rejectReparsePath(root, target, "target history"); err != nil {
		return err
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve target history directory: %w", err)
	}
	existingParent := filepath.Dir(target)
	for {
		if _, statErr := os.Lstat(existingParent); statErr == nil {
			break
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("inspect target history directory: %w", statErr)
		}
		next := filepath.Dir(existingParent)
		if next == existingParent || (!isPathWithin(root, next) && !samePath(root, next)) {
			return errors.New("target history parent escapes the target sessions directory")
		}
		existingParent = next
	}
	resolvedParent, err := filepath.EvalSymlinks(existingParent)
	if err != nil {
		return fmt.Errorf("resolve target history parent: %w", err)
	}
	remainder, err := filepath.Rel(existingParent, target)
	if err != nil || !isSafeRelativePath(remainder) {
		return errors.New("target history has an unsafe relative path")
	}
	resolvedTarget := filepath.Join(resolvedParent, remainder)
	if !isPathWithin(resolvedRoot, resolvedTarget) {
		return errors.New("target history directory contains a symbolic link or junction escape")
	}
	for path := filepath.Dir(target); isPathWithin(root, path) || samePath(root, path); path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err == nil && (info.Mode()&os.ModeSymlink != 0 || isPlatformReparsePoint(info)) {
			return errors.New("target history directory contains a symbolic link or junction")
		}
		if samePath(root, path) {
			break
		}
	}
	if info, err := os.Lstat(target); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return errors.New("target history is not a regular file")
	}
	return nil
}

func rejectReparsePath(root, target, label string) error {
	for path := target; isPathWithin(root, path) || samePath(root, path); path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err == nil && (info.Mode()&os.ModeSymlink != 0 || isPlatformReparsePoint(info)) {
			return fmt.Errorf("%s contains a symbolic link or junction escape", label)
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("inspect %s path: %w", label, err)
		}
		if samePath(root, path) {
			break
		}
	}
	return nil
}

func replaceHistoryFile(source, destination string) error {
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		err = os.Rename(source, destination)
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
