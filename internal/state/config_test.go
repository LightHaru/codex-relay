package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestForceWindowsSandboxUnelevatedReplacesAndDeduplicatesSetting(t *testing.T) {
	input := strings.Join([]string{
		`sandbox_mode = "danger-full-access"`,
		`[windows]`,
		`sandbox = "elevated"`,
		`private_desktop = true`,
		`sandbox = "elevated"`,
		`[features]`,
		`foo = true`,
	}, "\n")

	got := forceWindowsSandboxUnelevated(input)
	if strings.Count(got, "sandbox =") != 1 {
		t.Fatalf("expected one windows sandbox assignment, got:\n%s", got)
	}
	if !strings.Contains(got, "sandbox = \"unelevated\"") {
		t.Fatalf("expected unelevated sandbox, got:\n%s", got)
	}
	if strings.Contains(got, `sandbox = "elevated"`) {
		t.Fatalf("elevated sandbox leaked into managed config:\n%s", got)
	}
	if !strings.Contains(got, "private_desktop = true") || !strings.Contains(got, "[features]") {
		t.Fatalf("unrelated settings were lost:\n%s", got)
	}
}

func TestForceWindowsSandboxUnelevatedAddsMissingWindowsTable(t *testing.T) {
	got := forceWindowsSandboxUnelevated("model = \"gpt-5.6-terra\"\n")
	want := "[windows]\nsandbox = \"unelevated\""
	if !strings.Contains(got, want) {
		t.Fatalf("expected missing windows table to be added, got:\n%s", got)
	}
}

func TestForceWindowsSandboxUnelevatedDoesNotRewriteSandboxMode(t *testing.T) {
	input := "sandbox_mode = \"workspace-write\"\n"
	got := forceWindowsSandboxUnelevated(input)
	if !strings.Contains(got, input) {
		t.Fatalf("sandbox_mode should remain unchanged, got:\n%s", got)
	}
}

func TestRewriteIsolatedCodexHomeEnvironmentDoesNotReuseNativeHome(t *testing.T) {
	input := strings.Join([]string{
		"[mcp_servers.node_repl.env]",
		`CODEX_HOME = 'C:\\Users\\ADMIN\\.codex'`,
		`CODEX_SQLITE_HOME = "C:\\Users\\ADMIN\\.codex"`,
		`NODE_REPL_TRUSTED_CODE_PATHS = 'C:\\Users\\ADMIN\\.codex;C:\\Tools\\node_modules'`,
		`# CODEX_HOME = 'C:\Users\ADMIN\.codex'`,
	}, "\n")

	got := rewriteIsolatedCodexHomeEnvironment(input, `C:\Users\ADMIN\AppData\Roaming\Codex Relay\codex-home`)
	if strings.Contains(got, `CODEX_HOME = 'C:\\Users\\ADMIN\\.codex'`) ||
		strings.Contains(got, `CODEX_SQLITE_HOME = "C:\\Users\\ADMIN\\.codex"`) {
		t.Fatalf("native home leaked into MCP environment:\n%s", got)
	}
	for _, expected := range []string{
		`CODEX_HOME = "C:\\Users\\ADMIN\\AppData\\Roaming\\Codex Relay\\codex-home"`,
		`CODEX_SQLITE_HOME = "C:\\Users\\ADMIN\\AppData\\Roaming\\Codex Relay\\codex-home"`,
		`NODE_REPL_TRUSTED_CODE_PATHS = 'C:\\Users\\ADMIN\\.codex;C:\\Tools\\node_modules'`,
		`# CODEX_HOME = 'C:\Users\ADMIN\.codex'`,
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("rewritten config is missing %q:\n%s", expected, got)
		}
	}
}

func TestRewriteIsolatedCodexHomeEnvironmentIsNoopWithoutSelectors(t *testing.T) {
	input := "model = \"gpt-test\"\n"
	if got := rewriteIsolatedCodexHomeEnvironment(input, `C:\Relay`); got != input {
		t.Fatalf("unrelated config changed: got %q want %q", got, input)
	}
}

func TestEnsureRelayPoolProviderTextReplacesManagedTableAndDefault(t *testing.T) {
	input := strings.Join([]string{
		"# keep this comment",
		"model = \"gpt-5.6\"",
		"model_provider = \"openai\"",
		"",
		"[plugins.browser]",
		"enabled = true",
		"",
		"[model_providers.relay_pool]",
		"name = \"stale\"",
		"base_url = \"http://old\"",
		"",
		"[projects.\"C:\\\\work\"]",
		"trust_level = \"trusted\"",
	}, "\n")
	got := ensureRelayPoolProviderText(input, "http://127.0.0.1:48123/v1/")
	if strings.Count(got, "[model_providers.relay_pool]") != 1 {
		t.Fatalf("expected one Relay provider table:\n%s", got)
	}
	for _, expected := range []string{
		`model_provider = "relay_pool"`,
		`base_url = "http://127.0.0.1:48123/v1"`,
		"[plugins.browser]",
		"[projects.\"C:\\\\work\"]",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("missing %q after provider install:\n%s", expected, got)
		}
	}
	if strings.Contains(got, `name = "stale"`) || strings.Contains(got, `base_url = "http://old"`) {
		t.Fatalf("stale Relay provider fields survived:\n%s", got)
	}
}

func TestSyncConfigPreservesRelayProviderTableWhileCopyingManagedSettings(t *testing.T) {
	primary := "model = \"shared\"\nmodel_provider = \"openai\"\n\n[plugins.browser]\nenabled = true\n"
	isolated := "model = \"old\"\nmodel_provider = \"relay_pool\"\n\n[model_providers.relay_pool]\nname = \"Codex Relay Pool\"\nbase_url = \"http://127.0.0.1:1/v1\"\n\n[projects.\"C:\\\\work\"]\ntrust_level = \"trusted\"\n"
	managed := filterConfig([]byte(primary), func(section string) bool { return !isProjectSection(section) })
	managed = removeTopLevelCredentialSettings(managed)
	managed = rewriteIsolatedCodexHomeEnvironment(managed, `C:\Relay`)
	relay := filterConfig([]byte(isolated), isRelayPoolProviderSection)
	if strings.TrimSpace(relay) != "" {
		managed = removeTopLevelSetting(managed, "model_provider")
	}
	got := strings.TrimSpace(strings.Join([]string{isolatedCredentialConfig, managed, relay, filterConfig([]byte(isolated), isProjectSection)}, "\n\n"))
	got = strings.Replace(got, isolatedCredentialConfig+"\n\n", isolatedCredentialConfig+"\n\nmodel_provider = \"relay_pool\"\n\n", 1)
	for _, expected := range []string{"model = \"shared\"", "[plugins.browser]", "[model_providers.relay_pool]", "base_url = \"http://127.0.0.1:1/v1\"", "[projects.\"C:\\\\work\"]"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("merged config is missing %q:\n%s", expected, got)
		}
	}
	if strings.Contains(got, `model_provider = "openai"`) {
		t.Fatalf("native provider leaked into unified authority config:\n%s", got)
	}
}

func TestEnsureRelayPoolProviderWritesAuthorityConfig(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(configPath, []byte("model = \"gpt-5.6\"\nmodel_provider = \"openai\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRelayPoolProvider(root, "http://127.0.0.1:48123/v1"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "model_providers.relay_pool") || !strings.Contains(string(contents), `env_key = "CODEX_RELAY_GATEWAY_TOKEN"`) {
		t.Fatalf("authority provider was not persisted:\n%s", contents)
	}
	before := string(contents)
	if err := EnsureRelayPoolProvider(root, "http://127.0.0.1:48123/v1"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Fatalf("idempotent provider install changed config")
	}
}
