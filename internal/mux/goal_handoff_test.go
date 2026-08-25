package mux

import "testing"

func TestNormalizedTargetGoalPreservesRemainingTokenBudget(t *testing.T) {
	budget := int64(1_000)
	status, remaining := normalizedTargetGoal(&transferableThreadGoal{
		Status: "usageLimited", TokenBudget: &budget, TokensUsed: 375,
	})
	if status != "active" || remaining == nil || *remaining != 625 {
		t.Fatalf("normalized goal status=%q remaining=%v, want active/625", status, remaining)
	}
}

func TestNormalizedTargetGoalDoesNotReactivateExhaustedBudget(t *testing.T) {
	budget := int64(100)
	status, remaining := normalizedTargetGoal(&transferableThreadGoal{
		Status: "usageLimited", TokenBudget: &budget, TokensUsed: 100,
	})
	if status != "budgetLimited" || remaining == nil || *remaining != 0 {
		t.Fatalf("normalized goal status=%q remaining=%v, want budgetLimited/0", status, remaining)
	}
}
