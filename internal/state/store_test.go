package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStoreBootstrapsPrimaryAndPersistsThreadAffinity(t *testing.T) {
	root := t.TempDir()
	primaryHome := filepath.Join(root, "primary")
	store, err := Open(filepath.Join(root, "mux"), primaryHome)
	if err != nil {
		t.Fatal(err)
	}
	accounts := store.Accounts()
	if len(accounts) != 1 || accounts[0].ID != "primary" || !accounts[0].Controller {
		t.Fatalf("unexpected bootstrap accounts: %#v", accounts)
	}
	added, err := store.AddAccount("Work")
	if err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(filepath.Join(added.CodexHome, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	wantConfig := "cli_auth_credentials_store = \"file\"\nmcp_oauth_credentials_store = \"file\"\n"
	if runtime.GOOS == "windows" {
		wantConfig += "\n[windows]\nsandbox = \"unelevated\"\n"
	}
	if string(config) != wantConfig {
		t.Fatalf("unexpected isolated config: %q", config)
	}
	if err := store.SetThreadOwner("thread-1", added.ID); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(filepath.Join(root, "mux"), primaryHome)
	if err != nil {
		t.Fatal(err)
	}
	owner, ok := reopened.ThreadOwner("thread-1")
	if !ok || owner != added.ID {
		t.Fatalf("thread affinity was not persisted: owner=%q ok=%v", owner, ok)
	}
}

func TestAccountConfigInheritsManagedMCPAndPreservesLocalProjects(t *testing.T) {
	root := t.TempDir()
	primaryHome := filepath.Join(root, "primary")
	if err := os.MkdirAll(primaryHome, 0o700); err != nil {
		t.Fatal(err)
	}
	primaryConfig := `model = "gpt-test"

[mcp_servers.node_repl]
command = "/Applications/Codex Subscription Router.app/node_repl"

[mcp_servers.node_repl.env]
SKY_CUA_SERVICE_PATH = "/Applications/Codex Subscription Router Computer Use.app"

[projects."/primary-only"]
trust_level = "trusted"
`
	if err := os.WriteFile(filepath.Join(primaryHome, "config.toml"), []byte(primaryConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	muxRoot := filepath.Join(root, "mux")
	store, err := Open(muxRoot, primaryHome)
	if err != nil {
		t.Fatal(err)
	}
	added, err := store.AddAccount("Work")
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(added.CodexHome, "config.toml")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(config)
	for _, expected := range []string{
		`cli_auth_credentials_store = "file"`,
		`mcp_oauth_credentials_store = "file"`,
		`model = "gpt-test"`,
		`[mcp_servers.node_repl]`,
		`SKY_CUA_SERVICE_PATH = "/Applications/Codex Subscription Router Computer Use.app"`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("account config is missing %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "/primary-only") {
		t.Fatalf("primary project trust leaked into account config:\n%s", text)
	}

	text += `
[projects."/account-project"]
trust_level = "trusted"
`
	if err := os.WriteFile(configPath, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	primaryConfig = strings.ReplaceAll(primaryConfig, "gpt-test", "gpt-updated")
	if err := os.WriteFile(filepath.Join(primaryHome, "config.toml"), []byte(primaryConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(muxRoot, primaryHome); err != nil {
		t.Fatal(err)
	}
	config, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	text = string(config)
	if !strings.Contains(text, `model = "gpt-updated"`) {
		t.Fatalf("managed config was not refreshed:\n%s", text)
	}
	if !strings.Contains(text, `[projects."/account-project"]`) {
		t.Fatalf("account project trust was not preserved:\n%s", text)
	}
}

func TestSyncManagedConfigPropagatesPluginsWithoutRestart(t *testing.T) {
	root := t.TempDir()
	primaryHome := filepath.Join(root, "primary")
	if err := os.MkdirAll(primaryHome, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(primaryHome, "config.toml")
	if err := os.WriteFile(configPath, []byte("model = \"before\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(filepath.Join(root, "mux"), primaryHome)
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.AddAccount("Work")
	if err != nil {
		t.Fatal(err)
	}
	updated := "model = \"after\"\n\n[plugins.\"browser@openai-bundled\"]\nenabled = true\n"
	if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncManagedConfig(); err != nil {
		t.Fatal(err)
	}
	isolated, err := os.ReadFile(filepath.Join(account.CodexHome, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(isolated), `[plugins."browser@openai-bundled"]`) {
		t.Fatalf("plugin config did not propagate:\n%s", isolated)
	}
}

func TestUpdateAccountPreservesController(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root, filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	label := "Personal"
	enabled := false
	account, err := store.UpdateAccount("primary", &label, &enabled)
	if err != nil {
		t.Fatal(err)
	}
	if account.Label != label || account.Enabled || !account.Controller {
		t.Fatalf("unexpected updated account: %#v", account)
	}
}

func TestPendingLoginPersistsAcrossStoreReopenAndCanBeCleared(t *testing.T) {
	root := t.TempDir()
	muxRoot := filepath.Join(root, "mux")
	primaryHome := filepath.Join(root, "primary")
	store, err := Open(muxRoot, primaryHome)
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.AddAccount("Waiting")
	if err != nil {
		t.Fatal(err)
	}
	marked, err := store.SetPendingLogin(account.ID, "login-123")
	if err != nil {
		t.Fatal(err)
	}
	if !marked.PendingLogin || marked.PendingLoginID != "login-123" {
		t.Fatalf("pending login was not marked: %#v", marked)
	}
	reopened, err := Open(muxRoot, primaryHome)
	if err != nil {
		t.Fatal(err)
	}
	waiting, ok := reopened.Account(account.ID)
	if !ok || !waiting.PendingLogin || waiting.PendingLoginID != "login-123" {
		t.Fatalf("pending login did not survive reopen: %#v ok=%v", waiting, ok)
	}
	cleared, err := reopened.ClearPendingLogin(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.PendingLogin || cleared.PendingLoginID != "" {
		t.Fatalf("pending login was not cleared: %#v", cleared)
	}
}

func TestOpenIsolatedMovesEveryLegacyNativeAccountWithoutTouchingNativeHome(t *testing.T) {
	root := t.TempDir()
	muxRoot := filepath.Join(root, "mux")
	legacyHome := filepath.Join(root, "native")
	isolatedPrimary := filepath.Join(root, "relay-primary")
	if err := os.MkdirAll(legacyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	nativeAuth := filepath.Join(legacyHome, "auth.json")
	if err := os.WriteFile(nativeAuth, []byte(`{"native":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(muxRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	stateJSON := `{
  "version": 1,
  "accounts": [
    {"id":"legacy-secondary","label":"Old secondary","codexHome":"` + filepath.ToSlash(legacyHome) + `","enabled":true,"controller":true,"createdAt":1}
  ],
  "threadOwner": {"old-thread":"legacy-secondary"}
}
`
	if err := os.WriteFile(filepath.Join(muxRoot, "state.json"), []byte(stateJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := OpenIsolated(muxRoot, isolatedPrimary, legacyHome)
	if err != nil {
		t.Fatal(err)
	}
	migrated, ok := store.Account("legacy-secondary")
	if !ok {
		t.Fatal("legacy secondary account disappeared")
	}
	wantHome := filepath.Join(muxRoot, "accounts", migrated.ID, "codex-home")
	if filepath.Clean(migrated.CodexHome) != filepath.Clean(wantHome) {
		t.Fatalf("legacy account still points outside Relay: got=%q want=%q", migrated.CodexHome, wantHome)
	}
	if filepath.Clean(migrated.CodexHome) == filepath.Clean(legacyHome) {
		t.Fatal("legacy account still shares the native Codex home")
	}
	if _, ok := store.Account("primary"); !ok {
		t.Fatal("isolated Relay primary was not restored")
	}
	if _, ok := store.ThreadOwner("old-thread"); ok {
		t.Fatal("old native-home thread affinity was retained")
	}
	if contents, err := os.ReadFile(nativeAuth); err != nil || string(contents) != `{"native":true}` {
		t.Fatalf("native Codex auth was changed: contents=%q err=%v", contents, err)
	}
	if _, err := os.Stat(filepath.Join(migrated.CodexHome, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy auth unexpectedly copied into isolated home: err=%v", err)
	}
}

func TestDiscardProvisionalAccountRemovesOnlyUnownedSecondary(t *testing.T) {
	root := t.TempDir()
	store, err := Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Temporary")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DiscardProvisionalAccount(secondary.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Account(secondary.ID); ok {
		t.Fatalf("discarded account %q is still present", secondary.ID)
	}
	if primary, ok := store.Controller(); !ok || primary.ID != "primary" || !primary.Controller {
		t.Fatalf("discarding a secondary changed controller: %#v ok=%v", primary, ok)
	}

	reopened, err := Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.Account(secondary.ID); ok {
		t.Fatalf("discarded account %q was persisted after reopen", secondary.ID)
	}
}

func TestDiscardProvisionalAccountRejectsPrimaryAndThreadOwner(t *testing.T) {
	root := t.TempDir()
	store, err := Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DiscardProvisionalAccount("primary"); err == nil {
		t.Fatal("discarding primary should fail")
	}
	secondary, err := store.AddAccount("Assigned")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetThreadOwner("thread-owned", secondary.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DiscardProvisionalAccount(secondary.ID); err == nil {
		t.Fatal("discarding a thread-owning account should fail")
	}
	if _, ok := store.Account(secondary.ID); !ok {
		t.Fatal("rejected discard removed the assigned account")
	}
}

func TestSetControllerPersistsIndependentPrimary(t *testing.T) {
	root := t.TempDir()
	store, err := Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	selected, err := store.SetController(secondary.ID)
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != secondary.ID || !selected.Controller {
		t.Fatalf("unexpected selected controller: %#v", selected)
	}
	if primary, ok := store.Account("primary"); !ok || primary.Controller {
		t.Fatalf("original account remained controller: %#v ok=%v", primary, ok)
	}
	reopened, err := Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	if primary, ok := reopened.Controller(); !ok || primary.ID != secondary.ID {
		t.Fatalf("independent Primary choice did not persist: %#v ok=%v", primary, ok)
	}
}

func TestOpenRestoresNativePrimaryWhenPersistedStateLostIt(t *testing.T) {
	root := t.TempDir()
	muxRoot := filepath.Join(root, "mux")
	primaryHome := filepath.Join(root, "primary")
	secondaryHome := filepath.Join(muxRoot, "accounts", "secondary", "codex-home")
	if err := os.MkdirAll(filepath.Dir(secondaryHome), 0o700); err != nil {
		t.Fatal(err)
	}
	persisted := persistedState{
		Version: 1,
		Accounts: []Account{{
			ID:         "secondary",
			Label:      "Subscription 2",
			CodexHome:  secondaryHome,
			Enabled:    true,
			Controller: true,
		}},
		ThreadOwner: map[string]string{},
	}
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(muxRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(muxRoot, "state.json"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := Open(muxRoot, primaryHome)
	if err != nil {
		t.Fatal(err)
	}
	primary, ok := store.Account("primary")
	if !ok || primary.CodexHome != primaryHome || primary.Controller {
		t.Fatalf("native primary was not restored correctly: %#v ok=%v", primary, ok)
	}
	controller, ok := store.Controller()
	if !ok || controller.ID != "secondary" {
		t.Fatalf("existing Router Primary changed during migration: %#v ok=%v", controller, ok)
	}
	reopened, err := Open(muxRoot, primaryHome)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.Account("primary"); !ok {
		t.Fatal("restored native primary was not persisted")
	}
}

func TestOpenIsolatedMigratesNativePrimaryWithoutTouchingNativeHome(t *testing.T) {
	root := t.TempDir()
	muxRoot := filepath.Join(root, "mux")
	legacyHome := filepath.Join(root, "native")
	isolatedHome := filepath.Join(root, "relay", "codex-home")
	if err := os.MkdirAll(legacyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	nativeConfig := filepath.Join(legacyHome, "config.toml")
	if err := os.WriteFile(nativeConfig, []byte("native = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secondaryHome := filepath.Join(muxRoot, "accounts", "secondary", "codex-home")
	if err := os.MkdirAll(filepath.Dir(secondaryHome), 0o700); err != nil {
		t.Fatal(err)
	}
	persisted := persistedState{
		Version: 1,
		Accounts: []Account{
			{ID: "primary", Label: "Primary", CodexHome: legacyHome, Enabled: true},
			{ID: "secondary", Label: "Subscription 2", CodexHome: secondaryHome, Enabled: true, Controller: true},
		},
		ThreadOwner: map[string]string{"old-chat": "primary", "secondary-chat": "secondary"},
	}
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(muxRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(muxRoot, "state.json"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := OpenIsolated(muxRoot, isolatedHome, legacyHome)
	if err != nil {
		t.Fatal(err)
	}
	primary, ok := store.Account("primary")
	if !ok || primary.CodexHome != isolatedHome || primary.Controller {
		t.Fatalf("native primary was not moved into the isolated home: %#v ok=%v", primary, ok)
	}
	if controller, ok := store.Controller(); !ok || controller.ID != "secondary" {
		t.Fatalf("existing Router controller changed: %#v ok=%v", controller, ok)
	}
	if _, ok := store.ThreadOwner("old-chat"); ok {
		t.Fatal("native chat ownership leaked into the isolated Relay state")
	}
	if owner, ok := store.ThreadOwner("secondary-chat"); !ok || owner != "secondary" {
		t.Fatalf("secondary ownership was not preserved: owner=%q ok=%v", owner, ok)
	}
	if got, err := os.ReadFile(nativeConfig); err != nil || string(got) != "native = true\n" {
		t.Fatalf("native home was modified: err=%v contents=%q", err, got)
	}
	config, err := os.ReadFile(filepath.Join(isolatedHome, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), `cli_auth_credentials_store = "file"`) ||
		!strings.Contains(string(config), `mcp_oauth_credentials_store = "file"`) {
		t.Fatalf("isolated primary does not use file-backed credentials:\n%s", config)
	}
}

func TestRemoveAccountProtectsPrimaryAndThreadOwnership(t *testing.T) {
	root := t.TempDir()
	store, err := Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RemoveAccount("primary", true); err == nil {
		t.Fatal("removing the active Primary should fail")
	}
	if err := store.SetThreadOwner("old-chat", secondary.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RemoveAccount(secondary.ID, false); err == nil {
		t.Fatal("removing an account that owns a chat should require force")
	}
	removed, err := store.RemoveAccount(secondary.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if removed.ID != secondary.ID {
		t.Fatalf("removed unexpected account: %#v", removed)
	}
	if _, ok := store.Account(secondary.ID); ok {
		t.Fatal("forced removal left account metadata")
	}
	if _, ok := store.ThreadOwner("old-chat"); ok {
		t.Fatal("forced removal left stale thread ownership")
	}
}

func TestRemoveAccountProtectsRelayPrimaryHomeAfterControllerSwitch(t *testing.T) {
	root := t.TempDir()
	muxRoot := filepath.Join(root, "mux")
	primaryHome := filepath.Join(root, "primary")
	store, err := Open(muxRoot, primaryHome)
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetController(secondary.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RemoveAccount("primary", true); err == nil {
		t.Fatal("the Relay primary home must remain addressable after a controller switch")
	}
	if _, err := store.DiscardProvisionalAccount("primary"); err == nil {
		t.Fatal("the Relay primary home must not be discarded as a provisional account")
	}
}
