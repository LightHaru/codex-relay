package mux

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LightHaru/codex-relay/internal/protocol"
	"github.com/LightHaru/codex-relay/internal/state"
)

// TestMuxCancelLoginHelper is executed as a child app-server by the tests
// below. It deliberately implements only the narrow account methods needed to
// exercise the real Child lifecycle; no network or real account is involved.
func TestMuxCancelLoginHelper(t *testing.T) {
	if os.Getenv("GO_WANT_MUX_CANCEL_HELPER") != "1" {
		return
	}
	connected := os.Getenv("MUX_CANCEL_HELPER_CONNECTED") == "1"
	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()
	for scanner.Scan() {
		message, err := protocol.Parse(scanner.Bytes())
		if err != nil || message.Method == "" || len(message.ID) == 0 {
			continue
		}
		if message.Method == "account/login/cancel" {
			var params struct {
				LoginID string `json:"loginId"`
			}
			_ = json.Unmarshal(message.Params, &params)
			if logPath := os.Getenv("MUX_CANCEL_HELPER_LOG"); logPath != "" {
				_ = os.WriteFile(logPath, []byte(params.LoginID), 0o600)
			}
		}
		result := json.RawMessage(`{}`)
		if message.Method == "account/read" {
			if connected {
				result = json.RawMessage(`{"account":{"type":"chatgpt","email":"test@example.invalid","planType":"plus"}}`)
			} else {
				result = json.RawMessage(`{"account":null}`)
			}
		}
		encoded, _ := protocol.Encode(protocol.Success(message.ID, result))
		_, _ = writer.Write(append(encoded, '\n'))
		_ = writer.Flush()
	}
	os.Exit(0)
}

func newCancelLoginTestMux(t *testing.T, connected bool) (*Multiplexer, *state.Store, state.Account, context.CancelFunc, string) {
	t.Helper()
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Temporary")
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "cancel-login-id")
	environment := append([]string{}, os.Environ()...)
	environment = append(environment,
		"GO_WANT_MUX_CANCEL_HELPER=1",
		"MUX_CANCEL_HELPER_LOG="+logPath,
	)
	if connected {
		environment = append(environment, "MUX_CANCEL_HELPER_CONNECTED=1")
	}
	multiplexer, err := New(Options{
		RealExecutable: os.Args[0],
		RealArgs:       []string{"-test.run=TestMuxCancelLoginHelper", "--"},
		Environment:    environment,
		Store:          store,
		Output:         io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := multiplexer.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		multiplexer.Close()
		cancel()
	})
	return multiplexer, store, secondary, cancel, logPath
}

func TestCancelLoginDiscardsUnconnectedSecondaryAndItsHome(t *testing.T) {
	multiplexer, store, secondary, _, logPath := newCancelLoginTestMux(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := multiplexer.CancelLogin(ctx, secondary.ID, "login-2")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Canceled || result.Connected {
		t.Fatalf("unexpected cancellation result: %#v", result)
	}
	if _, ok := store.Account(secondary.ID); ok {
		t.Fatalf("cancelled subscription %q is still in state", secondary.ID)
	}
	if _, err := os.Stat(filepath.Dir(secondary.CodexHome)); !os.IsNotExist(err) {
		t.Fatalf("cancelled isolated home still exists or could not be checked: %v", err)
	}
	loginID, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(loginID) != "login-2" {
		t.Fatalf("cancel request used %q, want login-2", loginID)
	}
	if controller, ok := store.Controller(); !ok || controller.ID != "primary" {
		t.Fatalf("cancellation changed the primary controller: %#v ok=%v", controller, ok)
	}
}

func TestCancelLoginPreservesAlreadyConnectedSecondary(t *testing.T) {
	multiplexer, store, secondary, _, logPath := newCancelLoginTestMux(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := multiplexer.CancelLogin(ctx, secondary.ID, "login-race")
	if err != nil {
		t.Fatal(err)
	}
	if result.Canceled || !result.Connected || result.Account == nil || result.Account.ID != secondary.ID {
		t.Fatalf("connected race should preserve the account: %#v", result)
	}
	if _, ok := store.Account(secondary.ID); !ok {
		t.Fatal("connected subscription was removed")
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("connected account should not send a cancel request: %v", err)
	}
}

func TestCancelLoginRejectsPrimary(t *testing.T) {
	multiplexer, _, _, _, _ := newCancelLoginTestMux(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := multiplexer.CancelLogin(ctx, "primary", "primary-login")
	if err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatalf("expected primary cancellation to be rejected, got %v", err)
	}
}
