package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/LightHaru/codex-relay/internal/control"
	"github.com/LightHaru/codex-relay/internal/mux"
	"github.com/LightHaru/codex-relay/internal/protocol"
	"github.com/LightHaru/codex-relay/internal/state"
)

const defaultControlPort = 48123

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "codex-mux: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	realExecutable, err := resolveRealExecutable()
	if err != nil {
		return err
	}
	// The Router is intentionally isolated from the official Store app. On
	// Windows we force only Router-owned CLI/app-server invocations onto the
	// reliable unelevated sandbox path; normalizeRouterArgs never edits the
	// user's native ~/.codex/config.toml.
	args := normalizeRouterArgs(os.Args[1:], runtime.GOOS == "windows")
	if !isInteractiveAppServer(args) {
		return passthrough(realExecutable, args)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	root := os.Getenv("CODEX_MUX_HOME")
	if root == "" {
		root = filepath.Join(home, ".codex-mux")
	}
	primaryCodexHome, legacyPrimaryHome, isolatedPrimary := resolvePrimaryCodexHome(home, realExecutable)
	var store *state.Store
	if isolatedPrimary {
		// The Relay copy must never inherit the Store app's ~/.codex account,
		// even when the parent desktop process exposes CODEX_HOME. Its primary
		// home is a Relay-owned directory with a file-backed credential store.
		store, err = state.OpenIsolated(root, primaryCodexHome, legacyPrimaryHome)
	} else {
		store, err = state.Open(root, primaryCodexHome)
	}
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	multiplexer, err := mux.New(mux.Options{
		RealExecutable:       realExecutable,
		RealArgs:             args,
		Environment:          os.Environ(),
		CompatibilityProfile: resolveCompatibilityProfile(realExecutable),
		Store:                store,
		Output:               os.Stdout,
	})
	if err != nil {
		return err
	}
	if err := multiplexer.Start(ctx); err != nil {
		return err
	}
	defer multiplexer.Close()

	token, err := loadOrCreateToken(root)
	if err != nil {
		return err
	}
	port := defaultControlPort
	if value := os.Getenv("CODEX_MUX_CONTROL_PORT"); value != "" {
		if parsed, parseErr := strconv.Atoi(value); parseErr == nil && parsed > 0 && parsed <= 65535 {
			port = parsed
		}
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "codex-mux: account UI unavailable: %v\n", err)
	} else {
		controlServer := control.New(
			listener.Addr().String(),
			token,
			multiplexer,
			os.Getenv("CODEX_MUX_UI_TESTS") == "1",
		)
		go func() {
			if serveErr := controlServer.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				fmt.Fprintf(os.Stderr, "codex-mux: control server: %v\n", serveErr)
			}
		}()
		defer func() {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer shutdownCancel()
			_ = controlServer.Shutdown(shutdownCtx)
		}()
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		message, parseErr := protocol.Parse(scanner.Bytes())
		if parseErr != nil {
			fmt.Fprintf(os.Stderr, "codex-mux: ignore invalid client JSON: %v\n", parseErr)
			continue
		}
		multiplexer.HandleClient(message)
	}
	cancel()
	return scanner.Err()
}

func resolveCompatibilityProfile(realExecutable string) string {
	manifestPath := filepath.Join(filepath.Dir(realExecutable), "..", "codex-relay.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "unknown"
	}
	var manifest struct {
		AppServerCompatibilityProfile string `json:"appServerCompatibilityProfile"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return "unknown"
	}
	profile := strings.TrimSpace(manifest.AppServerCompatibilityProfile)
	if profile == "" {
		return "unknown"
	}
	return profile
}

// resolvePrimaryCodexHome separates the Windows Relay copy from the official
// Store installation. The Store app never launches this wrapper, so the
// executable path is a stable, local marker that does not rely on a mutable
// Electron user-data directory or on inherited environment variables.
//
// CODEX_RELAY_CODEX_HOME is an explicit escape hatch for test/portable
// installations. CODEX_HOME remains supported for the official/native path.
func resolvePrimaryCodexHome(home, executable string) (primary, legacy string, isolated bool) {
	if isRelayWrapperExecutable(executable) {
		legacyHome := filepath.Join(home, ".codex")
		if configured := strings.TrimSpace(os.Getenv("CODEX_RELAY_CODEX_HOME")); configured != "" && !sameCodexHome(configured, legacyHome) {
			return configured, legacyHome, true
		}
		appData := strings.TrimSpace(os.Getenv("APPDATA"))
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appData, "Codex Relay", "codex-home"), legacyHome, true
	}
	if configured := strings.TrimSpace(os.Getenv("CODEX_HOME")); configured != "" {
		return configured, "", false
	}
	return filepath.Join(home, ".codex"), "", false
}

func sameCodexHome(left, right string) bool {
	leftAbsolute, leftErr := filepath.Abs(left)
	rightAbsolute, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		leftAbsolute = filepath.Clean(left)
		rightAbsolute = filepath.Clean(right)
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(leftAbsolute, rightAbsolute)
	}
	return leftAbsolute == rightAbsolute
}

func isRelayWrapperExecutable(executable string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	cleaned := strings.ToLower(filepath.Clean(executable))
	for _, productDirectory := range []string{"codex relay", "codex subscription router"} {
		marker := strings.ToLower(string(filepath.Separator) + productDirectory + string(filepath.Separator) + "app" + string(filepath.Separator) + "resources" + string(filepath.Separator))
		if strings.Contains(cleaned, marker) {
			return true
		}
	}
	return false
}

func resolveRealExecutable() (string, error) {
	if configured := os.Getenv("CODEX_MUX_REAL_CODEX"); configured != "" {
		return configured, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve wrapper executable: %w", err)
	}
	realName := "codex.real"
	if runtime.GOOS == "windows" {
		realName += ".exe"
	}
	realExecutable := filepath.Join(filepath.Dir(executable), realName)
	if _, err := os.Stat(realExecutable); err != nil {
		return "", fmt.Errorf("find bundled codex.real: %w", err)
	}
	return realExecutable, nil
}

func isInteractiveAppServer(args []string) bool {
	for index, argument := range args {
		if argument != "app-server" {
			continue
		}
		return !hasAppServerToolingSubcommand(args, index)
	}
	return false
}

func hasAppServerToolingSubcommand(args []string, commandIndex int) bool {
	for index := commandIndex + 1; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			return false
		}
		if argument == "-c" || argument == "--config" || argument == "--listen" ||
			argument == "--code-mode-host" || argument == "--ws-auth" ||
			argument == "--ws-token-file" || argument == "--ws-token-sha256" ||
			argument == "--ws-shared-secret-file" || argument == "--ws-issuer" ||
			argument == "--ws-audience" || argument == "--ws-max-clock-skew-seconds" {
			index++
			continue
		}
		if strings.HasPrefix(argument, "-") {
			continue
		}
		switch argument {
		case "daemon", "proxy", "generate-ts", "generate-json-schema", "help":
			return true
		default:
			return false
		}
	}
	return false
}

func passthrough(realExecutable string, args []string) error {
	command := exec.Command(realExecutable, args...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Env = os.Environ()
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			os.Exit(exitError.ExitCode())
		}
		return err
	}
	return nil
}

func loadOrCreateToken(root string) (string, error) {
	if configured := os.Getenv("CODEX_MUX_CONTROL_TOKEN"); configured != "" {
		return validateControlToken(configured)
	}
	path := filepath.Join(root, "control-token")
	if data, err := os.ReadFile(path); err == nil {
		token, validateErr := validateControlToken(string(data))
		if validateErr != nil {
			return "", fmt.Errorf("read control token: %w", validateErr)
		}
		if chmodErr := os.Chmod(path, 0o600); chmodErr != nil {
			return "", fmt.Errorf("secure control token: %w", chmodErr)
		}
		return token, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read control token: %w", err)
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate control token: %w", err)
	}
	token := hex.EncodeToString(bytes)
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		return "", fmt.Errorf("write control token: %w", err)
	}
	return token, nil
}

func validateControlToken(value string) (string, error) {
	token := strings.TrimSpace(value)
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return "", errors.New("control token must be exactly 32 random bytes encoded as hexadecimal")
	}
	return token, nil
}
