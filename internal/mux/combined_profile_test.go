package mux

import (
	"testing"
	"time"
)

func TestAggregateProfileStatsMergesActivity(t *testing.T) {
	pluginID := "browser@openai-bundled"
	first := profileFetchResult{}
	first.profile.Stats = whamProfileStats{
		LifetimeTokens: 100, TotalThreads: 10, LongestRunningTurnSec: 40,
		FastModeUsagePercentage: 20, TotalSkillsUsed: 4, UniqueSkillsUsed: 3,
		MostUsedReasoningEffort: "high", MostUsedReasoningEffortPercentage: 60,
		DailyUsageBuckets: []usageBucket{
			{StartDate: "2026-08-12", Tokens: 10},
			{StartDate: "2026-08-13", Tokens: 20},
		},
		TopInvocations: []profileInvocation{{Type: "plugin", PluginID: &pluginID, UsageCount: 2}},
	}
	second := profileFetchResult{}
	second.profile.Stats = whamProfileStats{
		LifetimeTokens: 200, TotalThreads: 30, LongestRunningTurnSec: 80,
		FastModeUsagePercentage: 60, TotalSkillsUsed: 6, UniqueSkillsUsed: 5,
		MostUsedReasoningEffort: "xhigh", MostUsedReasoningEffortPercentage: 70,
		DailyUsageBuckets: []usageBucket{
			{StartDate: "2026-08-13", Tokens: 30},
			{StartDate: "2026-08-14", Tokens: 40},
		},
		TopInvocations: []profileInvocation{{Type: "plugin", PluginID: &pluginID, UsageCount: 5}},
	}

	got := aggregateProfileStatsAt(
		[]profileFetchResult{first, second},
		time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC),
	)
	if got.LifetimeTokens != 300 || got.TotalThreads != 40 {
		t.Fatalf("usage totals were not combined: %#v", got)
	}
	if got.PeakDailyTokens != 50 {
		t.Fatalf("peak should be based on merged daily totals, got %d", got.PeakDailyTokens)
	}
	if got.CurrentStreakDays != 3 || got.LongestStreakDays != 3 {
		t.Fatalf("unexpected merged streaks current=%d longest=%d", got.CurrentStreakDays, got.LongestStreakDays)
	}
	if got.LongestRunningTurnSec != 80 || got.FastModeUsagePercentage != 50 {
		t.Fatalf("unexpected duration or weighted fast-mode value: %#v", got)
	}
	if len(got.TopInvocations) != 1 || got.TopInvocations[0].UsageCount != 7 {
		t.Fatalf("invocations were not merged: %#v", got.TopInvocations)
	}
	if len(got.DailyUsageBuckets) != 3 || got.CumulativeDailyUsageBuckets[2].Tokens != 100 {
		t.Fatalf("daily activity was not merged: %#v", got.DailyUsageBuckets)
	}
}
