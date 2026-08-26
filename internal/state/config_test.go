package state

import (
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
