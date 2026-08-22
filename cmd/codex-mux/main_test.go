package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInteractiveAppServerDetection(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{args: []string{"-c", "features.code_mode_host=true", "app-server", "--analytics-default-enabled"}, want: true},
		{args: []string{"app-server", "daemon", "version"}, want: false},
		{args: []string{"app-server", "generate-ts", "--out", "/tmp/schema"}, want: false},
		{args: []string{"exec", "hello"}, want: false},
	}
	for _, test := range tests {
		if got := isInteractiveAppServer(test.args); got != test.want {
			t.Fatalf("isInteractiveAppServer(%q)=%v, want %v", test.args, got, test.want)
		}
	}
}

func TestNormalizeRouterArgsForAppServer(t *testing.T) {
	got := normalizeRouterArgs([]string{
		"-c", "features.code_mode_host=true", "app-server", "--analytics-default-enabled",
	}, true)
	want := []string{
		"-c", "features.code_mode_host=true", "app-server", "-c", routerWindowsSandboxOverride,
		"--analytics-default-enabled",
	}
	if !equalStrings(got, want) {
		t.Fatalf("normalizeRouterArgs(app-server)=%q, want %q", got, want)
	}
}

func TestNormalizeRouterArgsReplacesElevatedOverride(t *testing.T) {
	got := normalizeRouterArgs([]string{
		"app-server", "-c", "windows.sandbox=\"elevated\"", "--stdio",
	}, true)
	want := []string{"app-server", "-c", routerWindowsSandboxOverride, "--stdio"}
	if !equalStrings(got, want) {
		t.Fatalf("normalizeRouterArgs(replace)=%q, want %q", got, want)
	}
}

func TestNormalizeRouterArgsForSandboxLeavesPayloadAlone(t *testing.T) {
	got := normalizeRouterArgs([]string{"sandbox", "windows", "--", "cmd.exe", "-c", "echo ok"}, true)
	want := []string{"sandbox", "-c", routerWindowsSandboxOverride, "windows", "--", "cmd.exe", "-c", "echo ok"}
	if !equalStrings(got, want) {
		t.Fatalf("normalizeRouterArgs(sandbox)=%q, want %q", got, want)
	}
}

func TestNormalizeRouterArgsDoesNothingWhenDisabledOrIrrelevant(t *testing.T) {
	args := []string{"exec", "hello"}
	if got := normalizeRouterArgs(args, true); !equalStrings(got, args) {
		t.Fatalf("irrelevant command changed: %q", got)
	}
	if got := normalizeRouterArgs([]string{"app-server"}, false); !equalStrings(got, []string{"app-server"}) {
		t.Fatalf("disabled normalization changed args: %q", got)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestValidateControlToken(t *testing.T) {
	valid := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if got, err := validateControlToken("\n" + valid + "\t"); err != nil || got != valid {
		t.Fatalf("validateControlToken(valid) = %q, %v", got, err)
	}
	for _, invalid := range []string{"short", valid + "00", valid[:63] + "z"} {
		if _, err := validateControlToken(invalid); err == nil {
			t.Fatalf("validateControlToken(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestResolvePrimaryCodexHomeUsesDedicatedRelayHomeOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the installed Relay path is Windows-specific")
	}
	t.Setenv("APPDATA", filepath.Join(`C:\Users\tester`, "AppData", "Roaming"))
	t.Setenv("CODEX_HOME", filepath.Join(`C:\Users\tester`, ".codex"))
	t.Setenv("CODEX_RELAY_CODEX_HOME", "")

	primary, legacy, isolated := resolvePrimaryCodexHome(
		`C:\Users\tester`,
		`C:\Users\tester\AppData\Local\Codex Relay\app\resources\codex.exe`,
	)
	want := filepath.Join(`C:\Users\tester`, "AppData", "Roaming", "Codex Relay", "codex-home")
	if primary != want || legacy != filepath.Join(`C:\Users\tester`, ".codex") || !isolated {
		t.Fatalf("unexpected Relay homes: primary=%q legacy=%q isolated=%v", primary, legacy, isolated)
	}
}

func TestResolvePrimaryCodexHomeKeepsNativePathOutsideRelay(t *testing.T) {
	t.Setenv("CODEX_HOME", filepath.Join(`C:\Users\tester`, ".codex-custom"))
	t.Setenv("CODEX_RELAY_CODEX_HOME", "")
	primary, legacy, isolated := resolvePrimaryCodexHome(
		`C:\Users\tester`,
		`C:\Program Files\WindowsApps\OpenAI.Codex_26.818.3698.0_x64__2p2nqsd0c76g0\app\resources\codex.exe`,
	)
	if primary != os.Getenv("CODEX_HOME") || legacy != "" || isolated {
		t.Fatalf("native path was changed: primary=%q legacy=%q isolated=%v", primary, legacy, isolated)
	}
}

func TestResolvePrimaryCodexHomeRejectsNativeOverride(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the installed Relay path is Windows-specific")
	}
	t.Setenv("APPDATA", filepath.Join(`C:\Users\tester`, "AppData", "Roaming"))
	t.Setenv("CODEX_HOME", filepath.Join(`C:\Users\tester`, ".codex"))
	t.Setenv("CODEX_RELAY_CODEX_HOME", `C:\Users\tester\.codex`)
	primary, legacy, isolated := resolvePrimaryCodexHome(
		`C:\Users\tester`,
		`C:\Users\tester\AppData\Local\Codex Relay\app\resources\codex.exe`,
	)
	want := filepath.Join(`C:\Users\tester`, "AppData", "Roaming", "Codex Relay", "codex-home")
	if primary != want || legacy != filepath.Join(`C:\Users\tester`, ".codex") || !isolated {
		t.Fatalf("native override was accepted: primary=%q legacy=%q isolated=%v", primary, legacy, isolated)
	}
}
