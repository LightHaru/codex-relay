package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const isolatedCredentialConfig = `cli_auth_credentials_store = "file"
mcp_oauth_credentials_store = "file"`

// syncIsolatedConfig shares desktop-managed settings and MCP servers with an
// isolated subscription while keeping its credentials and project trust local.
func syncIsolatedConfig(primaryCodexHome, isolatedCodexHome string) error {
	if isolatedCodexHome == "" {
		return errors.New("isolated Codex home is required")
	}
	if err := os.MkdirAll(isolatedCodexHome, 0o700); err != nil {
		return fmt.Errorf("create isolated Codex home: %w", err)
	}
	if err := os.Chmod(isolatedCodexHome, 0o700); err != nil {
		return fmt.Errorf("secure isolated Codex home: %w", err)
	}

	primaryConfig, err := readConfig(filepath.Join(primaryCodexHome, "config.toml"))
	if err != nil {
		return fmt.Errorf("read primary config: %w", err)
	}
	configPath := filepath.Join(isolatedCodexHome, "config.toml")
	isolatedConfig, err := readConfig(configPath)
	if err != nil {
		return fmt.Errorf("read isolated config: %w", err)
	}

	managed := filterConfig(primaryConfig, func(section string) bool {
		return !isProjectSection(section)
	})
	managed = removeTopLevelCredentialSettings(managed)
	// MCP servers are launched by the Codex child and inherit the values from
	// their TOML environment table. Older Router builds copied the native
	// Store value verbatim, which made node_repl and other helpers read
	// C:\\Users\\<user>\\.codex even though the app-server itself had an
	// isolated CODEX_HOME. Rewrite only the two home selectors; plugin code
	// paths remain shared by design and do not contain credentials.
	managed = rewriteIsolatedCodexHomeEnvironment(managed, isolatedCodexHome)
	// The Windows elevated sandbox currently fails on a number of supported
	// desktop installations (and can block every chat before a turn starts).
	// Secondary homes are Router-owned, so make their safe fallback explicit.
	// The primary home is intentionally not rewritten by secondary sync; the
	// Router app-server receives the same override at process start instead.
	if runtime.GOOS == "windows" {
		managed = forceWindowsSandboxUnelevated(managed)
	}
	projects := filterConfig(isolatedConfig, isProjectSection)

	parts := []string{isolatedCredentialConfig}
	if managed = strings.TrimSpace(managed); managed != "" {
		parts = append(parts, managed)
	}
	if projects = strings.TrimSpace(projects); projects != "" {
		parts = append(parts, projects)
	}
	contents := []byte(strings.Join(parts, "\n\n") + "\n")
	temporaryPath := configPath + ".tmp"
	if err := os.WriteFile(temporaryPath, contents, 0o600); err != nil {
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		return fmt.Errorf("secure temporary config: %w", err)
	}
	// Windows app-server/plugin readers can briefly hold config.toml open.
	// Reuse the bounded atomic-rename retry used by the state ledger so a
	// transient sharing violation never surfaces as a user-visible Access is
	// denied failure during a normal pool refresh.
	if err := renameStateFile(temporaryPath, configPath); err != nil {
		return fmt.Errorf("commit config: %w", err)
	}
	return nil
}

// rewriteIsolatedCodexHomeEnvironment rewrites explicit MCP environment
// selectors that point at the native Codex home. It deliberately does not
// parse and re-encode TOML: keeping the original fragment intact preserves
// comments, unknown future keys, and plugin definitions across Store updates.
// The helper is safe to call on both the Relay primary config and every
// secondary subscription config.
func rewriteIsolatedCodexHomeEnvironment(contents, isolatedCodexHome string) string {
	isolatedCodexHome = strings.TrimSpace(isolatedCodexHome)
	if isolatedCodexHome == "" || strings.TrimSpace(contents) == "" {
		return contents
	}
	quoted := strconv.Quote(isolatedCodexHome)
	lines := strings.Split(contents, "\n")
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		equal := strings.IndexByte(trimmed, '=')
		if equal < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:equal])
		if key != "CODEX_HOME" && key != "CODEX_SQLITE_HOME" {
			continue
		}
		indentLength := len(line) - len(strings.TrimLeft(line, " \t"))
		indent := line[:indentLength]
		lines[index] = indent + key + " = " + quoted
	}
	return strings.Join(lines, "\n")
}

// forceWindowsSandboxUnelevated returns a config fragment with exactly one
// [windows].sandbox assignment set to "unelevated". It deliberately works on
// the small managed TOML fragment rather than unmarshalling/re-encoding the
// full file, so comments, plugin tables, and unknown future settings remain
// byte-for-byte intact. The helper is also used by tests on non-Windows hosts.
func forceWindowsSandboxUnelevated(contents string) string {
	lines := strings.Split(contents, "\n")
	result := make([]string, 0, len(lines)+3)
	section := ""
	foundWindows := false
	wroteSandbox := false

	flushWindows := func() {
		if section == "windows" && !wroteSandbox {
			result = append(result, `sandbox = "unelevated"`)
			wroteSandbox = true
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			flushWindows()
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]"))
			if section == "windows" {
				foundWindows = true
				wroteSandbox = false
			} else {
				wroteSandbox = false
			}
		}
		if section == "windows" && isSandboxAssignment(trimmed) {
			if !wroteSandbox {
				result = append(result, `sandbox = "unelevated"`)
				wroteSandbox = true
			}
			continue
		}
		result = append(result, line)
	}
	flushWindows()
	if !foundWindows {
		if len(result) > 0 && strings.TrimSpace(result[len(result)-1]) != "" {
			result = append(result, "")
		}
		result = append(result, "[windows]", `sandbox = "unelevated"`)
	}
	return strings.Join(result, "\n")
}

func isSandboxAssignment(trimmed string) bool {
	if strings.HasPrefix(trimmed, "#") {
		return false
	}
	keyEnd := strings.IndexByte(trimmed, '=')
	if keyEnd < 0 {
		return false
	}
	return strings.TrimSpace(trimmed[:keyEnd]) == "sandbox"
}

func readConfig(path string) ([]byte, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return contents, err
}

func filterConfig(contents []byte, keep func(section string) bool) string {
	var builder strings.Builder
	section := ""
	for _, line := range strings.Split(string(contents), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]"))
		}
		if keep(section) {
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

func removeTopLevelCredentialSettings(contents string) string {
	var builder strings.Builder
	section := ""
	for _, line := range strings.Split(contents, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]"))
		}
		if section == "" && (strings.HasPrefix(trimmed, "cli_auth_credentials_store =") ||
			strings.HasPrefix(trimmed, "mcp_oauth_credentials_store =")) {
			continue
		}
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func isProjectSection(section string) bool {
	return section == "projects" || strings.HasPrefix(section, "projects.")
}

func samePath(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	leftAbsolute, leftErr := filepath.Abs(left)
	rightAbsolute, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return filepath.Clean(left) == filepath.Clean(right)
	}
	return filepath.Clean(leftAbsolute) == filepath.Clean(rightAbsolute)
}
