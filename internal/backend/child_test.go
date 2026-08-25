package backend

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestChildEnvironmentKeepsCodexAndSQLiteHomesIsolated(t *testing.T) {
	home := filepath.Join(t.TempDir(), "subscription-2")
	environment := withEnvironment([]string{"PATH=fixture", "CODEX_HOME=wrong", "CODEX_SQLITE_HOME=wrong"}, "CODEX_HOME", home)
	environment = withEnvironment(environment, "CODEX_SQLITE_HOME", home)
	values := map[string][]string{}
	for _, entry := range environment {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) == 2 {
			values[strings.ToUpper(parts[0])] = append(values[strings.ToUpper(parts[0])], parts[1])
		}
	}
	for _, key := range []string{"CODEX_HOME", "CODEX_SQLITE_HOME"} {
		if len(values[key]) != 1 || filepath.Clean(values[key][0]) != filepath.Clean(home) {
			t.Fatalf("%s values = %#v, want isolated home %q", key, values[key], home)
		}
	}
}
