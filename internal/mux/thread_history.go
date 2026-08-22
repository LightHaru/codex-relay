package mux

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// maxThreadHistoryBytes puts a clear upper bound on a single on-demand
// migration. It is deliberately large enough for lengthy local Codex chats,
// while protecting the isolated account home from an accidental multi-gigabyte
// copy caused by a malformed app-server response.
const maxThreadHistoryBytes int64 = 1 << 30

// copyThreadHistory mirrors precisely one local rollout file from the source
// subscription into the target subscription's isolated CODEX_HOME. The
// Windows app-server currently resumes a persisted thread by threadId only;
// it does not accept the path/history fields that newer app-server versions
// expose. Giving the target its own copy lets it resume the same thread ID
// without sharing or modifying the source account's history file.
func copyThreadHistory(sourceHome, targetHome, sourcePath string) error {
	if sourceHome == "" || targetHome == "" || sourcePath == "" {
		return errors.New("source home, target home, and history path are required")
	}

	sourceHome = normalizeWindowsHistoryPath(sourceHome)
	targetHome = normalizeWindowsHistoryPath(targetHome)
	sourceHome, err := filepath.Abs(sourceHome)
	if err != nil {
		return fmt.Errorf("resolve source CODEX_HOME: %w", err)
	}
	targetHome, err = filepath.Abs(targetHome)
	if err != nil {
		return fmt.Errorf("resolve target CODEX_HOME: %w", err)
	}

	historyRoot, sourceFile, err := resolveSourceHistoryPath(sourceHome, sourcePath)
	if err != nil {
		return err
	}
	if !isPathWithin(historyRoot.path, sourceFile) {
		return errors.New("existing chat history is outside the source sessions directory")
	}
	if strings.ToLower(filepath.Ext(sourceFile)) != ".jsonl" {
		return errors.New("existing chat history is not a rollout JSONL file")
	}

	resolvedRoot, err := filepath.EvalSymlinks(historyRoot.path)
	if err != nil {
		return fmt.Errorf("resolve source history directory: %w", err)
	}
	resolvedSource, err := filepath.EvalSymlinks(sourceFile)
	if err != nil {
		return fmt.Errorf("resolve existing chat history: %w", err)
	}
	if !isPathWithin(resolvedRoot, resolvedSource) {
		return errors.New("existing chat history resolves outside the source sessions directory")
	}
	info, err := os.Stat(resolvedSource)
	if err != nil {
		return fmt.Errorf("inspect existing chat history: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("existing chat history is not a regular file")
	}
	if info.Size() > maxThreadHistoryBytes {
		return fmt.Errorf("existing chat history is too large to migrate (%d bytes)", info.Size())
	}

	relativePath, err := filepath.Rel(historyRoot.path, sourceFile)
	if err != nil || relativePath == "." || !isSafeRelativePath(relativePath) {
		return errors.New("existing chat history has an unsafe sessions-relative path")
	}
	targetRoot, err := filepath.Abs(filepath.Join(targetHome, historyRoot.name))
	if err != nil {
		return fmt.Errorf("resolve target history directory: %w", err)
	}
	targetFile := filepath.Join(targetRoot, relativePath)
	if !isPathWithin(targetRoot, targetFile) {
		return errors.New("target history path escapes the target sessions directory")
	}
	if filepath.Clean(resolvedSource) == filepath.Clean(targetFile) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(targetFile), 0o700); err != nil {
		return fmt.Errorf("create target history directory: %w", err)
	}

	input, err := os.Open(resolvedSource)
	if err != nil {
		return fmt.Errorf("open existing chat history: %w", err)
	}
	defer input.Close()

	temporary, err := os.CreateTemp(filepath.Dir(targetFile), ".codex-mux-history-*")
	if err != nil {
		return fmt.Errorf("create temporary chat history: %w", err)
	}
	temporaryPath := temporary.Name()
	completed := false
	defer func() {
		if !completed {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary chat history: %w", err)
	}
	written, err := io.Copy(temporary, io.LimitReader(input, maxThreadHistoryBytes+1))
	if err != nil {
		return fmt.Errorf("copy existing chat history: %w", err)
	}
	if written > maxThreadHistoryBytes {
		return fmt.Errorf("existing chat history exceeded the migration limit (%d bytes)", maxThreadHistoryBytes)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("flush copied chat history: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close copied chat history: %w", err)
	}
	// os.Rename replaces an existing regular destination atomically on the
	// supported platforms. The old file remains untouched if the copy or rename
	// fails, which is important when a subscription was used for this chat
	// before a later failover.
	if err := os.Rename(temporaryPath, targetFile); err != nil {
		return fmt.Errorf("install copied chat history: %w", err)
	}
	completed = true
	return nil
}

// copyThreadHistoryWithLegacyFallback keeps old chats usable after Relay is
// separated from the Store Codex home. Older Router versions sometimes left a
// thread's SQLite rollout_path pointing at the former native CODEX_HOME even
// though the thread is now owned by a Relay subscription. The native home is
// accepted only as a read-only history source; credentials and config are
// never opened, copied, or used to start a child process.
func copyThreadHistoryWithLegacyFallback(
	sourceHome, targetHome, sourcePath, legacyHome string,
) error {
	err := copyThreadHistory(sourceHome, targetHome, sourcePath)
	if err == nil || strings.TrimSpace(legacyHome) == "" || samePath(sourceHome, legacyHome) {
		return err
	}
	if fallbackErr := copyThreadHistory(legacyHome, targetHome, sourcePath); fallbackErr == nil {
		return nil
	}
	return err
}

type historyRoot struct {
	name string
	path string
}

// resolveSourceHistoryPath accepts the path formats emitted by different
// Codex app-server versions. Older versions return an absolute path under
// sessions, while newer versions may return a CODEX_HOME-relative path (or a
// path rooted at either sessions or archived_sessions). We resolve all of
// those forms against the source account home, but never accept a path outside
// one of Codex's two managed history directories.
func resolveSourceHistoryPath(sourceHome, sourcePath string) (historyRoot, string, error) {
	roots := []historyRoot{
		{name: "sessions", path: filepath.Join(sourceHome, "sessions")},
		{name: "archived_sessions", path: filepath.Join(sourceHome, "archived_sessions")},
	}
	for index := range roots {
		absolute, err := filepath.Abs(roots[index].path)
		if err != nil {
			return historyRoot{}, "", fmt.Errorf("resolve source history directory: %w", err)
		}
		roots[index].path = absolute
	}

	raw := normalizeWindowsHistoryPath(sourcePath)
	pathCandidates := make([]string, 0, 3)
	if filepath.IsAbs(raw) {
		pathCandidates = append(pathCandidates, raw)
	} else {
		// A CODEX_HOME-relative path is the most common new response shape. The
		// explicit root-relative candidates below also cover a bare date path.
		pathCandidates = append(pathCandidates, filepath.Join(sourceHome, raw))
		for _, root := range roots {
			pathCandidates = append(pathCandidates, filepath.Join(root.path, raw))
		}
	}

	var fallback *struct {
		root historyRoot
		path string
	}
	for _, candidate := range pathCandidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		for _, root := range roots {
			if !isPathWithin(root.path, absolute) {
				continue
			}
			if _, statErr := os.Lstat(absolute); statErr == nil {
				return root, absolute, nil
			}
			if fallback == nil {
				fallback = &struct {
					root historyRoot
					path string
				}{root: root, path: absolute}
			}
		}
	}
	if fallback != nil {
		return fallback.root, fallback.path, nil
	}
	return historyRoot{}, "", errors.New("existing chat history is outside the source sessions directory")
}

// normalizeWindowsHistoryPath removes the extended-length prefixes emitted by
// Rust's Windows path serializer (for example, \\?\C:\...). Go's regular
// filepath helpers treat that spelling as a different volume from C:\..., so
// without this normalization a valid rollout can be rejected as outside the
// source CODEX_HOME. UNC extended paths are converted back to their normal
// \\server\share spelling as well.
func normalizeWindowsHistoryPath(value string) string {
	value = filepath.FromSlash(strings.TrimSpace(value))
	if runtime.GOOS != "windows" {
		return value
	}
	const (
		extendedPrefix = `\\?\`
		uncPrefix      = `\\?\UNC\`
		devicePrefix   = `\\.\`
	)
	switch {
	case strings.HasPrefix(strings.ToUpper(value), strings.ToUpper(uncPrefix)):
		return `\\` + value[len(uncPrefix):]
	case strings.HasPrefix(strings.ToUpper(value), strings.ToUpper(extendedPrefix)):
		return value[len(extendedPrefix):]
	case strings.HasPrefix(strings.ToUpper(value), strings.ToUpper(devicePrefix)):
		return value[len(devicePrefix):]
	default:
		return value
	}
}

// findThreadHistory locates a persisted rollout by its thread ID in one
// account's managed history directories. It is used when an older Router
// state file has no owner mapping for a chat that already exists on disk.
// WalkDir does not follow directory symlinks; copyThreadHistory performs the
// final EvalSymlinks boundary check before reading any file.
func findThreadHistory(codexHome, threadID string) (string, bool) {
	codexHome = normalizeWindowsHistoryPath(codexHome)
	threadID = strings.TrimSpace(threadID)
	if codexHome == "" || threadID == "" || strings.ContainsAny(threadID, `/\\`) {
		return "", false
	}
	wanted := strings.ToLower(threadID)
	for _, rootName := range []string{"sessions", "archived_sessions"} {
		root := filepath.Join(codexHome, rootName)
		var found string
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry == nil || entry.IsDir() {
				return nil
			}
			if strings.ToLower(filepath.Ext(entry.Name())) != ".jsonl" {
				return nil
			}
			base := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			base = strings.ToLower(base)
			if base == wanted || strings.HasSuffix(base, "-"+wanted) {
				found = path
				return fs.SkipAll
			}
			return nil
		})
		if found != "" {
			return found, true
		}
	}
	return "", false
}

func isPathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && isSafeRelativePath(relative)
}

func isSafeRelativePath(relative string) bool {
	return relative != "" && relative != "." && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(relative)
}

func samePath(left, right string) bool {
	left = normalizeWindowsHistoryPath(strings.TrimSpace(left))
	right = normalizeWindowsHistoryPath(strings.TrimSpace(right))
	if left == "" || right == "" {
		return false
	}
	leftAbsolute, leftErr := filepath.Abs(left)
	rightAbsolute, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return filepath.Clean(left) == filepath.Clean(right)
	}
	return filepath.Clean(leftAbsolute) == filepath.Clean(rightAbsolute)
}
