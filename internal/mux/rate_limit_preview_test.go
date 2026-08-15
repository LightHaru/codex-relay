package mux

import "testing"

func TestRateLimitPreviewOverridesOnlySelectedAccount(t *testing.T) {
	reset := int64(1_800_000_000)
	multiplexer := &Multiplexer{rateLimitPreview: &RateLimitPreview{
		Mode:      RateLimitPreviewSingleDepleted,
		AccountID: "selected",
		ResetsAt:  &reset,
	}}
	selected := AccountSnapshot{ID: "selected", Connected: true, AuthType: "chatgpt"}
	other := AccountSnapshot{ID: "other", Connected: true, AuthType: "chatgpt"}

	multiplexer.applyRateLimitPreview(&selected)
	multiplexer.applyRateLimitPreview(&other)

	weekly, _ := longestAndShortestWindow(selected.RateLimits)
	if weekly == nil || weekly.UsedPercent != 100 || weekly.ResetsAt == nil || *weekly.ResetsAt != reset {
		t.Fatalf("selected account was not depleted: %#v", selected.RateLimits)
	}
	if other.RateLimits != nil {
		t.Fatalf("unselected account was modified: %#v", other.RateLimits)
	}
}

func TestAllDepletedPreviewMarksEveryConnectedChatGPTAccount(t *testing.T) {
	multiplexer := &Multiplexer{rateLimitPreview: &RateLimitPreview{
		Mode: RateLimitPreviewAllDepleted,
	}}
	snapshot := AccountSnapshot{ID: "account", Connected: true, AuthType: "chatgpt"}

	multiplexer.applyRateLimitPreview(&snapshot)

	if snapshot.RateLimits == nil || snapshot.RateLimits.RateLimitReachedType != "legacy_rate_limit_reached" {
		t.Fatalf("all-depleted preview was not applied: %#v", snapshot.RateLimits)
	}
}
