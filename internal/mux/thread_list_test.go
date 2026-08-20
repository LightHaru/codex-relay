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
