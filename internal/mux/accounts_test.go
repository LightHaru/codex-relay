package mux

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LightHaru/codex-relay/internal/state"
)

func TestPlanLabel(t *testing.T) {
	tests := map[string]string{
		"free":       "Free",
		"go":         "Go",
		"plus":       "Plus",
		"prolite":    "Pro 5x",
		"pro":        "Pro 20x",
		"business":   "Business",
		"enterprise": "Enterprise",
		"edu":        "Edu",
		"unknown":    "",
	}
	for planType, want := range tests {
		if got := planLabel(planType); got != want {
			t.Errorf("planLabel(%q) = %q, want %q", planType, got, want)
		}
	}
}

func TestLongestAndShortestWindowUsesQuotaDuration(t *testing.T) {
	shortMinutes := int64(300)
	weeklyMinutes := int64(10_080)
	short := &RateLimitWindow{UsedPercent: 72, WindowDurationMins: &shortMinutes}
	weekly := &RateLimitWindow{UsedPercent: 31, WindowDurationMins: &weeklyMinutes}

	longest, shortest := longestAndShortestWindow(&RateLimits{
		Primary: short, Secondary: weekly,
	})
	if longest != weekly || shortest != short {
		t.Fatalf("windows were not ordered by duration: longest=%#v shortest=%#v", longest, shortest)
	}
}

func TestLongestAndShortestWindowHandlesSingleWindow(t *testing.T) {
	minutes := int64(300)
	only := &RateLimitWindow{UsedPercent: 12, WindowDurationMins: &minutes}
	longest, shortest := longestAndShortestWindow(&RateLimits{Primary: only})
	if longest != only || shortest != only {
		t.Fatalf("single window should serve both roles: longest=%#v shortest=%#v", longest, shortest)
	}
}

func TestEarliestRateLimitResetAtIsPerAccountWindow(t *testing.T) {
	soon := int64(1_700_000_100)
	later := int64(1_700_000_900)
	got := earliestRateLimitResetAt(&RateLimits{
		Primary:   &RateLimitWindow{ResetsAt: &later},
		Secondary: &RateLimitWindow{ResetsAt: &soon},
	})
	if got == nil || *got != soon {
		t.Fatalf("earliest reset=%v, want %d", got, soon)
	}
	if got := earliestRateLimitResetAt(nil); got != nil {
		t.Fatalf("nil limits returned reset %v", got)
	}
}

func TestAggregateRateLimitsKeepsPoolAvailable(t *testing.T) {
	weeklyMinutes := int64(10_080)
	limits, err := aggregateRateLimits([]AccountSnapshot{
		{
			ID: "one", Enabled: true, Connected: true, AuthType: "chatgpt",
			RateLimits: &RateLimits{Primary: &RateLimitWindow{
				UsedPercent: 100, WindowDurationMins: &weeklyMinutes,
			}},
		},
		{
			ID: "two", Enabled: true, Connected: true, AuthType: "chatgpt",
			RateLimits: &RateLimits{Primary: &RateLimitWindow{
				UsedPercent: 20, WindowDurationMins: &weeklyMinutes,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if limits.Primary == nil || limits.Primary.UsedPercent != 60 {
		t.Fatalf("expected pooled usage to average to 60%%, got %#v", limits.Primary)
	}
	if limits.RateLimitReachedType != nil {
		t.Fatalf("pool should remain available while one account has capacity: %#v", limits)
	}
}

func TestAggregateRateLimitsReportsAllDepleted(t *testing.T) {
	limits, err := aggregateRateLimits([]AccountSnapshot{
		{
			ID: "one", Enabled: true, Connected: true, AuthType: "chatgpt",
			RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 100}},
		},
		{
			ID: "two", Enabled: true, Connected: true, AuthType: "chatgpt",
			RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 100}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if limits.RateLimitReachedType != "rate_limit_reached" {
		t.Fatalf("expected the pool to report depletion, got %#v", limits)
	}
}

func TestRouteUrgencyPrefersQuotaExpiringSooner(t *testing.T) {
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	weeklyMinutes := int64(10_080)
	soon := now.Add(24 * time.Hour).Unix()
	later := now.Add(6 * 24 * time.Hour).Unix()
	soonScore := routeUrgencyScore(now, &RateLimitWindow{
		UsedPercent: 40, WindowDurationMins: &weeklyMinutes, ResetsAt: &soon,
	}, resetCreditMetadata{})
	laterScore := routeUrgencyScore(now, &RateLimitWindow{
		UsedPercent: 40, WindowDurationMins: &weeklyMinutes, ResetsAt: &later,
	}, resetCreditMetadata{})
	if soonScore <= laterScore {
		t.Fatalf("sooner reset should be more urgent: soon=%f later=%f", soonScore, laterScore)
	}
}

func TestRouteUrgencyWeightsBankedResetsWithoutDominating(t *testing.T) {
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	weeklyMinutes := int64(10_080)
	reset := now.Add(4 * 24 * time.Hour).Unix()
	window := &RateLimitWindow{
		UsedPercent: 50, WindowDurationMins: &weeklyMinutes, ResetsAt: &reset,
	}
	plain := routeUrgencyScore(now, window, resetCreditMetadata{Known: true})
	banked := routeUrgencyScore(now, window, resetCreditMetadata{Known: true, AvailableCount: 2})
	if banked <= plain {
		t.Fatalf("banked resets should increase urgency: plain=%f banked=%f", plain, banked)
	}
	if banked > plain*1.31 {
		t.Fatalf("banked reset bonus should remain bounded: plain=%f banked=%f", plain, banked)
	}
}

func TestRouteUrgencyCapsResetBonus(t *testing.T) {
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	reset := now.Add(7 * 24 * time.Hour).Unix()
	window := &RateLimitWindow{UsedPercent: 20, ResetsAt: &reset}
	three := routeUrgencyScore(now, window, resetCreditMetadata{Known: true, AvailableCount: 3})
	ten := routeUrgencyScore(now, window, resetCreditMetadata{Known: true, AvailableCount: 10})
	if three != ten {
		t.Fatalf("reset bonus cap was not applied: three=%f ten=%f", three, ten)
	}
}

func TestRouteUrgencyFallsBackToWeeklyUtilization(t *testing.T) {
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	weeklyMinutes := int64(10_080)
	lessUsed := routeUrgencyScore(now, &RateLimitWindow{
		UsedPercent: 20, WindowDurationMins: &weeklyMinutes,
	}, resetCreditMetadata{})
	moreUsed := routeUrgencyScore(now, &RateLimitWindow{
		UsedPercent: 80, WindowDurationMins: &weeklyMinutes,
	}, resetCreditMetadata{})
	if lessUsed <= moreUsed {
		t.Fatalf("fallback should prefer the less-used account: less=%f more=%f", lessUsed, moreUsed)
	}
}

func TestQuotaFallbackSelectsSecondaryWhenPrimaryHasNoCapacity(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	shortMinutes := int64(300)
	weeklyMinutes := int64(10_080)
	multiplexer := &Multiplexer{
		store: store,
		now:   time.Now,
		// Keep selection deterministic and fully local: the fallback algorithm
		// sees known reset metadata instead of making a live API request.
		resetPreviews: map[string]ResetCreditsPreview{
			secondary.ID: {AccountID: secondary.ID, AvailableCount: 0},
		},
	}
	account, _, err := multiplexer.chooseAccountFromSnapshots(context.Background(), []AccountSnapshot{
		{
			ID: "primary", Controller: true, Enabled: true, Connected: true, AuthType: "chatgpt",
			RateLimits: &RateLimits{
				Primary:   &RateLimitWindow{UsedPercent: 100, WindowDurationMins: &shortMinutes},
				Secondary: &RateLimitWindow{UsedPercent: 60, WindowDurationMins: &weeklyMinutes},
			},
		},
		{
			ID: secondary.ID, Enabled: true, Connected: true, AuthType: "chatgpt",
			RateLimits: &RateLimits{
				Primary:   &RateLimitWindow{UsedPercent: 25, WindowDurationMins: &shortMinutes},
				Secondary: &RateLimitWindow{UsedPercent: 40, WindowDurationMins: &weeklyMinutes},
			},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if account.ID != secondary.ID {
		t.Fatalf("fallback selected %q, want %q", account.ID, secondary.ID)
	}
}

func TestFairShareDoesNotPreferUnknownQuotaOverKnownCapacity(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	windowMinutes := int64(300)
	multiplexer := &Multiplexer{
		store:             store,
		newThreadDispatch: make(map[string]uint64),
	}
	selected, _, err := multiplexer.chooseFairShareFromSnapshots([]AccountSnapshot{
		{
			ID: "primary", Controller: true, Enabled: true, Connected: true, AuthType: "chatgpt",
			RateLimits: &RateLimits{
				Primary: &RateLimitWindow{UsedPercent: 95, WindowDurationMins: &windowMinutes},
			},
		},
		{
			ID: secondary.ID, Enabled: true, Connected: true, AuthType: "chatgpt",
			// A child that could not read its rate limits may still become a
			// last-resort fallback, but it must not bypass known capacity.
			RateLimitAvailable: false,
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != "primary" {
		t.Fatalf("selected %q, want known-capacity primary", selected.ID)
	}
}
