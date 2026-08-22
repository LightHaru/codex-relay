package mux

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/LightHaru/codex-relay/internal/backend"
	"github.com/LightHaru/codex-relay/internal/state"
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

func TestCopyThreadHistoryFallsBackToLegacyNativeHome(t *testing.T) {
	root := t.TempDir()
	sourceHome := filepath.Join(root, "subscription")
	legacyHome := filepath.Join(root, "native")
	targetHome := filepath.Join(root, "target")
	relative := filepath.Join("2026", "08", "22", "rollout-legacy-thread.jsonl")
	source := filepath.Join(legacyHome, "sessions", relative)
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	const original = `{"type":"session_meta","id":"legacy-thread"}` + "\n"
	if err := os.WriteFile(source, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := copyThreadHistoryWithLegacyFallback(sourceHome, targetHome, source, legacyHome); err != nil {
		t.Fatalf("copyThreadHistoryWithLegacyFallback: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(targetHome, "sessions", relative))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("destination legacy history = %q, want %q", got, original)
	}
	if _, err := os.Stat(filepath.Join(sourceHome, "sessions", relative)); !os.IsNotExist(err) {
		t.Fatalf("unexpected history was created in the subscription source: %v", err)
	}
}

func TestEnsureThreadHistoryOnAccountMigratesLegacyRollout(t *testing.T) {
	root := t.TempDir()
	primaryHome := filepath.Join(root, "relay-primary")
	legacyHome := filepath.Join(root, "native-store")
	store, err := state.OpenIsolated(filepath.Join(root, "mux"), primaryHome, legacyHome)
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	relative := filepath.Join("2026", "08", "22", "rollout-legacy-thread.jsonl")
	legacyPath := filepath.Join(legacyHome, "sessions", relative)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	const original = `{"type":"session_meta","id":"legacy-thread"}` + "\n"
	if err := os.WriteFile(legacyPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	multiplexer := &Multiplexer{store: store}
	if err := multiplexer.ensureThreadHistoryOnAccount("legacy-thread", account.ID); err != nil {
		t.Fatalf("ensureThreadHistoryOnAccount: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(account.CodexHome, "sessions", relative))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("migrated history = %q, want %q", got, original)
	}
}

func TestLoadThreadResumeInfoFallsBackWhenSourceChildHasNoLoadedThread(t *testing.T) {
	root := t.TempDir()
	legacyHome := filepath.Join(root, "native-store")
	store, err := state.OpenIsolated(filepath.Join(root, "mux"), filepath.Join(root, "relay-primary"), legacyHome)
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.AddAccount("Subscription 3")
	if err != nil {
		t.Fatal(err)
	}
	threadID := "legacy-unloaded-thread"
	relative := filepath.Join("2026", "08", "22", "rollout-legacy-unloaded-thread.jsonl")
	legacyPath := filepath.Join(legacyHome, "sessions", relative)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{"type":"session_meta","id":"`+threadID+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// There is deliberately no source child: this is the same observable
	// condition as an isolated app-server replying "thread not loaded". The
	// read-only native rollout must still be discoverable and resumable.
	multiplexer := &Multiplexer{store: store, children: make(map[string]*backend.Child)}
	info, err := multiplexer.loadThreadResumeInfo(context.Background(), threadID, source, target)
	if err != nil {
		t.Fatalf("loadThreadResumeInfo: %v", err)
	}
	if !samePath(info.historyHome, legacyHome) {
		t.Fatalf("history home = %q, want %q", info.historyHome, legacyHome)
	}
	if filepath.Clean(info.historyPath) != filepath.Clean(legacyPath) {
		t.Fatalf("history path = %q, want %q", info.historyPath, legacyPath)
	}
}

func TestCopyThreadHistoryResolvesCodexRelativeSessionsPath(t *testing.T) {
	root := t.TempDir()
	sourceHome := filepath.Join(root, "primary")
	targetHome := filepath.Join(root, "secondary")
	relative := filepath.Join("2026", "08", "21", "rollout-relative.jsonl")
	source := filepath.Join(sourceHome, "sessions", relative)
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	const original = `{"type":"session_meta","id":"relative-thread"}` + "\n"
	if err := os.WriteFile(source, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	// Newer app-server versions may return either "sessions/<path>" or the
	// bare date-relative path instead of an absolute Windows path.
	for _, historyPath := range []string{
		filepath.Join("sessions", relative),
		relative,
	} {
		if err := copyThreadHistory(sourceHome, targetHome, historyPath); err != nil {
			t.Fatalf("copyThreadHistory(%q): %v", historyPath, err)
		}
		got, err := os.ReadFile(filepath.Join(targetHome, "sessions", relative))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != original {
			t.Fatalf("destination history = %q, want %q", got, original)
		}
	}
}

func TestCopyThreadHistoryCopiesArchivedRolloutToArchivedSessions(t *testing.T) {
	root := t.TempDir()
	sourceHome := filepath.Join(root, "primary")
	targetHome := filepath.Join(root, "secondary")
	name := "rollout-archived-thread.jsonl"
	source := filepath.Join(sourceHome, "archived_sessions", name)
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	const original = `{"type":"session_meta","id":"archived-thread"}` + "\n"
	if err := os.WriteFile(source, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := copyThreadHistory(sourceHome, targetHome, filepath.Join("archived_sessions", name)); err != nil {
		t.Fatalf("copyThreadHistory archived rollout: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(targetHome, "archived_sessions", name))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("archived destination history = %q, want %q", got, original)
	}
}

func TestCopyThreadHistoryResolvesWindowsExtendedLengthPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows extended-length paths are only emitted on Windows")
	}

	root := t.TempDir()
	sourceHome := filepath.Join(root, "primary")
	targetHome := filepath.Join(root, "secondary")
	relative := filepath.Join("2026", "08", "21", "rollout-extended.jsonl")
	source := filepath.Join(sourceHome, "sessions", relative)
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	const original = `{"type":"session_meta","id":"extended-thread"}` + "\n"
	if err := os.WriteFile(source, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	extended := `\\?\` + source
	if err := copyThreadHistory(sourceHome, targetHome, extended); err != nil {
		t.Fatalf("copyThreadHistory extended path: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(targetHome, "sessions", relative))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("destination history = %q, want %q", got, original)
	}
}
