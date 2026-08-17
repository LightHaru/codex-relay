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

	"github.com/LightHaru/codex-subscription-router/internal/protocol"
	"github.com/LightHaru/codex-subscription-router/internal/state"
)

type routingHelperConfig struct {
	LogPath         string  `json:"logPath"`
	PrimaryHome     string  `json:"primaryHome"`
	PrimaryShort    float64 `json:"primaryShort"`
	PrimaryWeekly   float64 `json:"primaryWeekly"`
	SecondaryShort  float64 `json:"secondaryShort"`
	SecondaryWeekly float64 `json:"secondaryWeekly"`
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
			result = json.RawMessage(`{"thread":{"id":"thread-1","path":"C:\\fake\\thread.jsonl","cwd":"C:\\fake","modelProvider":"openai"}}`)
		case "thread/resume", "thread/start", "turn/start":
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
	configData, err := json.Marshal(routingHelperConfig{
		LogPath:         logPath,
		PrimaryHome:     primaryHome,
		PrimaryShort:    primaryShort,
		PrimaryWeekly:   primaryWeekly,
		SecondaryShort:  secondaryShort,
		SecondaryWeekly: secondaryWeekly,
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
		"secondary:thread/resume",
		"secondary:turn/start",
	)
	owner, ok := pool.store.ThreadOwner("thread-1")
	if !ok || owner != pool.secondary.ID {
		t.Fatalf("failover owner=%q ok=%v, want %q", owner, ok, pool.secondary.ID)
	}
}
