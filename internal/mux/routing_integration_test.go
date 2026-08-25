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

	"github.com/LightHaru/codex-relay/internal/backend"
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
	CapacityFailures          int     `json:"capacityFailures"`
	AsyncUsageFailures        int     `json:"asyncUsageFailures"`
	ThreadStartUsageFailures  int     `json:"threadStartUsageFailures"`
	SecondaryResumeFailures   int     `json:"secondaryResumeFailures"`
	SecondarySupportsPath     bool    `json:"secondarySupportsPath"`
	SecondaryStaleThreadPath  string  `json:"secondaryStaleThreadPath"`
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
	capacityFailures := config.CapacityFailures
	asyncUsageFailures := config.AsyncUsageFailures
	threadStartUsageFailures := config.ThreadStartUsageFailures
	secondaryResumeFailures := config.SecondaryResumeFailures
	resumedPath := ""
	var goal map[string]any
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
		if parseErr != nil {
			continue
		}
		if len(message.ID) == 0 {
			if message.Method == "turn/interrupt" {
				appendRoutingHelperLog(config.LogPath, role+":turn/interrupt:notification")
			}
			continue
		}
		if message.Method == "" {
			if strings.Contains(string(message.ID), "approval-") {
				appendRoutingHelperLog(config.LogPath, role+":approval-response")
			}
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
			path := filepath.Join(os.Getenv("CODEX_HOME"), "sessions", config.ThreadHistoryRelativePath)
			if role == "secondary" {
				if resumedPath != "" {
					path = resumedPath
				} else if config.SecondaryStaleThreadPath != "" {
					path = config.SecondaryStaleThreadPath
				}
			}
			encoded, _ := json.Marshal(map[string]any{"thread": map[string]any{
				"id": "thread-1", "path": path, "cwd": "C:\\fake", "modelProvider": "openai",
			}})
			result = encoded
		case "thread/resume":
			if role == "secondary" && secondaryResumeFailures > 0 {
				secondaryResumeFailures--
				appendRoutingHelperLog(config.LogPath, role+":"+message.Method+":failed")
				encoded, _ := protocol.Encode(protocol.Failure(message.ID, -32603, "synthetic resume failure"))
				_, _ = writer.Write(append(encoded, '\n'))
				_ = writer.Flush()
				continue
			}
			var params map[string]json.RawMessage
			_ = json.Unmarshal(message.Params, &params)
			requestedPath := ""
			if rawPath, exists := params["path"]; exists {
				if !config.SecondarySupportsPath {
					appendRoutingHelperLog(config.LogPath, role+":"+message.Method+":path-unsupported")
					encoded, _ := protocol.Encode(protocol.Failure(message.ID, -32602, "thread/resume path is unsupported"))
					_, _ = writer.Write(append(encoded, '\n'))
					_ = writer.Flush()
					continue
				}
				_ = json.Unmarshal(rawPath, &requestedPath)
			}
			if _, exists := params["history"]; exists {
				encoded, _ := protocol.Encode(protocol.Failure(message.ID, -32602, "thread/resume history is unsupported"))
				_, _ = writer.Write(append(encoded, '\n'))
				_ = writer.Flush()
				continue
			}
			if role == "secondary" {
				historyPath := filepath.Join(os.Getenv("CODEX_HOME"), "sessions", config.ThreadHistoryRelativePath)
				if requestedPath != "" {
					historyPath = requestedPath
				}
				history, err := os.ReadFile(historyPath)
				if err != nil || string(history) != config.ThreadHistoryContents {
					encoded, _ := protocol.Encode(protocol.Failure(message.ID, -32602, "target history was not migrated"))
					_, _ = writer.Write(append(encoded, '\n'))
					_ = writer.Flush()
					continue
				}
				appendRoutingHelperLog(config.LogPath, role+":history-copied")
				if requestedPath != "" {
					resumedPath = requestedPath
					appendRoutingHelperLog(config.LogPath, role+":"+message.Method+":path")
				}
			}
			appendRoutingHelperLog(config.LogPath, role+":"+message.Method)
			result = json.RawMessage(`{"thread":{"id":"thread-1"}}`)
		case "thread/goal/set":
			var params struct {
				ThreadID    string `json:"threadId"`
				Objective   string `json:"objective"`
				Status      string `json:"status"`
				TokenBudget *int64 `json:"tokenBudget"`
			}
			_ = json.Unmarshal(message.Params, &params)
			goal = map[string]any{
				"threadId": params.ThreadID, "objective": params.Objective,
				"status": params.Status, "tokenBudget": params.TokenBudget,
				"tokensUsed": int64(0), "timeUsedSeconds": int64(0),
				"createdAt": time.Now().UnixMilli(), "updatedAt": time.Now().UnixMilli(),
			}
			appendRoutingHelperLog(config.LogPath, role+":"+message.Method+":"+params.Status)
			result, _ = json.Marshal(map[string]any{"goal": goal})
		case "thread/goal/get":
			appendRoutingHelperLog(config.LogPath, role+":"+message.Method)
			result, _ = json.Marshal(map[string]any{"goal": goal})
		case "thread/start":
			appendRoutingHelperLog(config.LogPath, role+":"+message.Method)
			if role == "primary" && threadStartUsageFailures > 0 {
				threadStartUsageFailures--
				encoded, _ := protocol.Encode(protocol.Failure(message.ID, -32000, "You've hit your usage limit. Please try again after your quota resets."))
				_, _ = writer.Write(append(encoded, '\n'))
				_ = writer.Flush()
				continue
			}
			result = json.RawMessage(`{"thread":{"id":"thread-1"}}`)
		case "turn/start":
			var params map[string]json.RawMessage
			_ = json.Unmarshal(message.Params, &params)
			model := ""
			if raw := params["model"]; len(raw) > 0 {
				_ = json.Unmarshal(raw, &model)
			}
			if role == "primary" && capacityFailures > 0 {
				capacityFailures--
				appendRoutingHelperLog(config.LogPath, role+":turn/start:capacity:"+model)
				encoded, _ := protocol.Encode(protocol.Failure(message.ID, -32000, "Selected model is at capacity. Please try a different model."))
				_, _ = writer.Write(append(encoded, '\n'))
				_ = writer.Flush()
				continue
			}
			if role == "primary" && asyncUsageFailures > 0 {
				asyncUsageFailures--
				appendRoutingHelperLog(config.LogPath, role+":turn/start:async-usage")
				encoded, _ := protocol.Encode(protocol.Success(message.ID, json.RawMessage(`{"thread":{"id":"thread-1"}}`)))
				_, _ = writer.Write(append(encoded, '\n'))
				errorParams := json.RawMessage(`{"threadId":"thread-1","error":{"message":"You've hit your usage limit.","codexErrorInfo":"UsageLimitExceeded"}}`)
				errorEvent, _ := protocol.Encode(protocol.Message{Method: "error", Params: errorParams})
				_, _ = writer.Write(append(errorEvent, '\n'))
				completedParams := json.RawMessage(`{"threadId":"thread-1","turn":{"status":"failed","error":{"message":"You've hit your usage limit.","codexErrorInfo":"UsageLimitExceeded"}}}`)
				completedEvent, _ := protocol.Encode(protocol.Message{Method: "turn/completed", Params: completedParams})
				_, _ = writer.Write(append(completedEvent, '\n'))
				_ = writer.Flush()
				continue
			}
			appendRoutingHelperLog(config.LogPath, role+":turn/start:model:"+model)
			result = json.RawMessage(`{"thread":{"id":"thread-1"}}`)
			if model == "approval-test" {
				encoded, _ := protocol.Encode(protocol.Success(message.ID, result))
				_, _ = writer.Write(append(encoded, '\n'))
				approvalParams := json.RawMessage(`{"threadId":"thread-1","itemId":"command-1"}`)
				approval, _ := protocol.Encode(protocol.Request("item/commandExecution/requestApproval", protocol.StringID("approval-"+role), approvalParams))
				_, _ = writer.Write(append(approval, '\n'))
				_ = writer.Flush()
				continue
			}
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
	configPath  string
}

func newRoutingTestPool(
	t *testing.T,
	primaryShort, primaryWeekly, secondaryShort, secondaryWeekly float64,
	capacityFailures ...int,
) routingTestPool {
	return newRoutingTestPoolWithAsyncUsage(
		t,
		primaryShort,
		primaryWeekly,
		secondaryShort,
		secondaryWeekly,
		firstInt(capacityFailures),
		0,
	)
}

func newRoutingTestPoolWithAsyncUsage(
	t *testing.T,
	primaryShort, primaryWeekly, secondaryShort, secondaryWeekly float64,
	capacityFailures, asyncUsageFailures int,
) routingTestPool {
	return newRoutingTestPoolWithConfig(
		t,
		primaryShort,
		primaryWeekly,
		secondaryShort,
		secondaryWeekly,
		capacityFailures,
		asyncUsageFailures,
		0,
	)
}

func newRoutingTestPoolWithThreadStartUsage(
	t *testing.T,
	primaryShort, primaryWeekly, secondaryShort, secondaryWeekly float64,
	threadStartUsageFailures int,
) routingTestPool {
	return newRoutingTestPoolWithConfig(
		t,
		primaryShort,
		primaryWeekly,
		secondaryShort,
		secondaryWeekly,
		0,
		0,
		threadStartUsageFailures,
	)
}

func newRoutingTestPoolWithConfig(
	t *testing.T,
	primaryShort, primaryWeekly, secondaryShort, secondaryWeekly float64,
	capacityFailures, asyncUsageFailures, threadStartUsageFailures int,
	secondaryResumeOptions ...int,
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
	secondarySupportsPath := true
	if len(secondaryResumeOptions) > 1 {
		secondarySupportsPath = secondaryResumeOptions[1] != 0
	}
	secondaryStaleThreadPath := ""
	if len(secondaryResumeOptions) > 2 && secondaryResumeOptions[2] != 0 {
		secondaryStaleThreadPath = filepath.Join(root, "native-store", "sessions", historyRelativePath)
		if err := os.MkdirAll(filepath.Dir(secondaryStaleThreadPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(secondaryStaleThreadPath, []byte(historyContents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	configData, err := json.Marshal(routingHelperConfig{
		LogPath:                   logPath,
		PrimaryHome:               primaryHome,
		PrimaryShort:              primaryShort,
		PrimaryWeekly:             primaryWeekly,
		SecondaryShort:            secondaryShort,
		SecondaryWeekly:           secondaryWeekly,
		CapacityFailures:          capacityFailures,
		AsyncUsageFailures:        asyncUsageFailures,
		ThreadStartUsageFailures:  threadStartUsageFailures,
		SecondaryResumeFailures:   firstInt(secondaryResumeOptions),
		SecondarySupportsPath:     secondarySupportsPath,
		SecondaryStaleThreadPath:  secondaryStaleThreadPath,
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
		RealExecutable:       os.Args[0],
		RealArgs:             []string{"-test.run=TestMuxRoutingHelper", "--"},
		Environment:          environment,
		CompatibilityProfile: "fixture-reviewed-v2",
		Store:                store,
		Output:               output,
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
		configPath:  configPath,
	}
}

func firstInt(values []int) int {
	if len(values) == 0 {
		return 0
	}
	return values[0]
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

func TestRouteNewThreadsWeightsUnequalQuota(t *testing.T) {
	pool := newRoutingTestPool(t, 80, 90, 5, 5)
	for index := 0; index < 4; index++ {
		pool.multiplexer.HandleClient(protocol.Request(
			"thread/start",
			protocol.StringID(fmt.Sprintf("unequal-fair-share-%d", index)),
			json.RawMessage(`{"cwd":"C:\\fake"}`),
		))
	}
	deadline := time.Now().Add(5 * time.Second)
	var log []byte
	for time.Now().Before(deadline) {
		log, _ = os.ReadFile(pool.logPath)
		if strings.Count(string(log), "primary:thread/start")+
			strings.Count(string(log), "secondary:thread/start") >= 4 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	primaryCount := strings.Count(string(log), "primary:thread/start")
	secondaryCount := strings.Count(string(log), "secondary:thread/start")
	if secondaryCount <= primaryCount {
		t.Fatalf("higher-capacity subscription was not weighted higher primary=%d secondary=%d log=%q", primaryCount, secondaryCount, string(log))
	}
}

func TestRouteNewThreadsShareEqualQuotaAcrossSubscriptions(t *testing.T) {
	pool := newRoutingTestPool(t, 20, 20, 20, 20)
	for index := 0; index < 4; index++ {
		pool.multiplexer.HandleClient(protocol.Request(
			"thread/start",
			protocol.StringID(fmt.Sprintf("fair-share-%d", index)),
			json.RawMessage(`{"cwd":"C:\\fake"}`),
		))
	}
	deadline := time.Now().Add(5 * time.Second)
	var log []byte
	for time.Now().Before(deadline) {
		log, _ = os.ReadFile(pool.logPath)
		primaryCount := strings.Count(string(log), "primary:thread/start")
		secondaryCount := strings.Count(string(log), "secondary:thread/start")
		if primaryCount+secondaryCount >= 4 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	primaryCount := strings.Count(string(log), "primary:thread/start")
	secondaryCount := strings.Count(string(log), "secondary:thread/start")
	if primaryCount != 2 || secondaryCount != 2 {
		t.Fatalf("fair-share distribution primary=%d secondary=%d log=%q", primaryCount, secondaryCount, string(log))
	}
}

func TestRouteNewThreadsPreservesLowQuotaReserve(t *testing.T) {
	pool := newRoutingTestPool(t, 96, 96, 10, 20)
	for index := 0; index < 2; index++ {
		pool.multiplexer.HandleClient(protocol.Request(
			"thread/start",
			protocol.StringID(fmt.Sprintf("known-capacity-fair-share-%d", index)),
			json.RawMessage(`{"cwd":"C:\\fake"}`),
		))
	}
	deadline := time.Now().Add(5 * time.Second)
	var log []byte
	for time.Now().Before(deadline) {
		log, _ = os.ReadFile(pool.logPath)
		if strings.Count(string(log), "primary:thread/start")+
			strings.Count(string(log), "secondary:thread/start") >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	primaryCount := strings.Count(string(log), "primary:thread/start")
	secondaryCount := strings.Count(string(log), "secondary:thread/start")
	if primaryCount != 0 || secondaryCount != 2 {
		t.Fatalf("low quota reserve was not preserved primary=%d secondary=%d log=%q", primaryCount, secondaryCount, string(log))
	}
}

func TestRouteTurnRetriesSameModelAfterCapacity(t *testing.T) {
	pool := newRoutingTestPool(t, 10, 20, 10, 20, 1)
	if err := pool.store.SetRoutingPolicy(state.RoutingPolicySticky); err != nil {
		t.Fatal(err)
	}
	if err := pool.store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	pool.multiplexer.HandleClient(protocol.Request(
		"turn/start",
		protocol.StringID("capacity-retry"),
		json.RawMessage(`{"threadId":"thread-1","model":"gpt-5.3-codex"}`),
	))
	waitForRoutingEvidence(t, pool,
		"primary:turn/start:capacity:gpt-5.3-codex",
		"primary:turn/start:model:gpt-5.3-codex",
	)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Count(string(mustReadFile(t, pool.logPath)), "primary:turn/start") >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	log := string(mustReadFile(t, pool.logPath))
	if strings.Count(log, "primary:turn/start") != 2 {
		t.Fatalf("expected one capacity failure and one exact-model retry, log=%q", log)
	}
	if strings.Contains(log, "primary:turn/start:model:") && !strings.Contains(log, "primary:turn/start:model:gpt-5.3-codex") {
		t.Fatalf("retry changed the selected model, log=%q", log)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(pool.output.String(), `"id":"capacity-retry"`) {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(pool.output.String(), `"id":"capacity-retry"`) || strings.Contains(pool.output.String(), "Selected model is at capacity") {
		t.Fatalf("retry did not return a successful response: %s", pool.output.String())
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
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

func TestRouteNewThreadRetriesWhenSelectedAccountReportsUsageLimit(t *testing.T) {
	pool := newRoutingTestPoolWithThreadStartUsage(t, 10, 10, 20, 20, 1)
	pool.multiplexer.HandleClient(protocol.Request(
		"thread/start",
		protocol.StringID("new-thread-usage-retry"),
		json.RawMessage(`{"cwd":"C:\\fake"}`),
	))
	waitForRoutingEvidence(t, pool,
		"primary:thread/start",
		"secondary:thread/start",
	)
	output := pool.output.String()
	if strings.Contains(output, "You've hit your usage limit") || strings.Contains(output, "UsageLimitExceeded") {
		t.Fatalf("new-thread quota failure leaked to desktop output: %s", output)
	}
}

func TestFirstTurnStaysOnWorkerThatCreatedNewThread(t *testing.T) {
	pool := newRoutingTestPool(t, 20, 20, 20, 20)
	pool.multiplexer.HandleClient(protocol.Request(
		"thread/start",
		protocol.StringID("new-thread-first-turn"),
		json.RawMessage(`{"cwd":"C:\\fake"}`),
	))
	deadline := time.Now().Add(5 * time.Second)
	owner := ""
	for time.Now().Before(deadline) {
		owner, _ = pool.store.ThreadOwner("thread-1")
		if owner != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if owner == "" {
		t.Fatalf("new thread owner was not learned; output=%q", pool.output.String())
	}

	pool.multiplexer.HandleClient(protocol.Request(
		"turn/start",
		protocol.StringID("new-thread-first-turn-start"),
		json.RawMessage(`{"threadId":"thread-1"}`),
	))
	role := "primary"
	if owner == pool.secondary.ID {
		role = "secondary"
	}
	waitForRoutingEvidence(t, pool, role+":turn/start:model:")
	log := string(mustReadFile(t, pool.logPath))
	if strings.Contains(log, ":thread/read") || strings.Contains(log, ":thread/resume") {
		t.Fatalf("first turn attempted a history migration before a rollout existed: %q", log)
	}
	if handoffs := pool.store.Handoffs(); len(handoffs) != 0 {
		t.Fatalf("first turn created a premature handoff: %#v", handoffs)
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
	route, ok := pool.store.ThreadRoute("thread-1")
	if !ok || route.Generation != 2 || route.ActiveMigrationID != "" || route.HistorySHA256 == "" {
		t.Fatalf("transactional route was not committed: %#v ok=%v", route, ok)
	}
	handoffs := pool.store.Handoffs()
	if len(handoffs) != 1 || handoffs[0].Phase != "COMMITTED" || handoffs[0].TargetAccountID != pool.secondary.ID {
		t.Fatalf("handoff journal was not committed: %#v", handoffs)
	}
	checkpoint, ok := pool.store.Checkpoint("thread-1")
	if !ok || checkpoint.HistorySHA256 != route.HistorySHA256 {
		t.Fatalf("canonical checkpoint is missing or mismatched: %#v ok=%v", checkpoint, ok)
	}
}

func TestPathResumeOverridesStaleTargetHistoryIndexBeforeVerification(t *testing.T) {
	// The third secondary option creates the exact production regression: the
	// target already has a legacy state row pointing outside its isolated home,
	// while the canonical replica is materialized under the correct home.
	pool := newRoutingTestPoolWithConfig(t, 100, 100, 15, 25, 0, 0, 0, 0, 1, 1)
	if err := pool.store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	pool.multiplexer.HandleClient(protocol.Request(
		"turn/start",
		protocol.StringID("stale-target-index"),
		json.RawMessage(`{"threadId":"thread-1"}`),
	))
	waitForRoutingEvidence(t, pool,
		"secondary:thread/resume:path",
		"secondary:turn/start",
	)
	owner, ok := pool.store.ThreadOwner("thread-1")
	if !ok || owner != pool.secondary.ID {
		t.Fatalf("path-based resume owner=%q ok=%v, want %q", owner, ok, pool.secondary.ID)
	}
	if handoffs := pool.store.Handoffs(); len(handoffs) != 1 || handoffs[0].Phase != "COMMITTED" {
		t.Fatalf("path-based handoff did not commit: %#v", handoffs)
	}
}

func TestPathUnsupportedBuildFallsBackToVerifiedIDResume(t *testing.T) {
	pool := newRoutingTestPoolWithConfig(t, 100, 100, 15, 25, 0, 0, 0, 0, 0)
	if err := pool.store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	pool.multiplexer.HandleClient(protocol.Request(
		"turn/start",
		protocol.StringID("path-unsupported-fallback"),
		json.RawMessage(`{"threadId":"thread-1"}`),
	))
	waitForRoutingEvidence(t, pool,
		"secondary:thread/resume:path-unsupported",
		"secondary:turn/start",
	)
	if handoffs := pool.store.Handoffs(); len(handoffs) != 1 || handoffs[0].Phase != "COMMITTED" {
		t.Fatalf("verified ID fallback did not commit: %#v", handoffs)
	}
}

func TestCompletedBoundaryHandoffTransfersActiveGoalToTarget(t *testing.T) {
	pool := newRoutingTestPool(t, 100, 100, 15, 25)
	if err := pool.store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	pool.multiplexer.HandleClient(protocol.Request(
		"thread/goal/set",
		protocol.StringID("goal-set-source"),
		json.RawMessage(`{"threadId":"thread-1","objective":"keep goal across worker","status":"usageLimited","tokenBudget":null}`),
	))
	waitForRoutingEvidence(t, pool, "primary:thread/goal/set:usageLimited")

	pool.multiplexer.HandleClient(protocol.Request(
		"turn/start",
		protocol.StringID("goal-handoff-turn"),
		json.RawMessage(`{"threadId":"thread-1"}`),
	))
	waitForRoutingEvidence(t, pool,
		"primary:thread/goal/get",
		"secondary:thread/resume",
		"secondary:thread/goal/set:active",
		"secondary:turn/start",
	)
	pool.multiplexer.HandleClient(protocol.Request(
		"thread/goal/get",
		protocol.StringID("goal-get-target"),
		json.RawMessage(`{"threadId":"thread-1"}`),
	))
	waitForRoutingEvidence(t, pool, "secondary:thread/goal/get")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(pool.output.String(), `"objective":"keep goal across worker"`) {
		time.Sleep(20 * time.Millisecond)
	}
	if output := pool.output.String(); !strings.Contains(output, `"objective":"keep goal across worker"`) || !strings.Contains(output, `"status":"active"`) {
		t.Fatalf("target worker did not expose the restored active goal: %s", output)
	}
}

func TestAutonomousGoalQuotaCompletionFailsOverWithoutReplayingTurnStart(t *testing.T) {
	pool := newRoutingTestPool(t, 20, 20, 10, 10)
	if err := pool.store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	pool.multiplexer.HandleClient(protocol.Request(
		"thread/goal/set",
		protocol.StringID("goal-autonomous-source"),
		json.RawMessage(`{"threadId":"thread-1","objective":"continue after quota","status":"usageLimited","tokenBudget":null}`),
	))
	waitForRoutingEvidence(t, pool, "primary:thread/goal/set:usageLimited")

	completed := protocol.Message{
		Method: "turn/completed",
		Params: json.RawMessage(`{
			"threadId":"thread-1",
			"turn":{"id":"goal-turn-quota","error":{
				"message":"You've hit your usage limit.",
				"codexErrorInfo":"usageLimitExceeded"
			}}
		}`),
	}
	raw, err := protocol.Encode(completed)
	if err != nil {
		t.Fatal(err)
	}
	inbound := backend.Inbound{AccountID: "primary", Message: completed, Raw: raw}
	pool.multiplexer.handleInbound(inbound)
	waitForRoutingEvidence(t, pool,
		"primary:thread/goal/get",
		"secondary:thread/resume",
		"secondary:thread/goal/set:active",
	)
	deadline := time.Now().Add(5 * time.Second)
	owner, ok := pool.store.ThreadOwner("thread-1")
	for time.Now().Before(deadline) && (!ok || owner != pool.secondary.ID) {
		time.Sleep(20 * time.Millisecond)
		owner, ok = pool.store.ThreadOwner("thread-1")
	}
	if !ok || owner != pool.secondary.ID {
		t.Fatalf("autonomous quota handoff owner=%q ok=%v, want %q; handoffs=%#v", owner, ok, pool.secondary.ID, pool.store.Handoffs())
	}
	logData, err := os.ReadFile(pool.logPath)
	if err != nil {
		t.Fatal(err)
	}
	if log := string(logData); strings.Contains(log, "secondary:turn/start") {
		t.Fatalf("terminal goal handoff replayed turn/start:\n%s", log)
	}

	// Duplicate terminal delivery must not increment the circuit or initiate a
	// second handoff after ownership has already advanced.
	pool.multiplexer.handleInbound(inbound)
	time.Sleep(100 * time.Millisecond)
	health, _ := pool.store.AccountHealth("primary")
	if health.ConsecutiveFailures != 1 {
		t.Fatalf("duplicate terminal quota event counted %d failures, want 1", health.ConsecutiveFailures)
	}
}

func TestUnknownAppServerProfileKeepsBalancedTaskSticky(t *testing.T) {
	pool := newRoutingTestPool(t, 20, 20, 20, 20)
	pool.multiplexer.compatibilityProfile = "unknown"
	pool.multiplexer.safeHandoff = false
	if err := pool.store.SetRoutingPolicy(state.RoutingPolicyBalanced); err != nil {
		t.Fatal(err)
	}
	if err := pool.store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	pool.multiplexer.HandleClient(protocol.Request("turn/start", protocol.StringID("unknown-sticky"), json.RawMessage(`{"threadId":"thread-1","model":"unknown-profile"}`)))
	waitForRoutingEvidence(t, pool, "primary:turn/start:model:unknown-profile")
	if owner, _ := pool.store.ThreadOwner("thread-1"); owner != "primary" {
		t.Fatalf("unknown profile moved owner to %q", owner)
	}
	if handoffs := pool.store.Handoffs(); len(handoffs) != 0 {
		t.Fatalf("unknown profile created handoff: %#v", handoffs)
	}
	status := pool.multiplexer.RouterStatus(context.Background())
	if status.Policy != state.RoutingPolicyBalanced || status.EffectivePolicy != state.RoutingPolicySticky || status.HandoffSupported {
		t.Fatalf("unknown profile status = %#v", status)
	}
}

func TestUnknownAppServerProfileDoesNotMigrateDepletedExistingTask(t *testing.T) {
	pool := newRoutingTestPool(t, 100, 100, 20, 20)
	pool.multiplexer.compatibilityProfile = "unknown"
	pool.multiplexer.safeHandoff = false
	if err := pool.store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	pool.multiplexer.HandleClient(protocol.Request("turn/start", protocol.StringID("unknown-depleted"), json.RawMessage(`{"threadId":"thread-1"}`)))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(pool.output.String(), "remains Sticky") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(pool.output.String(), "remains Sticky") {
		t.Fatalf("unknown profile did not report safe fallback: %s", pool.output.String())
	}
	if handoffs := pool.store.Handoffs(); len(handoffs) != 0 {
		t.Fatalf("unknown depleted task created handoff: %#v", handoffs)
	}
	logContents, _ := os.ReadFile(pool.logPath)
	if strings.Contains(string(logContents), "secondary:thread/resume") || strings.Contains(string(logContents), "secondary:turn/start") {
		t.Fatalf("unknown depleted task reached fallback worker: %s", logContents)
	}
}

func TestRotatePolicyMovesOnlyAtCompletedTurnBoundaries(t *testing.T) {
	pool := newRoutingTestPool(t, 20, 20, 20, 20)
	if err := pool.store.SetRoutingPolicy(state.RoutingPolicyRotate); err != nil {
		t.Fatal(err)
	}
	if err := pool.store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	first := protocol.Request("turn/start", protocol.StringID("rotate-1"), json.RawMessage(`{"threadId":"thread-1","model":"first"}`))
	pool.multiplexer.HandleClient(first)
	waitForRoutingEvidence(t, pool, "secondary:turn/start:model:first")
	if owner, _ := pool.store.ThreadOwner("thread-1"); owner != pool.secondary.ID {
		t.Fatalf("first rotation owner = %q", owner)
	}

	// No next routing decision is permitted until the active turn is terminal.
	pool.multiplexer.completeActiveTurn("thread-1", pool.secondary.ID)
	second := protocol.Request("turn/start", protocol.StringID("rotate-2"), json.RawMessage(`{"threadId":"thread-1","model":"second"}`))
	pool.multiplexer.HandleClient(second)
	waitForRoutingEvidence(t, pool, "primary:turn/start:model:second")
	if owner, _ := pool.store.ThreadOwner("thread-1"); owner != "primary" {
		t.Fatalf("second rotation owner = %q", owner)
	}
	pool.multiplexer.completeActiveTurn("thread-1", "primary")
	third := protocol.Request("turn/start", protocol.StringID("rotate-3"), json.RawMessage(`{"threadId":"thread-1","model":"third"}`))
	pool.multiplexer.HandleClient(third)
	waitForRoutingEvidence(t, pool, "secondary:turn/start:model:third")
	if owner, _ := pool.store.ThreadOwner("thread-1"); owner != pool.secondary.ID {
		t.Fatalf("third rotation owner = %q", owner)
	}
	route, _ := pool.store.ThreadRoute("thread-1")
	if route.Generation != 4 {
		t.Fatalf("rotation generation = %d, want 4", route.Generation)
	}
}

func TestApprovalResponseReturnsToTheChildThatCreatedIt(t *testing.T) {
	pool := newRoutingTestPool(t, 20, 20, 20, 20)
	if err := pool.store.SetRoutingPolicy(state.RoutingPolicyRotate); err != nil {
		t.Fatal(err)
	}
	if err := pool.store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	pool.multiplexer.HandleClient(protocol.Request("turn/start", protocol.StringID("approval-turn"), json.RawMessage(`{"threadId":"thread-1","model":"approval-test"}`)))
	waitForRoutingEvidence(t, pool, "secondary:turn/start:model:approval-test")
	var routedID json.RawMessage
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pool.multiplexer.serverMu.Lock()
		for key, route := range pool.multiplexer.serverRoutes {
			if route.accountID == pool.secondary.ID && route.threadID == "thread-1" {
				routedID = json.RawMessage(key)
				break
			}
		}
		pool.multiplexer.serverMu.Unlock()
		if len(routedID) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(routedID) == 0 {
		t.Fatal("approval request was not bound to the active worker")
	}
	pool.multiplexer.HandleClient(protocol.Success(routedID, json.RawMessage(`{"decision":"approved"}`)))
	waitForRoutingEvidence(t, pool, "secondary:approval-response")
	logContents, _ := os.ReadFile(pool.logPath)
	if strings.Contains(string(logContents), "primary:approval-response") {
		t.Fatal("approval response crossed into the controller child")
	}
}

func TestThreadlessInterruptUsesTheSingleActiveWorkerBinding(t *testing.T) {
	pool := newRoutingTestPool(t, 20, 20, 20, 20)
	if err := pool.store.SetRoutingPolicy(state.RoutingPolicyRotate); err != nil {
		t.Fatal(err)
	}
	if err := pool.store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	pool.multiplexer.HandleClient(protocol.Request("turn/start", protocol.StringID("interrupt-turn"), json.RawMessage(`{"threadId":"thread-1","model":"interrupt-test"}`)))
	waitForRoutingEvidence(t, pool, "secondary:turn/start:model:interrupt-test")
	pool.multiplexer.HandleClient(protocol.Message{Method: "turn/interrupt", Params: json.RawMessage(`{}`)})
	waitForRoutingEvidence(t, pool, "secondary:turn/interrupt:notification")
	logContents, _ := os.ReadFile(pool.logPath)
	if strings.Contains(string(logContents), "primary:turn/interrupt:notification") {
		t.Fatalf("threadless interrupt crossed into Controller: %s", logContents)
	}
}

func TestFailedTargetResumeRollsBackHandoffAndSourceOwnership(t *testing.T) {
	pool := newRoutingTestPoolWithConfig(t, 100, 100, 20, 20, 0, 0, 0, 2)
	if err := pool.store.SetRoutingPolicy(state.RoutingPolicySticky); err != nil {
		t.Fatal(err)
	}
	if err := pool.store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	pool.multiplexer.HandleClient(protocol.Request("turn/start", protocol.StringID("resume-fails"), json.RawMessage(`{"threadId":"thread-1"}`)))
	waitForRoutingEvidence(t, pool, "secondary:thread/resume:failed")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		handoffs := pool.store.Handoffs()
		if len(handoffs) == 1 && handoffs[0].Phase == "FAILED" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	route, _ := pool.store.ThreadRoute("thread-1")
	if route.AccountID != "primary" || route.Generation != 1 || route.ActiveMigrationID != "" {
		t.Fatalf("failed handoff did not restore source route: %#v", route)
	}
	handoffs := pool.store.Handoffs()
	if len(handoffs) != 1 || handoffs[0].Phase != "FAILED" {
		t.Fatalf("failed handoff journal = %#v", handoffs)
	}
	scheduler := pool.store.Scheduler()
	for (scheduler.Cursor != 0 || len(scheduler.Reservations) != 0 || len(scheduler.Deficits) != 0 || len(scheduler.Dispatches) != 0 || len(scheduler.LastSelectedAt) != 0) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		scheduler = pool.store.Scheduler()
	}
	if scheduler.Cursor != 0 || len(scheduler.Reservations) != 0 || len(scheduler.Deficits) != 0 || len(scheduler.Dispatches) != 0 || len(scheduler.LastSelectedAt) != 0 {
		t.Fatalf("failed handoff leaked a dispatch charge: %#v", scheduler)
	}
}

func TestRouteTurnFailsOverAfterAsyncUsageLimit(t *testing.T) {
	pool := newRoutingTestPoolWithAsyncUsage(t, 20, 20, 20, 20, 0, 1)
	if err := pool.store.SetRoutingPolicy(state.RoutingPolicySticky); err != nil {
		t.Fatal(err)
	}
	if err := pool.store.SetThreadOwner("thread-1", "primary"); err != nil {
		t.Fatal(err)
	}
	pool.multiplexer.HandleClient(protocol.Request(
		"turn/start",
		protocol.StringID("async-usage-failover"),
		json.RawMessage(`{"threadId":"thread-1","model":"gpt-5.3-codex"}`),
	))
	waitForRoutingEvidence(t, pool,
		"primary:turn/start:async-usage",
		"primary:thread/read",
		"secondary:history-copied",
		"secondary:thread/resume",
		"secondary:turn/start:model:gpt-5.3-codex",
	)
	owner, ok := pool.store.ThreadOwner("thread-1")
	if !ok || owner != pool.secondary.ID {
		t.Fatalf("async usage failover owner=%q ok=%v, want %q", owner, ok, pool.secondary.ID)
	}
	output := pool.output.String()
	if strings.Contains(output, "You've hit your usage limit") || strings.Contains(output, "UsageLimitExceeded") {
		t.Fatalf("source async quota failure leaked to desktop output: %s", output)
	}
	if strings.Count(output, `"id":"async-usage-failover"`) != 1 {
		t.Fatalf("async failover duplicated the desktop response: %s", output)
	}
	if handoffs := pool.store.Handoffs(); len(handoffs) != 1 || handoffs[0].Phase != "COMMITTED" {
		t.Fatalf("error plus failed completion created multiple migrations: %#v", handoffs)
	}
}

func TestRouteUnassignedExistingTurnFailsOverFromController(t *testing.T) {
	pool := newRoutingTestPool(t, 100, 100, 15, 25)
	// A chat created before Relay was installed has no persisted Router owner.
	// It begins at the controller so its local history can be read, then must
	// migrate to a subscription with capacity instead of surfacing the
	// controller's depleted quota error.
	pool.multiplexer.HandleClient(protocol.Request(
		"turn/start",
		protocol.StringID("legacy-chat-failed-over-turn"),
		json.RawMessage(`{"threadId":"thread-1"}`),
	))
	waitForRoutingEvidence(t, pool,
		"primary:thread/read",
		"secondary:history-copied",
		"secondary:thread/resume",
		"secondary:turn/start:model:",
	)
	owner, ok := pool.store.ThreadOwner("thread-1")
	if !ok || owner != pool.secondary.ID {
		t.Fatalf("legacy chat owner=%q ok=%v, want %q", owner, ok, pool.secondary.ID)
	}
}

func TestRouteUnassignedExistingResumeUsesRolloutOwner(t *testing.T) {
	pool := newRoutingTestPool(t, 20, 20, 20, 20)
	pool.multiplexer.HandleClient(protocol.Request(
		"thread/resume",
		protocol.StringID("legacy-chat-resume"),
		json.RawMessage(`{"threadId":"thread-1"}`),
	))
	waitForRoutingEvidence(t, pool, "primary:thread/resume")
	deadline := time.Now().Add(5 * time.Second)
	owner, ok := pool.store.ThreadOwner("thread-1")
	for (!ok || owner != "primary") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		owner, ok = pool.store.ThreadOwner("thread-1")
	}
	if !ok || owner != "primary" {
		t.Fatalf("legacy resume owner=%q ok=%v, want primary", owner, ok)
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
