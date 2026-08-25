package mux

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LightHaru/codex-relay/internal/backend"
)

// transferableThreadGoal is the subset accepted by thread/goal/set. Goal
// state is app-server-local rather than part of the rollout JSONL, so a safe
// cross-account handoff must explicitly recreate it on the target worker.
// The value is deliberately kept in memory and never added to routing
// diagnostics because the objective is user-authored content.
type transferableThreadGoal struct {
	Objective   string `json:"objective"`
	Status      string `json:"status"`
	TokenBudget *int64 `json:"tokenBudget"`
	TokensUsed  int64  `json:"tokensUsed"`
}

func readTransferableThreadGoal(ctx context.Context, source *backend.Child, threadID string) (*transferableThreadGoal, error) {
	if source == nil {
		return nil, nil
	}
	params, _ := json.Marshal(map[string]string{"threadId": threadID})
	response, err := source.Request(ctx, "thread/goal/get", params)
	if err != nil {
		// Older reviewed app-server builds predate the goal protocol. Method-not-
		// found means there is no separate goal state to transfer.
		if response.Error != nil && response.Error.Code == -32601 {
			return nil, nil
		}
		return nil, fmt.Errorf("read task goal: %w", err)
	}
	var decoded struct {
		Goal *transferableThreadGoal `json:"goal"`
	}
	if err := json.Unmarshal(response.Result, &decoded); err != nil {
		return nil, fmt.Errorf("decode task goal: %w", err)
	}
	if decoded.Goal == nil || strings.TrimSpace(decoded.Goal.Objective) == "" {
		return nil, nil
	}
	return decoded.Goal, nil
}

func applyTransferableThreadGoal(ctx context.Context, target *backend.Child, threadID string, goal *transferableThreadGoal) error {
	if goal == nil {
		return nil
	}
	status, budget := normalizedTargetGoal(goal)
	params, _ := json.Marshal(map[string]any{
		"threadId": threadID, "objective": goal.Objective,
		"status": status, "tokenBudget": budget,
	})
	response, err := target.Request(ctx, "thread/goal/set", params)
	if err != nil {
		return fmt.Errorf("restore task goal: %w", err)
	}
	var decoded struct {
		Goal *transferableThreadGoal `json:"goal"`
	}
	if err := json.Unmarshal(response.Result, &decoded); err != nil {
		return fmt.Errorf("verify restored task goal: %w", err)
	}
	if decoded.Goal == nil || decoded.Goal.Objective != goal.Objective || decoded.Goal.Status != status {
		return fmt.Errorf("verify restored task goal: target goal does not match source")
	}
	return nil
}

func normalizedTargetGoal(goal *transferableThreadGoal) (string, *int64) {
	status := strings.TrimSpace(goal.Status)
	if status == "usageLimited" {
		// The selected target has confirmed capacity. Keeping usageLimited would
		// immediately pause the goal even though the quota condition was the
		// reason for the handoff.
		status = "active"
	}
	budget := goal.TokenBudget
	if budget != nil {
		remaining := *budget - goal.TokensUsed
		if remaining < 0 {
			remaining = 0
		}
		budget = &remaining
		if remaining == 0 && status == "active" {
			status = "budgetLimited"
		}
	}
	return status, budget
}
