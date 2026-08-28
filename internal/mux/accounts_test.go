package mux

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
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

func TestCachedUsageQuotaCannotOverwriteNewerAppServerSnapshot(t *testing.T) {
	weeklyMinutes := int64(10_080)
	newerObservedAt := time.Date(2026, time.August, 27, 14, 0, 10, 0, time.UTC).UnixMilli()
	olderObservedAt := newerObservedAt - int64((10*time.Second)/time.Millisecond)
	allowed := true
	limitReached := false
	snapshot := AccountSnapshot{
		ID: "account-1", Enabled: true, Connected: true, AuthType: "chatgpt",
		RateLimitAvailable: true, RateLimitsObservedAt: newerObservedAt, QuotaSource: "app-server",
		RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 14, WindowDurationMins: &weeklyMinutes}},
	}
	staleUsage := usageQuotaSignal{
		Allowed: &allowed, LimitReached: &limitReached, ObservedAt: olderObservedAt,
		RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 0, WindowDurationMins: &weeklyMinutes}},
	}

	if applyUsageQuotaSignal(&snapshot, staleUsage) {
		t.Fatal("stale Usage cache must not replace a newer direct app-server quota snapshot")
	}
	if got := snapshot.RateLimits.Primary.UsedPercent; got != 14 {
		t.Fatalf("fresh app-server usage changed to %.0f%%, want 14%%", got)
	}
	if snapshot.QuotaSource != "app-server" || snapshot.RateLimitsObservedAt != newerObservedAt {
		t.Fatalf("fresh quota provenance changed: %#v", snapshot)
	}
}

func TestNewerUsageQuotaCanRepairAppServerSnapshot(t *testing.T) {
	weeklyMinutes := int64(10_080)
	observedAt := time.Date(2026, time.August, 27, 14, 0, 10, 0, time.UTC).UnixMilli()
	allowed := true
	limitReached := false
	snapshot := AccountSnapshot{
		RateLimitAvailable: true, RateLimitsObservedAt: observedAt, QuotaSource: "app-server",
		RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 14, WindowDurationMins: &weeklyMinutes}},
	}
	newerUsage := usageQuotaSignal{
		Allowed: &allowed, LimitReached: &limitReached, ObservedAt: observedAt + 1,
		RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 15, WindowDurationMins: &weeklyMinutes}},
	}

	if !applyUsageQuotaSignal(&snapshot, newerUsage) {
		t.Fatal("newer Usage evidence should be applied")
	}
	if got := snapshot.RateLimits.Primary.UsedPercent; got != 15 {
		t.Fatalf("newer Usage evidence was not applied: got %.0f%%", got)
	}
	if snapshot.QuotaSource != "app-server+usage" {
		t.Fatalf("quota source=%q, want app-server+usage", snapshot.QuotaSource)
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

func TestPoolStatusAddsFiveSubscriptionQuotasInsteadOfAveragingThem(t *testing.T) {
	multiplexer, _ := newCoordinatorTestMux(t)
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	multiplexer.now = func() time.Time { return now }
	weeklyMinutes := int64(10_080)
	used := []float64{0, 27, 100, 100, 0}
	snapshots := make([]AccountSnapshot, 0, len(used))
	for index, usedPercent := range used {
		snapshots = append(snapshots, AccountSnapshot{
			ID: fmt.Sprintf("account-%d", index+1), Enabled: true, Connected: true, AuthType: "chatgpt",
			RateLimitAvailable: true, RateLimitsObservedAt: now.UnixMilli(),
			RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: usedPercent, WindowDurationMins: &weeklyMinutes}},
		})
	}
	pool := multiplexer.poolStatus(snapshots)
	if pool.MaximumPercent != 500 || pool.ConfirmedRemainingPercent != 273 {
		t.Fatalf("pool quota = %.0f/%.0f, want 273/500: %#v", pool.ConfirmedRemainingPercent, pool.MaximumPercent, pool)
	}
	if pool.KnownSubscriptions != 5 || pool.UnknownSubscriptions != 0 || pool.AvailableSubscriptions != 3 || pool.DepletedSubscriptions != 2 {
		t.Fatalf("pool worker counts are wrong: %#v", pool)
	}

	for index := range snapshots {
		snapshots[index].RateLimits.Primary.UsedPercent = 0
	}
	pool = multiplexer.poolStatus(snapshots)
	if pool.ConfirmedRemainingPercent != 500 || pool.MaximumPercent != 500 {
		t.Fatalf("five fresh subscriptions must render as 500/500, got %#v", pool)
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
	multiplexer := &Multiplexer{store: store}
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
	selected, _, err = multiplexer.chooseFairShareFromSnapshots([]AccountSnapshot{
		{ID: secondary.ID, Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitAvailable: false},
	}, nil)
	if err != nil || selected.ID != secondary.ID {
		t.Fatalf("probation-only pool selected %q with error %v", selected.ID, err)
	}
}

func TestSchedulerZeroTwentyTwoHundredQuotaDistribution(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	second, _ := store.AddAccount("Subscription B")
	third, _ := store.AddAccount("Subscription C")
	minutes := int64(10_080)
	now := time.Unix(2_000_000_000, 0)
	multiplexer := &Multiplexer{store: store, now: func() time.Time { return now }}
	snapshots := []AccountSnapshot{
		{ID: "primary", Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitAvailable: true, RateLimitsObservedAt: now.UnixMilli(), RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 100, WindowDurationMins: &minutes}}},
		{ID: second.ID, Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitAvailable: true, RateLimitsObservedAt: now.UnixMilli(), RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 78, WindowDurationMins: &minutes}}},
		{ID: third.ID, Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitAvailable: true, RateLimitsObservedAt: now.UnixMilli(), RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 0, WindowDurationMins: &minutes}}},
	}
	counts := map[string]int{}
	for index := 0; index < 244; index++ {
		selected, _, err := multiplexer.chooseFairShareFromSnapshots(snapshots, nil)
		if err != nil {
			t.Fatal(err)
		}
		counts[selected.ID]++
	}
	if counts["primary"] != 0 || counts[third.ID] <= counts[second.ID] || counts[second.ID] == 0 {
		t.Fatalf("A=0/B=22/C=100 distribution = %#v", counts)
	}
}

func TestBalancedPolicyUsesPersistentQuotaWeightedDeficits(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	third, err := store.AddAccount("Subscription 3")
	if err != nil {
		t.Fatal(err)
	}
	minutes := int64(10_080)
	multiplexer := &Multiplexer{store: store}
	snapshots := []AccountSnapshot{
		{
			ID: "primary", Controller: true, Enabled: true, Connected: true,
			AuthType: "chatgpt", RateLimitAvailable: true,
			RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 95, WindowDurationMins: &minutes}},
		},
		{
			ID: second.ID, Enabled: true, Connected: true,
			AuthType: "chatgpt", RateLimitAvailable: true,
			RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 5, WindowDurationMins: &minutes}},
		},
		{
			ID: third.ID, Enabled: true, Connected: true,
			AuthType: "chatgpt", RateLimitAvailable: true,
			RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 50, WindowDurationMins: &minutes}},
		},
	}
	counts := map[string]int{}
	for index := 0; index < 30; index++ {
		selected, _, err := multiplexer.chooseFairShareFromSnapshots(snapshots, nil)
		if err != nil {
			t.Fatal(err)
		}
		counts[selected.ID]++
	}
	if counts["primary"] == 0 || counts[second.ID] <= counts[third.ID] || counts[third.ID] <= counts["primary"] {
		t.Fatalf("weighted selection did not follow remaining capacity: %#v", counts)
	}
	beforeRestart := store.Scheduler()
	reopened, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Scheduler().Cursor != beforeRestart.Cursor {
		t.Fatalf("scheduler cursor did not survive restart: before=%d after=%d", beforeRestart.Cursor, reopened.Scheduler().Cursor)
	}
	if !reflect.DeepEqual(reopened.Scheduler().Deficits, beforeRestart.Deficits) {
		t.Fatalf("scheduler deficits did not survive restart: before=%v after=%v", beforeRestart.Deficits, reopened.Scheduler().Deficits)
	}
	if !reflect.DeepEqual(reopened.Scheduler().Dispatches, beforeRestart.Dispatches) || !reflect.DeepEqual(reopened.Scheduler().LastSelectedAt, beforeRestart.LastSelectedAt) {
		t.Fatalf("per-worker dispatch metadata did not survive restart: before=%#v after=%#v", beforeRestart, reopened.Scheduler())
	}
}

func TestFairShareFiveFullAccountsAreEachDispatchedBeforeAnyRepeat(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	for index := 2; index <= 5; index++ {
		if _, err := store.AddAccount(fmt.Sprintf("Subscription %d", index)); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Unix(2_000_000_000, 0)
	minutes := int64(10_080)
	multiplexer := &Multiplexer{store: store, now: func() time.Time { return now }}
	snapshots := make([]AccountSnapshot, 0, 5)
	for _, account := range store.Accounts() {
		snapshots = append(snapshots, AccountSnapshot{
			ID: account.ID, Label: account.Label, CreatedAt: account.CreatedAt,
			Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitAvailable: true,
			RateLimitsObservedAt: now.UnixMilli(),
			RateLimits:           &RateLimits{Primary: &RateLimitWindow{UsedPercent: 0, WindowDurationMins: &minutes}},
		})
	}
	counts := make(map[string]int, len(snapshots))
	for index := 0; index < len(snapshots); index++ {
		preview := multiplexer.previewNextCandidate(snapshots)
		selected, _, err := multiplexer.chooseFairShareFromSnapshots(snapshots, nil)
		if err != nil {
			t.Fatal(err)
		}
		if preview == nil || preview.AccountID != selected.ID {
			t.Fatalf("preview/dispatch drift at selection %d: preview=%#v selected=%q", index, preview, selected.ID)
		}
		counts[selected.ID]++
	}
	for _, snapshot := range snapshots {
		if counts[snapshot.ID] != 1 {
			t.Fatalf("five full workers were not anti-starvation balanced: counts=%#v", counts)
		}
	}
}

func TestFairShareDoesNotUseLowQuotaWorkerWhileFullWorkersAreUnused(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	for index := 2; index <= 5; index++ {
		if _, err := store.AddAccount(fmt.Sprintf("Subscription %d", index)); err != nil {
			t.Fatal(err)
		}
	}
	accounts := store.Accounts()
	now := time.Unix(2_000_000_000, 0)
	minutes := int64(10_080)
	multiplexer := &Multiplexer{store: store, now: func() time.Time { return now }}
	snapshots := make([]AccountSnapshot, 0, len(accounts))
	lowQuotaID := accounts[len(accounts)-1].ID
	for _, account := range accounts {
		used := 0.0
		if account.ID == lowQuotaID {
			used = 69
		}
		snapshots = append(snapshots, AccountSnapshot{
			ID: account.ID, Label: account.Label, CreatedAt: account.CreatedAt,
			Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitAvailable: true,
			RateLimitsObservedAt: now.UnixMilli(),
			RateLimits:           &RateLimits{Primary: &RateLimitWindow{UsedPercent: used, WindowDurationMins: &minutes}},
		})
	}
	counts := make(map[string]int, len(snapshots))
	for index := 0; index < len(snapshots)-1; index++ {
		selected, _, err := multiplexer.chooseFairShareFromSnapshots(snapshots, nil)
		if err != nil {
			t.Fatal(err)
		}
		counts[selected.ID]++
	}
	if counts[lowQuotaID] != 0 {
		t.Fatalf("31%% worker was selected while unused 100%% workers existed: counts=%#v", counts)
	}
	for _, account := range accounts[:len(accounts)-1] {
		if counts[account.ID] != 1 {
			t.Fatalf("unused full-quota worker was starved: counts=%#v", counts)
		}
	}
}

func TestFairShareCorrectsHistoricalOveruseRelativeToQuotaWeight(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	for index := 2; index <= 5; index++ {
		if _, err := store.AddAccount(fmt.Sprintf("Subscription %d", index)); err != nil {
			t.Fatal(err)
		}
	}
	accounts := store.Accounts()
	if err := store.UpdateScheduler(func(scheduler *state.SchedulerState) error {
		scheduler.Dispatches[accounts[0].ID] = 1
		scheduler.Dispatches[accounts[1].ID] = 4
		scheduler.Dispatches[accounts[2].ID] = 1
		scheduler.Dispatches[accounts[3].ID] = 1
		scheduler.Dispatches[accounts[4].ID] = 5
		scheduler.Deficits[accounts[0].ID] = -0.21
		scheduler.Deficits[accounts[1].ID] = -0.55
		scheduler.Deficits[accounts[2].ID] = -0.21
		scheduler.Deficits[accounts[3].ID] = -0.21
		scheduler.Deficits[accounts[4].ID] = 1.18
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2_000_000_000, 0)
	minutes := int64(10_080)
	multiplexer := &Multiplexer{store: store, now: func() time.Time { return now }}
	snapshots := make([]AccountSnapshot, 0, len(accounts))
	for index, account := range accounts {
		used := 0.0
		if index == 1 {
			used = 6
		}
		if index == 4 {
			used = 74
		}
		snapshots = append(snapshots, AccountSnapshot{
			ID: account.ID, Label: account.Label, CreatedAt: account.CreatedAt,
			Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitAvailable: true,
			RateLimitsObservedAt: now.UnixMilli(),
			RateLimits:           &RateLimits{Primary: &RateLimitWindow{UsedPercent: used, WindowDurationMins: &minutes}},
		})
	}
	preview := multiplexer.previewNextCandidate(snapshots)
	selected, _, err := multiplexer.chooseFairShareFromSnapshots(snapshots, nil)
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID == accounts[4].ID || store.Scheduler().Dispatches[selected.ID] != 2 {
		t.Fatalf("historically overused 26%% worker retained priority: selected=%q scheduler=%#v", selected.ID, store.Scheduler())
	}
	if preview == nil || preview.AccountID != selected.ID {
		t.Fatalf("historical catch-up preview drifted: preview=%#v selected=%q", preview, selected.ID)
	}
}

func TestConcurrentSchedulerReservationsAreAtomic(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, _ := store.AddAccount("Subscription 2")
	minutes := int64(300)
	now := time.Unix(2_000_000_000, 0)
	multiplexer := &Multiplexer{store: store, now: func() time.Time { return now }}
	snapshots := []AccountSnapshot{
		{ID: "primary", Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitsObservedAt: now.UnixMilli(), RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 50, WindowDurationMins: &minutes}}},
		{ID: secondary.ID, Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitsObservedAt: now.UnixMilli(), RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 50, WindowDurationMins: &minutes}}},
	}
	const requestCount = 24
	var wait sync.WaitGroup
	errors := make(chan error, requestCount)
	for index := 0; index < requestCount; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, _, err := multiplexer.chooseFairShareFromSnapshotsReserved(snapshots, nil, fmt.Sprintf("concurrent-%d", index))
			errors <- err
		}(index)
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	scheduler := store.Scheduler()
	counts := map[string]int{}
	for _, reservation := range scheduler.Reservations {
		counts[reservation.AccountID]++
	}
	if len(scheduler.Reservations) != requestCount || counts["primary"] != requestCount/2 || counts[secondary.ID] != requestCount/2 {
		t.Fatalf("atomic reservations = %d, distribution = %#v", len(scheduler.Reservations), counts)
	}
}

func TestFailedSendRollsBackSchedulerDispatchCredit(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	minutes := int64(300)
	now := time.Unix(2_000_000_000, 0)
	multiplexer := &Multiplexer{store: store, now: func() time.Time { return now }}
	before := store.Scheduler()
	_, _, err = multiplexer.chooseFairShareFromSnapshotsReserved([]AccountSnapshot{
		{ID: "primary", Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitsObservedAt: now.UnixMilli(), RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 50, WindowDurationMins: &minutes}}},
	}, nil, "failed-send")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RollbackReservation("failed-send"); err != nil {
		t.Fatal(err)
	}
	after := store.Scheduler()
	if after.Cursor != before.Cursor || len(after.Reservations) != 0 || !reflect.DeepEqual(after.Deficits, before.Deficits) || !reflect.DeepEqual(after.Dispatches, before.Dispatches) || !reflect.DeepEqual(after.LastSelectedAt, before.LastSelectedAt) {
		t.Fatalf("failed dispatch leaked scheduler credit: before=%#v after=%#v", before, after)
	}
}

func TestReselectingSameLogicalTurnReplacesSchedulerCharge(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	minutes := int64(300)
	now := time.Unix(2_000_000_000, 0)
	multiplexer := &Multiplexer{store: store, now: func() time.Time { return now }}
	snapshots := []AccountSnapshot{
		{ID: "primary", Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitsObservedAt: now.UnixMilli(), RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 50, WindowDurationMins: &minutes}}},
		{ID: secondary.ID, Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitsObservedAt: now.UnixMilli(), RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 50, WindowDurationMins: &minutes}}},
	}
	first, _, err := multiplexer.chooseFairShareFromSnapshotsReserved(snapshots, nil, "logical-turn")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := multiplexer.chooseFairShareFromSnapshotsReserved(snapshots, map[string]struct{}{first.ID: {}}, "logical-turn")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatalf("replacement selection stayed on excluded account %q", first.ID)
	}
	scheduler := store.Scheduler()
	reservation, ok := scheduler.Reservations["logical-turn"]
	if !ok || reservation.AccountID != second.ID || !reservation.DispatchCharged {
		t.Fatalf("replacement reservation = %#v ok=%v", reservation, ok)
	}
	if scheduler.Cursor != 1 {
		t.Fatalf("logical turn counted as %d dispatches, want 1", scheduler.Cursor)
	}
	if err := store.RollbackReservation("logical-turn"); err != nil {
		t.Fatal(err)
	}
	after := store.Scheduler()
	if after.Cursor != 0 || len(after.Reservations) != 0 || len(after.Deficits) != 0 || len(after.Dispatches) != 0 || len(after.LastSelectedAt) != 0 {
		t.Fatalf("replacement rollback leaked scheduler state: %#v", after)
	}
}

func TestBalancedSchedulerPrefersFreshKnownQuotaAndSkipsOpenCircuit(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2_000_000_000, 0)
	minutes := int64(300)
	multiplexer := &Multiplexer{store: store, now: func() time.Time { return now }}
	snapshots := []AccountSnapshot{
		{ID: "primary", Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitAvailable: true, RateLimitsObservedAt: now.Add(-3 * time.Minute).UnixMilli(), RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 1, WindowDurationMins: &minutes}}},
		{ID: secondary.ID, Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitAvailable: true, RateLimitsObservedAt: now.UnixMilli(), RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 80, WindowDurationMins: &minutes}}},
	}
	selected, _, err := multiplexer.chooseFairShareFromSnapshots(snapshots, nil)
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != secondary.ID {
		t.Fatalf("stale quota displaced fresh known quota: selected %q", selected.ID)
	}
	if err := store.PutAccountHealth(state.AccountHealth{AccountID: secondary.ID, State: "open", OpenUntil: now.Add(time.Minute).UnixMilli(), Reason: "test"}); err != nil {
		t.Fatal(err)
	}
	selected, _, err = multiplexer.chooseFairShareFromSnapshots(snapshots, nil)
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != "primary" {
		t.Fatalf("open circuit was selected: %q", selected.ID)
	}
}

func TestCircuitBreakerRequiresQuotaRefreshAfterCooldown(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2_000_000_000, 0)
	minutes := int64(300)
	multiplexer := &Multiplexer{store: store, now: func() time.Time { return now }}
	multiplexer.recordAccountFailure("primary", "quota rejected turn")
	snapshot := AccountSnapshot{
		ID: "primary", Enabled: true, Connected: true, AuthType: "chatgpt",
		RateLimitsObservedAt: now.UnixMilli(), RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 50, WindowDurationMins: &minutes}},
	}
	if _, _, err := multiplexer.chooseFairShareFromSnapshots([]AccountSnapshot{snapshot}, nil); !errors.Is(err, errNoSubscriptionCapacity) {
		t.Fatalf("open circuit selection error = %v", err)
	}
	now = now.Add(31 * time.Second)
	if _, _, err := multiplexer.chooseFairShareFromSnapshots([]AccountSnapshot{snapshot}, nil); !errors.Is(err, errNoSubscriptionCapacity) {
		t.Fatalf("expired cooldown without refresh selection error = %v", err)
	}
	snapshot.RateLimitsObservedAt = now.UnixMilli()
	selected, _, err := multiplexer.chooseFairShareFromSnapshots([]AccountSnapshot{snapshot}, nil)
	if err != nil || selected.ID != "primary" {
		t.Fatalf("refreshed half-open account selected %q with error %v", selected.ID, err)
	}
}

func TestCircuitClosesOnlyAfterNewQuotaEpochConfirmsCapacity(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2_000_000_000, 0)
	minutes := int64(300)
	blockedReset := now.Add(5 * time.Minute).Unix()
	multiplexer := &Multiplexer{
		store: store, now: func() time.Time { return now },
		quotaSnapshots:  make(map[string]AccountSnapshot),
		usageQuotaCache: make(map[string]usageQuotaCacheEntry),
		events:          make(map[chan Event]struct{}),
	}
	depleted := AccountSnapshot{
		ID: "primary", Enabled: true, Connected: true, AuthType: "chatgpt",
		RateLimitsObservedAt: now.UnixMilli(),
		RateLimits: &RateLimits{Primary: &RateLimitWindow{
			UsedPercent: 100, WindowDurationMins: &minutes, ResetsAt: &blockedReset,
		}},
	}
	multiplexer.observeAccountQuotaSnapshot(depleted)
	multiplexer.recordAccountFailure("primary", "quota rejected turn")
	health, _ := store.AccountHealth("primary")
	if health.BlockedResetAt != blockedReset {
		t.Fatalf("blocked reset=%d, want %d", health.BlockedResetAt, blockedReset)
	}

	now = now.Add(31 * time.Second)
	sameEpoch := depleted
	sameEpoch.RateLimitsObservedAt = now.UnixMilli()
	sameEpoch.RateLimits = &RateLimits{Primary: &RateLimitWindow{
		UsedPercent: 25, WindowDurationMins: &minutes, ResetsAt: &blockedReset,
	}}
	multiplexer.observeAccountQuotaSnapshot(sameEpoch)
	health, _ = store.AccountHealth("primary")
	if health.State != "open" {
		t.Fatalf("same reset epoch closed circuit: %#v", health)
	}

	newReset := blockedReset + 300
	newEpoch := sameEpoch
	newEpoch.RateLimitsObservedAt = now.Add(time.Second).UnixMilli()
	newEpoch.RateLimits = &RateLimits{Primary: &RateLimitWindow{
		UsedPercent: 0, WindowDurationMins: &minutes, ResetsAt: &newReset,
	}}
	multiplexer.observeAccountQuotaSnapshot(newEpoch)
	health, _ = store.AccountHealth("primary")
	if health.State != "closed" || health.ConsecutiveFailures != 0 || health.RecoverySource != "quota_reset" {
		t.Fatalf("new quota epoch did not close circuit: %#v", health)
	}
}

func TestAccountCapacityHonorsExplicitUsageDeny(t *testing.T) {
	allowed := false
	limitReached := true
	snapshot := AccountSnapshot{
		ID: "primary", Enabled: true, Connected: true, AuthType: "chatgpt",
		QuotaAllowed: &allowed, QuotaLimitReached: &limitReached,
		RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 0}},
	}
	if accountHasCapacity(snapshot) {
		t.Fatal("explicit native Usage denial was treated as capacity")
	}
}

func TestSchedulerPublishesSanitizedAccountSkippedEvents(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "mux"), filepath.Join(root, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.AddAccount("Subscription 2")
	if err != nil {
		t.Fatal(err)
	}
	minutes := int64(300)
	now := time.Unix(2_000_000_000, 0)
	multiplexer := &Multiplexer{store: store, now: func() time.Time { return now }, events: make(map[chan Event]struct{})}
	events, unsubscribe := multiplexer.SubscribeEvents()
	defer unsubscribe()
	selected, _, err := multiplexer.chooseFairShareFromSnapshotsReserved([]AccountSnapshot{
		{ID: "primary", Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitsObservedAt: now.UnixMilli(), RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 100, WindowDurationMins: &minutes}}},
		{ID: secondary.ID, Enabled: true, Connected: true, AuthType: "chatgpt", RateLimitsObservedAt: now.UnixMilli(), RateLimits: &RateLimits{Primary: &RateLimitWindow{UsedPercent: 20, WindowDurationMins: &minutes}}},
	}, nil, "turn-event")
	if err != nil || selected.ID != secondary.ID {
		t.Fatalf("selected %q with error %v", selected.ID, err)
	}
	select {
	case event := <-events:
		if event.Type != "account-skipped" || event.AccountID != "primary" || event.Message == "" || event.Timestamp == 0 {
			t.Fatalf("unexpected skipped event: %#v", event)
		}
	default:
		t.Fatal("depleted worker did not publish account-skipped")
	}
}
