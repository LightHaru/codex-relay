package mux

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

	sourceSessions, err := filepath.Abs(filepath.Join(sourceHome, "sessions"))
	if err != nil {
		return fmt.Errorf("resolve source sessions directory: %w", err)
	}
	sourceFile, err := filepath.Abs(sourcePath)
	if err != nil {
		return fmt.Errorf("resolve source history path: %w", err)
	}
	if !isPathWithin(sourceSessions, sourceFile) {
		return errors.New("existing chat history is outside the source sessions directory")
	}
	if strings.ToLower(filepath.Ext(sourceFile)) != ".jsonl" {
		return errors.New("existing chat history is not a rollout JSONL file")
	}

	resolvedSessions, err := filepath.EvalSymlinks(sourceSessions)
	if err != nil {
		return fmt.Errorf("resolve source sessions directory: %w", err)
	}
	resolvedSource, err := filepath.EvalSymlinks(sourceFile)
	if err != nil {
		return fmt.Errorf("resolve existing chat history: %w", err)
	}
	if !isPathWithin(resolvedSessions, resolvedSource) {
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

	relativePath, err := filepath.Rel(sourceSessions, sourceFile)
	if err != nil || relativePath == "." || !isSafeRelativePath(relativePath) {
		return errors.New("existing chat history has an unsafe sessions-relative path")
	}
	targetSessions, err := filepath.Abs(filepath.Join(targetHome, "sessions"))
	if err != nil {
		return fmt.Errorf("resolve target sessions directory: %w", err)
	}
	targetFile := filepath.Join(targetSessions, relativePath)
	if !isPathWithin(targetSessions, targetFile) {
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

func isPathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && isSafeRelativePath(relative)
}

func isSafeRelativePath(relative string) bool {
	return relative != "" && relative != "." && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(relative)
}
