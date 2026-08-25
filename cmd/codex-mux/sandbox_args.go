package main

import "strings"

const routerWindowsSandboxOverride = `windows.sandbox="unelevated"`

// normalizeRouterArgs applies the Windows sandbox workaround only to the
// Router's own Codex process. The official Store app has its own executable
// and is never passed through this function. Keeping the override on the
// command line also keeps the Router's primary config scoped to its own
// CODEX_HOME; the official app's ~/.codex/config.toml remains untouched.
func normalizeRouterArgs(args []string, forceWindowsUnelevated bool) []string {
	result := append([]string(nil), args...)
	if !forceWindowsUnelevated {
		return result
	}

	commandIndex := -1
	for index, argument := range result {
		if argument == "--" {
			break
		}
		if argument == "app-server" || argument == "sandbox" {
			commandIndex = index
			break
		}
	}
	if commandIndex < 0 {
		return result
	}
	if result[commandIndex] == "app-server" && hasAppServerToolingSubcommand(result, commandIndex) {
		return result
	}

	foundOverride := false
	for index := 0; index < len(result); index++ {
		if result[index] == "--" {
			break
		}
		argument := result[index]
		switch {
		case argument == "-c" || argument == "--config":
			if index+1 >= len(result) || result[index+1] == "--" {
				continue
			}
			if isWindowsSandboxConfig(result[index+1]) {
				result[index+1] = routerWindowsSandboxOverride
				foundOverride = true
			}
			index++
		case strings.HasPrefix(argument, "-c="):
			if isWindowsSandboxConfig(strings.TrimPrefix(argument, "-c=")) {
				result[index] = "-c=" + routerWindowsSandboxOverride
				foundOverride = true
			}
		case strings.HasPrefix(argument, "--config="):
			if isWindowsSandboxConfig(strings.TrimPrefix(argument, "--config=")) {
				result[index] = "--config=" + routerWindowsSandboxOverride
				foundOverride = true
			}
		}
	}
	if foundOverride {
		return result
	}

	// Both `codex app-server` and `codex sandbox` accept -c after the
	// subcommand. Inserting immediately after the subcommand keeps a sandbox
	// command's payload (which may contain arbitrary -c-looking arguments)
	// untouched and gives the Router override normal CLI precedence.
	insertAt := commandIndex + 1
	override := []string{"-c", routerWindowsSandboxOverride}
	result = append(result, override...)
	copy(result[insertAt+len(override):], result[insertAt:len(result)-len(override)])
	copy(result[insertAt:], override)
	return result
}

func isWindowsSandboxConfig(value string) bool {
	value = strings.TrimSpace(value)
	separator := strings.IndexByte(value, '=')
	if separator < 0 {
		return false
	}
	return strings.TrimSpace(value[:separator]) == "windows.sandbox"
}
