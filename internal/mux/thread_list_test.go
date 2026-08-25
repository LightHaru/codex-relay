package mux

import (
	"path/filepath"
	"testing"

	"github.com/LightHaru/codex-relay/internal/state"
)

func TestChooseThreadCandidateKeepsPersistedFailoverOwner(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetThreadOwner("thread-1", secondary.ID); err != nil {
		t.Fatal(err)
	}
	primary, _ := store.Account("primary")
	got := chooseThreadCandidate(store, "thread-1", []threadCandidate{
		{account: primary, thread: map[string]any{"id": "thread-1", "preview": "primary"}},
		{account: secondary, thread: map[string]any{"id": "thread-1", "preview": "secondary"}},
	})
	if got.account.ID != secondary.ID {
		t.Fatalf("selected account = %q, want persisted owner %q", got.account.ID, secondary.ID)
	}
}

func TestChooseThreadCandidatePrefersPrimaryWithoutAnAssignment(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	primary, _ := store.Account("primary")
	got := chooseThreadCandidate(store, "thread-2", []threadCandidate{
		{account: secondary, thread: map[string]any{"id": "thread-2"}},
		{account: primary, thread: map[string]any{"id": "thread-2"}},
	})
	if got.account.ID != primary.ID {
		t.Fatalf("selected account = %q, want controller %q", got.account.ID, primary.ID)
	}
}

func TestMissingAuthoritativeReplicaDoesNotRewritePersistedOwner(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetThreadOwner("thread-3", secondary.ID); err != nil {
		t.Fatal(err)
	}
	primary, _ := store.Account("primary")
	selected := chooseThreadCandidate(store, "thread-3", []threadCandidate{
		{account: primary, thread: map[string]any{"id": "thread-3"}},
	})
	if selected.account.ID != primary.ID {
		t.Fatalf("visible fallback candidate = %q", selected.account.ID)
	}
	// aggregateThreadList now mutates ownership only for an unassigned task;
	// selecting a visible replica cannot make it authoritative.
	if owner, _ := store.ThreadOwner("thread-3"); owner != secondary.ID {
		t.Fatalf("authoritative owner changed to %q", owner)
	}
}

func TestMergeThreadCandidatesReturnsOneLogicalTaskForManyReplicas(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutThreadRoute(state.ThreadRoute{ThreadID: "thread-shared", AccountID: secondary.ID, Generation: 4, HistoryGeneration: 4}); err != nil {
		t.Fatal(err)
	}
	primary, _ := store.Account("primary")
	threads := mergeThreadCandidates(store, map[string][]threadCandidate{
		"thread-shared": {
			{account: primary, thread: map[string]any{"id": "thread-shared", "preview": "stale replica"}},
			{account: secondary, thread: map[string]any{"id": "thread-shared", "preview": "authoritative replica"}},
		},
	}, nil)
	if len(threads) != 1 {
		t.Fatalf("replicated task appeared %d times: %#v", len(threads), threads)
	}
	if threads[0]["preview"] != "authoritative replica" {
		t.Fatalf("merged task did not use authoritative owner: %#v", threads[0])
	}
	route, _ := store.ThreadRoute("thread-shared")
	if route.AccountID != secondary.ID || route.Generation != 4 || route.HistoryGeneration != 4 {
		t.Fatalf("thread-list merge rewrote route authority: %#v", route)
	}
}
