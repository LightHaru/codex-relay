package mux

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LightHaru/codex-relay/internal/protocol"
	"github.com/LightHaru/codex-relay/internal/state"
)

type routingHelperConfig struct {
	LogPath                   string  `json:"logPath"`
	PrimaryHome               string  `json:"primaryHome"`
	PrimaryShort              float64 `json:"primaryShort"`
	PrimaryWeekly             float64 `json:"primaryWeekly"`
	SecondaryShort            float64 `json:"secondaryShort"`
	SecondaryWeekly           float64 `json:"secondaryWeekly"`
	ThreadHistoryRelativePath string  `json:"threadHistoryRelativePath"`
	ThreadHistoryContents     string  `json:"threadHistoryContents"`
}

// TestMuxRoutingHelper is a deterministic JSONL app-server used only by the
// integration tests below. It models quota metadata and thread read/resume
// locally, so the routing and failover checks cannot consume a real account's
// quota or make a network request.
func TestMuxRoutingHelper(t *testing.T) {
	if os.Getenv("GO_WANT_MUX_ROUTING_HELPER") != "1" {
		return
	}
	configData, err := os.ReadFile(os.Getenv("MUX_ROUTING_HELPER_CONFIG"))
	if err != nil {
		os.Exit(2)
	}
	var config routingHelperConfig
	if json.Unmarshal(configData, &config) != nil {
		os.Exit(2)
	}
	role := "secondary"
	if filepath.Clean(os.Getenv("CODEX_HOME")) == filepath.Clean(config.PrimaryHome) {
		role = "primary"
	}
	short, weekly := config.SecondaryShort, config.SecondaryWeekly
	if role == "primary" {
		short, weekly = config.PrimaryShort, config.PrimaryWeekly
	}
	shortMinutes, weeklyMinutes := int64(300), int64(10_080)
	rateLimits, _ := json.Marshal(map[string]any{
		"rateLimits": RateLimits{
			Primary:   &RateLimitWindow{UsedPercent: short, WindowDurationMins: &shortMinutes},
			Secondary: &RateLimitWindow{UsedPercent: weekly, WindowDurationMins: &weeklyMinutes},
		},
	})

	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()
	for scanner.Scan() {
		message, parseErr := protocol.Parse(scanner.Bytes())
		if parseErr != nil || message.Method == "" || len(message.ID) == 0 {
			continue
		}
		result := json.RawMessage(`{}`)
		switch message.Method {
		case "account/read":
			result = json.RawMessage(`{"account":{"type":"chatgpt","email":"test@example.invalid","planType":"plus"}}`)
		case "account/rateLimits/read":
			result = rateLimits
		case "thread/read":
			appendRoutingHelperLog(config.LogPath, role+":"+message.Method)
			path := filepath.Join(config.PrimaryHome, "sessions", config.ThreadHistoryRelativePath)
			encoded, _ := json.Marshal(map[string]any{"thread": map[string]any{
				"id": "thread-1", "path": path, "cwd": "C:\\fake", "modelProvider": "openai",
			}})
			result = encoded
		case "thread/resume":
			var params map[string]json.RawMessage
			_ = json.Unmarshal(message.Params, &params)
			if _, exists := params["path"]; exists {
				encoded, _ := protocol.Encode(protocol.Failure(message.ID, -32602, "thread/resume path is unsupported"))
				_, _ = writer.Write(append(encoded, '\n'))
				_ = writer.Flush()
				continue
			}
			if _, exists := params["history"]; exists {
				encoded, _ := protocol.Encode(protocol.Failure(message.ID, -32602, "thread/resume history is unsupported"))
				_, _ = writer.Write(append(encoded, '\n'))
				_ = writer.Flush()
				continue
			}
			if role == "secondary" {
				historyPath := filepath.Join(os.Getenv("CODEX_HOME"), "sessions", config.ThreadHistoryRelativePath)
				history, err := os.ReadFile(historyPath)
				if err != nil || string(history) != config.ThreadHistoryContents {
					encoded, _ := protocol.Encode(protocol.Failure(message.ID, -32602, "target history was not migrated"))
					_, _ = writer.Write(append(encoded, '\n'))
					_ = writer.Flush()
					continue
				}
				appendRoutingHelperLog(config.LogPath, role+":history-copied")
			}
			appendRoutingHelperLog(config.LogPath, role+":"+message.Method)
			result = json.RawMessage(`{"thread":{"id":"thread-1"}}`)
		case "thread/start", "turn/start":
			appendRoutingHelperLog(config.LogPath, role+":"+message.Method)
			result = json.RawMessage(`{"thread":{"id":"thread-1"}}`)
		}
		encoded, _ := protocol.Encode(protocol.Success(message.ID, result))
		_, _ = writer.Write(append(encoded, '\n'))
		_ = writer.Flush()
	}
	os.Exit(0)
}

func appendRoutingHelperLog(path, line string) {
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(file, line)
	_ = file.Close()
}

type lockedOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (output *lockedOutput) Write(value []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.buf.Write(value)
}

func (output *lockedOutput) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.buf.String()
}

type routingTestPool struct {
	multiplexer *Multiplexer
	output      *lockedOutput
	secondary   state.Account
	store       *state.Store
	logPath     string
}

func newRoutingTestPool(
	t *testing.T,
	primaryShort, primaryWeekly, secondaryShort, secondaryWeekly float64,
) routingTestPool {
	t.Helper()
	root := t.TempDir()
	primaryHome := filepath.Join(root, "primary")
	store, err := state.Open(filepath.Join(root, "mux"), primaryHome)
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "routing-helper.json")
	logPath := filepath.Join(root, "routing-helper.log")
	historyRelativePath := filepath.Join("2026", "08", "20", "rollout-thread-1.jsonl")
	historyContents := `{"type":"session_meta","id":"thread-1"}` + "\n"
	historyPath := filepath.Join(primaryHome, "sessions", historyRelativePath)
	if err := os.MkdirAll(filepath.Dir(historyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(historyPath, []byte(historyContents), 0o600); err != nil {
		t.Fatal(err)
	}
	configData, err := json.Marshal(routingHelperConfig{
		LogPath:                   logPath,
		PrimaryHome:               primaryHome,
		PrimaryShort:              primaryShort,
		PrimaryWeekly:             primaryWeekly,
		SecondaryShort:            secondaryShort,
		SecondaryWeekly:           secondaryWeekly,
		ThreadHistoryRelativePath: historyRelativePath,
		ThreadHistoryContents:     historyContents,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, configData, 0o600); err != nil {
		t.Fatal(err)
	}
	environment := append([]string{}, os.Environ()...)
	environment = append(environment,
		"GO_WANT_MUX_ROUTING_HELPER=1",
		"MUX_ROUTING_HELPER_CONFIG="+configPath,
	)
	output := &lockedOutput{}
	multiplexer, err := New(Options{
		RealExecutable: os.Args[0],
		RealArgs:       []string{"-test.run=TestMuxRoutingHelper", "--"},
		Environment:    environment,
		Store:          store,
		Output:         output,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Selection tests must remain local and deterministic. The fallback has
	// known zero reset credits rather than asking the live ChatGPT endpoint.
	multiplexer.resetPreviews = map[string]ResetCreditsPreview{
		secondary.ID: {AccountID: secondary.ID, AvailableCount: 0},
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
	return routingTestPool{
		multiplexer: multiplexer,
		output:      output,
		secondary:   secondary,
		store:       store,
		logPath:     logPath,
	}
}

func waitForRoutingEvidence(t *testing.T, pool routingTestPool, expected ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		log, _ := os.ReadFile(pool.logPath)
		text := string(log)
		if allContained(text, expected...) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	log, _ := os.ReadFile(pool.logPath)
	t.Fatalf("did not observe routing evidence %q; helper log=%q output=%q", expected, string(log), pool.output.String())
}

func allContained(text string, expected ...string) bool {
	for _, item := range expected {
		if !strings.Contains(text, item) {
			return false
		}
	}
	return true
}

func TestRouteNewThreadUsesPrimaryBeforeAHealthierSecondary(t *testing.T) {
	pool := newRoutingTestPool(t, 80, 90, 5, 5)
	pool.multiplexer.HandleClient(protocol.Request(
		"thread/start",
		protocol.StringID("new-primary-thread"),
		json.RawMessage(`{"cwd":"C:\\fake"}`),
	))
	waitForRoutingEvidence(t, pool, "primary:thread/start")
	if log, _ := os.ReadFile(pool.logPath); strings.Contains(string(log), "secondary:thread/start") {
		t.Fatalf("new thread bypassed usable Primary: %s", log)
	}
}

func TestRouteNewThreadFallsBackWhenPrimaryShortWindowIsDepleted(t *testing.T) {
	pool := newRoutingTestPool(t, 100, 40, 20, 30)
	pool.multiplexer.HandleClient(protocol.Request(
		"thread/start",
		protocol.StringID("new-fallback-thread"),
		json.RawMessage(`{"cwd":"C:\\fake"}`),
	))
	waitForRoutingEvidence(t, pool, "secondary:thread/start")
	if log, _ := os.ReadFile(pool.logPath); strings.Contains(string(log), "primary:thread/start") {
		t.Fatalf("short-window-depleted Primary received a new thread: %s", log)
	}
}

func TestRouteTurnFailsOverToSecondaryAndPersistsNewOwner(t *testing.T) {
	pool := newRoutingTestPool(t, 100, 100, 15, 25)
	if err := pool.store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	pool.multiplexer.HandleClient(protocol.Request(
		"turn/start",
		protocol.StringID("failed-over-turn"),
		json.RawMessage(`{"threadId":"thread-1"}`),
	))
	waitForRoutingEvidence(t, pool,
		"primary:thread/read",
		"secondary:history-copied",
		"secondary:thread/resume",
		"secondary:turn/start",
	)
	owner, ok := pool.store.ThreadOwner("thread-1")
	if !ok || owner != pool.secondary.ID {
		t.Fatalf("failover owner=%q ok=%v, want %q", owner, ok, pool.secondary.ID)
	}
}

func TestSetPrimaryPersistsRouterChoiceIndependentOfNativePrimary(t *testing.T) {
	pool := newRoutingTestPool(t, 20, 25, 30, 35)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	before := make(map[string]any)
	for _, entry := range pool.multiplexer.childEntries() {
		before[entry.account.ID] = entry.child
	}
	change, err := pool.multiplexer.SetPrimaryAndRestart(ctx, pool.secondary.ID)
	if err != nil {
		t.Fatal(err)
	}
	selected := change.Account
	if selected.ID != pool.secondary.ID || !selected.Controller {
		t.Fatalf("unexpected selected Router Primary: %#v", selected)
	}
	if change.RestartedChildren != 2 {
		t.Fatalf("restarted children=%d, want 2", change.RestartedChildren)
	}
	for _, entry := range pool.multiplexer.childEntries() {
		if before[entry.account.ID] == entry.child {
			t.Fatalf("account %s kept the same app-server child after Primary change", entry.account.ID)
		}
	}
	controller, ok := pool.store.Controller()
	if !ok || controller.ID != pool.secondary.ID {
		t.Fatalf("Router Primary was not persisted independently: %#v ok=%v", controller, ok)
	}
	if original, ok := pool.store.Account("primary"); !ok || original.Controller {
		t.Fatalf("native/original account unexpectedly remained Router Primary: %#v ok=%v", original, ok)
	}
}

func TestRemoveSecondaryStopsChildAndCleansRouterHome(t *testing.T) {
	pool := newRoutingTestPool(t, 20, 25, 30, 35)
	secondaryHome := pool.secondary.CodexHome
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	removed, err := pool.multiplexer.RemoveAccount(ctx, pool.secondary.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if removed.ID != pool.secondary.ID {
		t.Fatalf("removed unexpected account: %#v", removed)
	}
	if _, ok := pool.store.Account(pool.secondary.ID); ok {
		t.Fatal("removed account remained in Router state")
	}
	if _, err := os.Stat(secondaryHome); !os.IsNotExist(err) {
		t.Fatalf("secondary home was not cleaned up: err=%v", err)
	}
	if controller, ok := pool.store.Controller(); !ok || controller.ID != "primary" {
		t.Fatalf("removing secondary changed Primary: %#v ok=%v", controller, ok)
	}
}
