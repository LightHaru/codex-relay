package mux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyThreadHistoryCopiesOnlyTheSessionsRelativeRollout(t *testing.T) {
	root := t.TempDir()
	sourceHome := filepath.Join(root, "primary")
	targetHome := filepath.Join(root, "secondary")
	relative := filepath.Join("2026", "08", "20", "rollout-thread-1.jsonl")
	source := filepath.Join(sourceHome, "sessions", relative)
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	const original = `{"type":"session_meta","id":"thread-1"}` + "\n"
	if err := os.WriteFile(source, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	// An existing stale copy must be replaced atomically only after the new
	// source copy has been completely written.
	destination := filepath.Join(targetHome, "sessions", relative)
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := copyThreadHistory(sourceHome, targetHome, source); err != nil {
		t.Fatalf("copyThreadHistory: %v", err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("destination history = %q, want %q", got, original)
	}
	sourceContents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(sourceContents) != original {
		t.Fatalf("source history was modified: %q", sourceContents)
	}
	temporaryFiles, err := filepath.Glob(filepath.Join(filepath.Dir(destination), ".codex-mux-history-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporaryFiles) != 0 {
		t.Fatalf("temporary history files were left behind: %v", temporaryFiles)
	}
}

func TestCopyThreadHistoryRejectsAPathOutsideSessions(t *testing.T) {
	root := t.TempDir()
	sourceHome := filepath.Join(root, "primary")
	targetHome := filepath.Join(root, "secondary")
	outside := filepath.Join(sourceHome, "auth.json")
	if err := os.MkdirAll(sourceHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("not a conversation"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := copyThreadHistory(sourceHome, targetHome, outside)
	if err == nil || !strings.Contains(err.Error(), "outside the source sessions") {
		t.Fatalf("copyThreadHistory outside sessions error = %v", err)
	}
}
