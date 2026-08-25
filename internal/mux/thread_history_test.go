package mux

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

func TestIncrementalHistoryMaterializationAppendsVerifiedSuffix(t *testing.T) {
	root := t.TempDir()
	sourceHome := filepath.Join(root, "source")
	targetHome := filepath.Join(root, "target")
	relative := filepath.Join("2026", "08", "23", "rollout-thread-incremental.jsonl")
	source := filepath.Join(sourceHome, "sessions", relative)
	target := filepath.Join(targetHome, "sessions", relative)
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	prefix := []byte("{\"type\":\"session_meta\"}\n")
	suffix := []byte("{\"type\":\"turn_context\"}\n")
	if err := os.WriteFile(source, append(append([]byte(nil), prefix...), suffix...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, prefix, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := syncThreadHistoryIncremental(sourceHome, targetHome, source)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Incremental {
		t.Fatal("verified append-only history did not use incremental materialization")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	want := append(append([]byte(nil), prefix...), suffix...)
	if string(got) != string(want) || result.Size != int64(len(want)) || result.SHA256 == "" {
		t.Fatalf("materialized history mismatch: result=%#v got=%q", result, got)
	}
}

func TestIncrementalHistoryMaterializationFallsBackOnPrefixMismatch(t *testing.T) {
	root := t.TempDir()
	sourceHome := filepath.Join(root, "source")
	targetHome := filepath.Join(root, "target")
	relative := filepath.Join("rollout-prefix-mismatch.jsonl")
	source := filepath.Join(sourceHome, "sessions", relative)
	target := filepath.Join(targetHome, "sessions", relative)
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("canonical\nnew\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("corrupt!!\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := syncThreadHistoryIncremental(sourceHome, targetHome, source)
	if err != nil {
		t.Fatal(err)
	}
	if result.Incremental {
		t.Fatal("mismatched prefix was incorrectly accepted")
	}
	got, _ := os.ReadFile(target)
	if string(got) != "canonical\nnew\n" {
		t.Fatalf("full fallback did not repair destination: %q", got)
	}
}

func TestLockedMaterializedDestinationUsesVerifiedSiblingGeneration(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "rollout-2026-08-09-thread-locked.jsonl")
	temporary := filepath.Join(root, ".codex-relay-materialize-test")
	if err := os.WriteFile(destination, []byte("locked-old-generation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(temporary, []byte("verified-new-generation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	installed, err := installMaterializedHistory(temporary, destination, func(source, target string) error {
		calls++
		if calls == 1 {
			return fs.ErrPermission
		}
		return os.Rename(source, target)
	})
	if err != nil {
		t.Fatal(err)
	}
	if samePath(installed, destination) || filepath.Dir(installed) != filepath.Dir(destination) {
		t.Fatalf("locked destination did not use a sibling generation: %q", installed)
	}
	if !strings.HasPrefix(filepath.Base(installed), "rollout-relay-") || !strings.HasSuffix(strings.TrimSuffix(filepath.Base(installed), filepath.Ext(installed)), "-thread-locked") {
		t.Fatalf("alternate rollout name is not discoverable by thread ID: %q", installed)
	}
	got, err := os.ReadFile(installed)
	if err != nil || string(got) != "verified-new-generation\n" {
		t.Fatalf("alternate rollout contents=%q error=%v", got, err)
	}
	old, err := os.ReadFile(destination)
	if err != nil || string(old) != "locked-old-generation\n" {
		t.Fatalf("locked generation was modified: %q error=%v", old, err)
	}
}

func TestFindThreadHistoryChoosesNewestSiblingGeneration(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "sessions", "2026", "08", "09")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	const threadID = "019fe645-f42f-7a20-8b2b-054f46c0af0a"
	stale := filepath.Join(directory, "rollout-2026-08-09T18-26-12-"+threadID+".jsonl")
	active := filepath.Join(directory, "rollout-relay-1787598216922731700-00-2026-08-09T18-26-12-"+threadID+".jsonl")
	if err := os.WriteFile(stale, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(active, []byte("stale\nnew-turn\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Unix(2_000_000_000, 0)
	newTime := oldTime.Add(time.Minute)
	if err := os.Chtimes(stale, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(active, newTime, newTime); err != nil {
		t.Fatal(err)
	}
	found, ok := findThreadHistory(home, threadID)
	if !ok || !samePath(found, active) {
		t.Fatalf("selected history=%q ok=%v, want newest generation %q", found, ok, active)
	}
}

func TestCompletedTurnRefreshesCanonicalRelayCheckpoint(t *testing.T) {
	root := t.TempDir()
	primaryHome := filepath.Join(root, "primary")
	store, err := state.Open(filepath.Join(root, "mux"), primaryHome)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetThreadOwner("thread-checkpoint", "primary"); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(primaryHome, "sessions", "2026", "08", "23", "rollout-thread-checkpoint.jsonl")
	if err := os.MkdirAll(filepath.Dir(rollout), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rollout, []byte("meta\nturn-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	multiplexer := &Multiplexer{store: store, events: make(map[chan Event]struct{})}
	multiplexer.checkpointCompletedTurn("thread-checkpoint", "primary")
	first, ok := store.Checkpoint("thread-checkpoint")
	if !ok || first.HistorySHA256 == "" || first.HistorySize != int64(len("meta\nturn-1\n")) {
		t.Fatalf("first checkpoint = %#v ok=%v", first, ok)
	}
	file, err := os.OpenFile(rollout, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("turn-2\n"); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	multiplexer.checkpointCompletedTurn("thread-checkpoint", "primary")
	second, ok := store.Checkpoint("thread-checkpoint")
	if !ok || second.HistorySize <= first.HistorySize || second.HistorySHA256 == first.HistorySHA256 {
		t.Fatalf("checkpoint was not advanced: first=%#v second=%#v", first, second)
	}
	canonical, err := os.ReadFile(second.RolloutPath)
	if err != nil || string(canonical) != "meta\nturn-1\nturn-2\n" {
		t.Fatalf("canonical memory mismatch: %v %q", err, canonical)
	}
	threadDirectory := multiplexer.canonicalThreadDirectory("thread-checkpoint")
	for _, relative := range []string{
		"route.json",
		"goal-checkpoint.json",
		filepath.Join("generations", "000001", "checkpoint.json"),
	} {
		info, statErr := os.Stat(filepath.Join(threadDirectory, relative))
		if statErr != nil || !info.Mode().IsRegular() {
			t.Fatalf("canonical metadata %s is missing: %v", relative, statErr)
		}
	}
	goalData, err := os.ReadFile(filepath.Join(threadDirectory, "goal-checkpoint.json"))
	if err != nil || !strings.Contains(string(goalData), `"objective": null`) {
		t.Fatalf("unknown goal fields were not preserved as null: %v %s", err, goalData)
	}
}

func TestCheckpointHashMismatchFailsClosed(t *testing.T) {
	checkpoint := historyMaterialization{SHA256: "expected", Size: 12}
	materialized := historyMaterialization{SHA256: "different", Size: 12}
	if err := validateCheckpointMaterialization(checkpoint, materialized); err == nil {
		t.Fatal("hash mismatch was accepted")
	}
	materialized = historyMaterialization{SHA256: "expected", Size: 13}
	if err := validateCheckpointMaterialization(checkpoint, materialized); err == nil {
		t.Fatal("size mismatch was accepted")
	}
}

func TestIncrementalHistoryRejectsOversizedSparseRollout(t *testing.T) {
	root := t.TempDir()
	sourceHome := filepath.Join(root, "source")
	targetHome := filepath.Join(root, "target")
	source := filepath.Join(sourceHome, "sessions", "rollout-large.jsonl")
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxThreadHistoryBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	_ = file.Close()
	if _, err := syncThreadHistoryIncremental(sourceHome, targetHome, source); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized rollout error = %v", err)
	}
}

func TestIncrementalHistoryRejectsSourceSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	sourceHome := filepath.Join(root, "source")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outside, "rollout.jsonl")
	if err := os.WriteFile(outsideFile, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(sourceHome, "sessions", "escape")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	createTestDirectoryLink(t, outside, link)
	if _, err := syncThreadHistoryIncremental(sourceHome, filepath.Join(root, "target"), filepath.Join(link, "rollout.jsonl")); err == nil || (!strings.Contains(err.Error(), "outside") && !strings.Contains(err.Error(), "junction escape")) {
		t.Fatalf("symlink escape error = %v", err)
	}
}

func TestIncrementalHistoryRejectsTargetSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	sourceHome := filepath.Join(root, "source")
	targetHome := filepath.Join(root, "target")
	source := filepath.Join(sourceHome, "sessions", "nested", "rollout.jsonl")
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside-target")
	if err := os.MkdirAll(filepath.Join(targetHome, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	createTestDirectoryLink(t, outside, filepath.Join(targetHome, "sessions", "nested"))
	if _, err := syncThreadHistoryIncremental(sourceHome, targetHome, source); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("target symlink escape error = %v", err)
	}
}

func createTestDirectoryLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err == nil {
		return
	} else if runtime.GOOS != "windows" {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	// Windows directory junctions exercise the same reparse-point escape
	// boundary without requiring Developer Mode or SeCreateSymbolicLinkPrivilege.
	if strings.ContainsAny(target+link, " \t\"&|<>^") {
		t.Skip("temporary junction path contains unsupported cmd metacharacters")
	}
	commandLine := `mklink /J ` + link + ` ` + target
	command := exec.Command("cmd.exe", "/d", "/c", commandLine)
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("symlink and junction creation are unavailable: %v: %s", err, strings.TrimSpace(string(output)))
	}
	if _, err := os.Stat(link); err != nil {
		t.Fatalf("created junction is not traversable: %v", err)
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

	multiplexer := &Multiplexer{store: store, compatibilityProfile: "fixture-reviewed-v2", safeHandoff: true}
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

func TestEnsureThreadHistoryOnAccountUnknownProfileFailsClosed(t *testing.T) {
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
	relative := filepath.Join("2026", "08", "22", "rollout-unknown-profile.jsonl")
	legacyPath := filepath.Join(legacyHome, "sessions", relative)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	multiplexer := &Multiplexer{store: store, compatibilityProfile: "unknown"}
	err = multiplexer.ensureThreadHistoryOnAccount("unknown-profile", account.ID)
	if err == nil || !strings.Contains(err.Error(), "remains Sticky") {
		t.Fatalf("unknown profile error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(account.CodexHome, "sessions", relative)); !os.IsNotExist(statErr) {
		t.Fatalf("unknown profile created a target replica: %v", statErr)
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

func TestLoadThreadResumeInfoReportsNoRolloutFound(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	source, _ := store.Account("primary")
	multiplexer := &Multiplexer{store: store, children: make(map[string]*backend.Child)}
	_, err = multiplexer.loadThreadResumeInfo(context.Background(), "missing-thread", source, target)
	if err == nil || !strings.Contains(err.Error(), "no rollout found for thread id missing-thread") {
		t.Fatalf("missing rollout error = %v", err)
	}
}

func TestCanonicalCheckpointSelectsVerifiedReplicaOnly(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	target, _ := store.AddAccount("Subscription 2")
	verified, _ := store.AddAccount("Subscription 3")
	source, _ := store.Account("primary")
	relative := filepath.Join("2026", "08", "23", "rollout-replica-thread.jsonl")
	writeReplica := func(home, contents string) string {
		path := filepath.Join(home, "sessions", relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	writeReplica(target.CodexHome, "corrupt replica\n")
	verifiedPath := writeReplica(verified.CodexHome, "canonical replica\n")
	hash, size, err := hashRegularFile(verifiedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutCheckpoint(state.CanonicalCheckpoint{ThreadID: "replica-thread", Generation: 7, HistorySHA256: hash, HistorySize: size, RolloutPath: "canonical-reference"}); err != nil {
		t.Fatal(err)
	}
	multiplexer := &Multiplexer{store: store, children: make(map[string]*backend.Child)}
	info, err := multiplexer.loadThreadResumeInfo(context.Background(), "replica-thread", source, target)
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(info.historyHome, verified.CodexHome) || !samePath(info.historyPath, verifiedPath) {
		t.Fatalf("selected unverified replica: %#v", info)
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
