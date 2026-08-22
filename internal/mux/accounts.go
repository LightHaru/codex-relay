package mux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/LightHaru/codex-relay/internal/state"
)

var errNoSubscriptionCapacity = errors.New("no enabled ChatGPT subscription has capacity")

const (
	routingFallbackWindow      = 7 * 24 * time.Hour
	routingMinimumWindow       = time.Minute
	routingResetBonusPerCredit = 0.15
	routingResetBonusCreditCap = 3
)

type RateLimitWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins *int64  `json:"windowDurationMins"`
	ResetsAt           *int64  `json:"resetsAt"`
}

type RateLimits struct {
	Primary              *RateLimitWindow `json:"primary"`
	Secondary            *RateLimitWindow `json:"secondary"`
	RateLimitReachedType any              `json:"rateLimitReachedType"`
}

type AccountSnapshot struct {
	ID                   string      `json:"id"`
	Label                string      `json:"label"`
	Enabled              bool        `json:"enabled"`
	Controller           bool        `json:"controller"`
	Connected            bool        `json:"connected"`
	PendingLogin         bool        `json:"pendingLogin"`
	DisplayName          string      `json:"displayName,omitempty"`
	Username             string      `json:"username,omitempty"`
	Email                string      `json:"email,omitempty"`
	PlanType             string      `json:"planType,omitempty"`
	PlanLabel            string      `json:"planLabel,omitempty"`
	AuthType             string      `json:"authType,omitempty"`
	ProfileImageURL      string      `json:"profileImageUrl,omitempty"`
	RateLimits           *RateLimits `json:"rateLimits,omitempty"`
	NextRateLimitResetAt *int64      `json:"nextRateLimitResetAt,omitempty"`
	// RateLimitAvailable distinguishes a subscription whose quota endpoint has
	// not returned data yet from one whose windows are genuinely at 0% left.
	// The UI uses this flag to avoid presenting missing data as a fake 100% or a
	// confusing dash.
	RateLimitAvailable bool            `json:"rateLimitAvailable"`
	RateLimitError     string          `json:"rateLimitError,omitempty"`
	ThreadCount        int             `json:"threadCount"`
	Error              string          `json:"error,omitempty"`
	CreatedAt          int64           `json:"createdAt"`
	RawAccount         json.RawMessage `json:"-"`
}

type RouteReason struct {
	WeeklyUsedPercent    *float64 `json:"weeklyUsedPercent"`
	WeeklyResetsAt       *int64   `json:"weeklyResetsAt,omitempty"`
	ShortUsedPercent     *float64 `json:"shortUsedPercent"`
	BankedResetCount     *int     `json:"bankedResetCount,omitempty"`
	ResetCreditExpiresAt *int64   `json:"resetCreditExpiresAt,omitempty"`
	UrgencyScore         *float64 `json:"urgencyScore,omitempty"`
	FairShareScore       *float64 `json:"fairShareScore,omitempty"`
	NewThreadDispatches  uint64   `json:"newThreadDispatches,omitempty"`
	ThreadCount          int      `json:"threadCount"`
}

// LoginCancellation reports the only two safe outcomes of cancelling an
// additional-account browser login: the provisional account was discarded, or
// the login had already completed and the connected account was preserved.
type LoginCancellation struct {
	Canceled  bool             `json:"canceled"`
	Connected bool             `json:"connected"`
	Account   *AccountSnapshot `json:"account,omitempty"`
}

// PrimaryChange is returned by the control API after a Primary switch. The
// restart count lets the UI explain that the Router-owned Codex sessions were
// actually restarted, rather than merely changing a label in the menu.
type PrimaryChange struct {
	Account           AccountSnapshot `json:"account"`
	RestartedChildren int             `json:"restartedChildren"`
}

func (m *Multiplexer) Accounts(ctx context.Context) []AccountSnapshot {
	return m.accountSnapshots(ctx, true)
}

func (m *Multiplexer) accountSnapshots(ctx context.Context, includeProfile bool) []AccountSnapshot {
	accounts := m.store.Accounts()
	results := make(chan AccountSnapshot, len(accounts))
	for _, account := range accounts {
		go func(account state.Account) {
			snapshot, err := m.accountSnapshotWithProfile(ctx, account.ID, includeProfile)
			if err != nil {
				snapshot = AccountSnapshot{
					ID: account.ID, Label: account.Label, Enabled: account.Enabled,
					Controller: account.Controller, PendingLogin: account.PendingLogin,
					CreatedAt: account.CreatedAt, Error: err.Error(),
				}
			}
			results <- snapshot
		}(account)
	}
	snapshots := make([]AccountSnapshot, 0, len(accounts))
	for range accounts {
		select {
		case snapshot := <-results:
			snapshots = append(snapshots, snapshot)
		case <-ctx.Done():
			return snapshots
		}
	}
	sort.SliceStable(snapshots, func(i, j int) bool {
		if snapshots[i].Controller != snapshots[j].Controller {
			return snapshots[i].Controller
		}
		return snapshots[i].CreatedAt < snapshots[j].CreatedAt
	})
	return snapshots
}

func (m *Multiplexer) AddAccount(ctx context.Context, label string) (AccountSnapshot, error) {
	account, err := m.store.AddAccount(label)
	if err != nil {
		return AccountSnapshot{}, err
	}
	if _, err := m.startChild(ctx, account); err != nil {
		// The account was only provisioned locally and has not started a
		// sign-in flow. Roll it back so a failed app-server launch cannot leave
		// a permanent "Waiting for sign-in" row behind.
		if _, discardErr := m.store.DiscardProvisionalAccount(account.ID); discardErr == nil {
			if cleanupErr := m.removeIsolatedAccountHome(account); cleanupErr != nil {
				fmt.Fprintf(os.Stderr, "codex-mux: remove failed account home %s: %v\n", account.ID, cleanupErr)
			}
		}
		return AccountSnapshot{}, err
	}
	return m.accountSnapshot(ctx, account.ID)
}

func (m *Multiplexer) UpdateAccount(ctx context.Context, id string, label *string, enabled *bool) (AccountSnapshot, error) {
	if _, err := m.store.UpdateAccount(id, label, enabled); err != nil {
		return AccountSnapshot{}, err
	}
	return m.accountSnapshot(ctx, id)
}

// SetPrimary changes the Router's controller independently of the account
// currently selected by the native Codex application. It deliberately does
// not require quota capacity: a selected account may be depleted and should
// then fail over to another connected subscription for new work. The change
// also restarts every Router-owned Codex child so the active session observes
// the new controller immediately.
func (m *Multiplexer) SetPrimary(ctx context.Context, id string) (AccountSnapshot, error) {
	result, err := m.SetPrimaryAndRestart(ctx, id)
	return result.Account, err
}

// SetPrimaryAndRestart performs the Primary transition and reports how many
// isolated Codex app-server children were restarted. Keeping this separate
// from SetPrimary preserves the small state-oriented API used by tests and
// internal callers while allowing the HTTP UI to show a truthful completion
// message.
func (m *Multiplexer) SetPrimaryAndRestart(ctx context.Context, id string) (PrimaryChange, error) {
	m.accountMutationMu.Lock()
	defer m.accountMutationMu.Unlock()

	account, ok := m.store.Account(id)
	if !ok {
		return PrimaryChange{}, fmt.Errorf("account %q not found", id)
	}
	if !account.Enabled {
		return PrimaryChange{}, errors.New("the selected account is disabled")
	}
	snapshot, err := m.accountSnapshotWithProfile(ctx, id, false)
	if err != nil {
		return PrimaryChange{}, fmt.Errorf("read subscription before selecting Primary: %w", err)
	}
	if !snapshot.Connected || snapshot.AuthType != "chatgpt" {
		return PrimaryChange{}, errors.New("the selected account is not a connected ChatGPT subscription")
	}
	previous, hadPrevious := m.store.Controller()
	if hadPrevious && previous.ID == id {
		return PrimaryChange{Account: snapshot}, nil
	}
	if _, err := m.store.SetController(id); err != nil {
		return PrimaryChange{}, err
	}
	restarted, restartErr := m.restartChildrenLocked(ctx)
	if restartErr != nil {
		// Do not leave the persisted controller pointing at a session pool that
		// could not be rebuilt. Best-effort rollback keeps the previous working
		// choice when a child executable or initialization fails.
		if hadPrevious {
			_, _ = m.store.SetController(previous.ID)
			_, _ = m.restartChildrenLocked(ctx)
		}
		return PrimaryChange{}, fmt.Errorf("restart Router sessions after changing Primary: %w", restartErr)
	}
	// The pre-switch snapshot already came from the selected child. Update its
	// local controller bit instead of issuing another request after the child
	// restart; this keeps the response fast and avoids a transient reconnect
	// race being reported as a failed Primary change.
	updated := snapshot
	updated.Controller = true
	m.publish(Event{Type: "primary-changed", AccountID: id, Data: updated})
	m.publish(Event{Type: "router-restarted", AccountID: id, Message: "Router Codex sessions restarted"})
	return PrimaryChange{Account: updated, RestartedChildren: restarted}, nil
}

// RemoveAccount disconnects and removes a secondary subscription from Router
// state. The controller must be changed first, and accounts owning chats need
// an explicit force confirmation so the UI cannot silently orphan history.
func (m *Multiplexer) RemoveAccount(ctx context.Context, id string, force bool) (AccountSnapshot, error) {
	m.accountMutationMu.Lock()
	defer m.accountMutationMu.Unlock()

	account, ok := m.store.Account(id)
	if !ok {
		return AccountSnapshot{}, fmt.Errorf("account %q not found", id)
	}
	if account.Controller {
		return AccountSnapshot{}, errors.New("choose another Primary account before removing this account")
	}
	if len(m.store.Accounts()) <= 1 {
		return AccountSnapshot{}, errors.New("at least one subscription must remain")
	}
	if !force {
		owned := m.store.ThreadIDsForAccount(id)
		if len(owned) > 0 {
			return AccountSnapshot{}, fmt.Errorf(
				"account %q owns %d chat(s); confirm removal to continue", id, len(owned),
			)
		}
	}

	if child, exists := m.child(id); exists {
		if err := child.Close(); err != nil {
			return AccountSnapshot{}, fmt.Errorf("stop subscription %q: %w", id, err)
		}
		if err := child.Wait(ctx); err != nil {
			return AccountSnapshot{}, fmt.Errorf("wait for subscription %q to stop: %w", id, err)
		}
		m.removeChild(id, child)
	}
	removed, err := m.store.RemoveAccount(id, force)
	if err != nil {
		return AccountSnapshot{}, err
	}
	m.purgeAccountReferences(id)
	if err := m.removeIsolatedAccountHome(removed); err != nil {
		// The state entry and child are already gone. Keep the metadata removal
		// successful while reporting cleanup in the process log; importantly,
		// removeIsolatedAccountHome refuses the native .codex home.
		fmt.Fprintf(os.Stderr, "codex-mux: remove account home %s: %v\n", id, err)
	}
	m.publish(Event{Type: "account-removed", AccountID: id, Message: "Subscription removed"})
	return AccountSnapshot{ID: removed.ID, Label: removed.Label, Enabled: removed.Enabled, Controller: removed.Controller, CreatedAt: removed.CreatedAt}, nil
}

func (m *Multiplexer) ThreadAccount(ctx context.Context, threadID string) (AccountSnapshot, error) {
	accountID, ok := m.store.ThreadOwner(threadID)
	if !ok {
		return AccountSnapshot{}, fmt.Errorf("thread %q has no subscription assignment", threadID)
	}
	return m.accountSnapshotWithProfile(ctx, accountID, true)
}

func (m *Multiplexer) StartLogin(ctx context.Context, id, mode string) (json.RawMessage, error) {
	if mode != "chatgpt" && mode != "chatgptDeviceCode" {
		return nil, errors.New("login mode must be chatgpt or chatgptDeviceCode")
	}
	child, ok := m.child(id)
	if !ok {
		return nil, fmt.Errorf("account %q is unavailable", id)
	}
	params, _ := json.Marshal(map[string]any{"type": mode})
	response, err := child.Request(ctx, "account/login/start", params)
	if err != nil {
		return nil, err
	}
	if _, err := m.store.SetPendingLogin(id, loginIdentifier(response.Result)); err != nil {
		return nil, fmt.Errorf("persist pending sign-in: %w", err)
	}
	return response.Result, nil
}

// CancelLogin cancels only an unconnected secondary-account login. It first
// asks the official child app-server to cancel its browser flow, then re-reads
// account state to guard the completion/cancel race. A completed login is
// retained; otherwise the isolated child, state entry, and account home are
// removed together.
func (m *Multiplexer) CancelLogin(ctx context.Context, id, loginID string) (LoginCancellation, error) {
	account, ok := m.store.Account(id)
	if !ok {
		return LoginCancellation{}, fmt.Errorf("account %q not found", id)
	}
	if account.Controller {
		return LoginCancellation{}, errors.New("the primary account login cannot be cancelled here")
	}
	loginID = strings.TrimSpace(loginID)
	if loginID == "" {
		loginID = strings.TrimSpace(account.PendingLoginID)
	}
	if !account.PendingLogin && loginID == "" {
		return LoginCancellation{}, fmt.Errorf("account %q has no pending sign-in", id)
	}

	m.cancelMu.Lock()
	if _, cancelling := m.cancelling[id]; cancelling {
		m.cancelMu.Unlock()
		return LoginCancellation{}, fmt.Errorf("account %q sign-in cancellation is already in progress", id)
	}
	m.cancelling[id] = struct{}{}
	m.cancelMu.Unlock()
	defer func() {
		m.cancelMu.Lock()
		delete(m.cancelling, id)
		m.cancelMu.Unlock()
	}()

	snapshot, err := m.accountSnapshotWithProfile(ctx, id, false)
	if err != nil {
		return LoginCancellation{}, fmt.Errorf("read subscription before cancellation: %w", err)
	}
	if snapshot.Connected {
		return LoginCancellation{Connected: true, Account: &snapshot}, nil
	}

	child, ok := m.child(id)
	if !ok {
		return LoginCancellation{}, fmt.Errorf("account %q is unavailable", id)
	}
	if loginID != "" {
		params, _ := json.Marshal(map[string]string{"loginId": loginID})
		if _, err := child.Request(ctx, "account/login/cancel", params); err != nil {
			// A completed browser callback can make cancellation return an error.
			// Re-read once before surfacing it so a valid account is never discarded.
			if refreshed, readErr := m.accountSnapshotWithProfile(ctx, id, false); readErr == nil && refreshed.Connected {
				return LoginCancellation{Connected: true, Account: &refreshed}, nil
			}
			return LoginCancellation{}, fmt.Errorf("cancel browser sign-in: %w", err)
		}
	}

	// The successful cancel acknowledgement fences the official login flow.
	// Re-read immediately afterward in case the browser completed just before
	// the acknowledgement arrived.
	snapshot, err = m.accountSnapshotWithProfile(ctx, id, false)
	if err != nil {
		return LoginCancellation{}, fmt.Errorf("read subscription after cancellation: %w", err)
	}
	if snapshot.Connected {
		return LoginCancellation{Connected: true, Account: &snapshot}, nil
	}

	if err := child.Close(); err != nil {
		return LoginCancellation{}, fmt.Errorf("stop cancelled subscription: %w", err)
	}
	if err := child.Wait(ctx); err != nil {
		return LoginCancellation{}, fmt.Errorf("wait for cancelled subscription to stop: %w", err)
	}
	m.removeChild(id, child)

	removed, err := m.store.DiscardProvisionalAccount(id)
	if err != nil {
		return LoginCancellation{}, err
	}
	m.purgeAccountReferences(id)
	if err := m.removeIsolatedAccountHome(removed); err != nil {
		// Metadata is already safely gone, and the account can no longer be
		// selected. Keep a recoverable empty local directory rather than turn a
		// completed cancellation into an ambiguous UI failure.
		fmt.Fprintf(os.Stderr, "codex-mux: remove cancelled account home %s: %v\n", id, err)
	}
	m.publish(Event{Type: "account-removed", AccountID: id, Message: "Cancelled subscription sign-in"})
	return LoginCancellation{Canceled: true}, nil
}

func (m *Multiplexer) Logout(ctx context.Context, id string) error {
	child, ok := m.child(id)
	if !ok {
		return fmt.Errorf("account %q is unavailable", id)
	}
	_, err := child.Request(ctx, "account/logout", nil)
	return err
}

func (m *Multiplexer) accountSnapshot(ctx context.Context, accountID string) (AccountSnapshot, error) {
	return m.accountSnapshotWithProfile(ctx, accountID, true)
}

func (m *Multiplexer) accountSnapshotWithProfile(ctx context.Context, accountID string, includeProfile bool) (AccountSnapshot, error) {
	account, ok := m.store.Account(accountID)
	if !ok {
		return AccountSnapshot{}, fmt.Errorf("account %q not found", accountID)
	}
	child, ok := m.child(accountID)
	if !ok {
		return AccountSnapshot{}, fmt.Errorf("account %q app-server is unavailable", accountID)
	}
	params := json.RawMessage(`{"refreshToken":false}`)
	accountResponse, err := child.Request(ctx, "account/read", params)
	if err != nil {
		return AccountSnapshot{}, err
	}
	var accountResult struct {
		Account json.RawMessage `json:"account"`
	}
	if err := json.Unmarshal(accountResponse.Result, &accountResult); err != nil {
		return AccountSnapshot{}, fmt.Errorf("decode account response: %w", err)
	}
	snapshot := AccountSnapshot{
		ID: account.ID, Label: account.Label, Enabled: account.Enabled,
		Controller: account.Controller, PendingLogin: account.PendingLogin,
		Connected: string(accountResult.Account) != "null" && len(accountResult.Account) > 0,
		CreatedAt: account.CreatedAt, RawAccount: accountResult.Account,
		ThreadCount: m.store.ThreadCounts()[account.ID],
	}
	if snapshot.Connected && account.PendingLogin {
		// A successful callback may arrive while the app was closed, before the
		// account/login/completed notification is observed by the mux. Clear the
		// persisted intent from the authoritative account snapshot as well.
		if _, clearErr := m.store.ClearPendingLogin(account.ID); clearErr == nil {
			snapshot.PendingLogin = false
		}
	}
	if snapshot.Connected {
		var details struct {
			Type        string `json:"type"`
			Email       string `json:"email"`
			PlanType    string `json:"planType"`
			DisplayName string `json:"displayName"`
			Username    string `json:"username"`
		}
		_ = json.Unmarshal(accountResult.Account, &details)
		snapshot.AuthType = details.Type
		snapshot.Email = details.Email
		snapshot.PlanType = details.PlanType
		snapshot.PlanLabel = planLabel(details.PlanType)
		snapshot.DisplayName = strings.TrimSpace(details.DisplayName)
		snapshot.Username = strings.TrimSpace(details.Username)
		if includeProfile {
			identity := m.profileIdentity(ctx, account)
			snapshot.ProfileImageURL = identity.ImageURL
			if identity.DisplayName != "" {
				snapshot.DisplayName = identity.DisplayName
			}
			if identity.Username != "" {
				snapshot.Username = identity.Username
			}
		}
		if details.Type == "chatgpt" {
			rateResponse, rateErr := child.Request(ctx, "account/rateLimits/read", nil)
			if rateErr == nil {
				var rateResult struct {
					RateLimits RateLimits `json:"rateLimits"`
				}
				if json.Unmarshal(rateResponse.Result, &rateResult) == nil {
					snapshot.RateLimits = &rateResult.RateLimits
					snapshot.RateLimitAvailable = true
					snapshot.NextRateLimitResetAt = earliestRateLimitResetAt(snapshot.RateLimits)
				} else {
					snapshot.RateLimitError = "quota data could not be read"
				}
			} else {
				snapshot.RateLimitError = "quota data is temporarily unavailable"
			}
		}
	}
	m.applyRateLimitPreview(&snapshot)
	return snapshot, nil
}

func loginIdentifier(payload json.RawMessage) string {
	var result struct {
		LoginID      string `json:"loginId"`
		SnakeLoginID string `json:"login_id"`
	}
	if json.Unmarshal(payload, &result) != nil {
		return ""
	}
	if value := strings.TrimSpace(result.LoginID); value != "" {
		return value
	}
	return strings.TrimSpace(result.SnakeLoginID)
}

func planLabel(planType string) string {
	switch planType {
	case "free":
		return "Free"
	case "go":
		return "Go"
	case "plus":
		return "Plus"
	case "prolite":
		return "Pro 5x"
	case "pro":
		return "Pro 20x"
	case "team":
		return "Team"
	case "self_serve_business_prolite", "self_serve_business_usage_based", "business":
		return "Business"
	case "ent26", "enterprise_cbp_automation", "enterprise_cbp_usage_based", "enterprise":
		return "Enterprise"
	case "edu":
		return "Edu"
	default:
		return ""
	}
}

func earliestRateLimitResetAt(limits *RateLimits) *int64 {
	if limits == nil {
		return nil
	}
	var earliest *int64
	for _, window := range []*RateLimitWindow{limits.Primary, limits.Secondary} {
		if window == nil || window.ResetsAt == nil || *window.ResetsAt <= 0 {
			continue
		}
		if earliest == nil || *window.ResetsAt < *earliest {
			value := *window.ResetsAt
			earliest = &value
		}
	}
	return earliest
}

func (m *Multiplexer) chooseAccount(ctx context.Context) (state.Account, RouteReason, error) {
	snapshots := m.accountSnapshots(ctx, false)
	return m.chooseFairShareFromSnapshots(snapshots, nil)
}

func (m *Multiplexer) chooseAccountExcluding(ctx context.Context, excluded map[string]struct{}) (state.Account, RouteReason, error) {
	snapshots := m.accountSnapshots(ctx, false)
	return m.chooseAccountFromSnapshots(ctx, snapshots, excluded)
}

// chooseFairShareFromSnapshots selects a new-chat owner from the connected
// subscriptions that still have capacity. The selector is deliberately
// round-robin by dispatch count: quota percentage is used to decide whether an
// account can enter the pool, not to keep routing every new chat to the account
// with the lowest current usage. That keeps traffic spread across the pool and
// gives each subscription a chance to reach the same reset window. Accounts
// whose quota is temporarily unknown remain last-resort candidates and are
// never allowed to displace a connected account with known capacity.
func (m *Multiplexer) chooseFairShareFromSnapshots(
	snapshots []AccountSnapshot,
	excluded map[string]struct{},
) (state.Account, RouteReason, error) {
	type candidate struct {
		account       state.Account
		reason        RouteReason
		dispatchCount uint64
		knownCapacity bool
	}
	candidates := make([]candidate, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if _, skip := excluded[snapshot.ID]; skip || !accountHasCapacity(snapshot) {
			continue
		}
		account, ok := m.store.Account(snapshot.ID)
		if !ok {
			continue
		}
		reason := routeReasonForSnapshot(snapshot)
		candidates = append(candidates, candidate{
			account: account, reason: reason,
			knownCapacity: snapshot.RateLimits != nil && snapshot.RateLimitAvailable,
		})
	}
	if len(candidates) == 0 {
		return state.Account{}, RouteReason{}, errNoSubscriptionCapacity
	}

	// Read and increment counters under one lock so two simultaneous new-chat
	// requests cannot both observe the same tie-break value.
	m.allocationMu.Lock()
	defer m.allocationMu.Unlock()
	for index := range candidates {
		candidates[index].dispatchCount = m.newThreadDispatch[candidates[index].account.ID]
	}
	known := make([]candidate, 0, len(candidates))
	for _, entry := range candidates {
		if entry.knownCapacity {
			known = append(known, entry)
		}
	}
	if len(known) > 0 {
		candidates = known
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.dispatchCount != right.dispatchCount {
			return left.dispatchCount < right.dispatchCount
		}
		if left.reason.ThreadCount != right.reason.ThreadCount {
			return left.reason.ThreadCount < right.reason.ThreadCount
		}
		return left.account.CreatedAt < right.account.CreatedAt
	})
	selected := &candidates[0]
	m.newThreadDispatch[selected.account.ID]++
	score := float64(selected.dispatchCount)
	selected.reason.FairShareScore = &score
	selected.reason.NewThreadDispatches = selected.dispatchCount + 1
	return selected.account, selected.reason, nil
}

func (m *Multiplexer) chooseAccountFromSnapshots(ctx context.Context, snapshots []AccountSnapshot, excluded map[string]struct{}) (state.Account, RouteReason, error) {
	type candidate struct {
		account      state.Account
		reason       RouteReason
		weekly       *RateLimitWindow
		weeklyUsed   float64
		shortUsed    float64
		resetCredits resetCreditMetadata
		urgency      float64
	}
	candidates := make([]candidate, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if _, skip := excluded[snapshot.ID]; skip {
			continue
		}
		if !accountHasCapacity(snapshot) {
			continue
		}
		account, ok := m.store.Account(snapshot.ID)
		if !ok {
			continue
		}
		weekly, short := longestAndShortestWindow(snapshot.RateLimits)
		weeklyUsed := 1_000.0
		shortUsed := 1_000.0
		reason := routeReasonForSnapshot(snapshot)
		if weekly != nil {
			weeklyUsed = weekly.UsedPercent
		}
		if short != nil {
			shortUsed = short.UsedPercent
		}
		candidates = append(candidates, candidate{
			account: account, reason: reason, weekly: weekly,
			weeklyUsed: weeklyUsed, shortUsed: shortUsed,
		})
	}
	if len(candidates) == 0 {
		return state.Account{}, RouteReason{}, errNoSubscriptionCapacity
	}

	type resetResult struct {
		index    int
		metadata resetCreditMetadata
	}
	resetResults := make(chan resetResult, len(candidates))
	for index := range candidates {
		go func(index int, account state.Account) {
			resetResults <- resetResult{
				index: index, metadata: m.routingResetCredits(ctx, account),
			}
		}(index, candidates[index].account)
	}

collectResetCredits:
	for received := 0; received < len(candidates); received++ {
		select {
		case result := <-resetResults:
			candidates[result.index].resetCredits = result.metadata
		case <-ctx.Done():
			break collectResetCredits
		}
	}

	now := m.now()
	for index := range candidates {
		entry := &candidates[index]
		entry.urgency = routeUrgencyScore(now, entry.weekly, entry.resetCredits)
		urgency := entry.urgency
		entry.reason.UrgencyScore = &urgency
		if entry.resetCredits.Known {
			count := entry.resetCredits.AvailableCount
			entry.reason.BankedResetCount = &count
		}
		if entry.resetCredits.EarliestExpiry != nil {
			expiresAt := *entry.resetCredits.EarliestExpiry
			entry.reason.ResetCreditExpiresAt = &expiresAt
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if math.Abs(left.urgency-right.urgency) > 0.000001 {
			return left.urgency > right.urgency
		}
		if math.Abs(left.shortUsed-right.shortUsed) > 0.001 {
			return left.shortUsed < right.shortUsed
		}
		if math.Abs(left.weeklyUsed-right.weeklyUsed) > 0.001 {
			return left.weeklyUsed < right.weeklyUsed
		}
		if left.reason.ThreadCount != right.reason.ThreadCount {
			return left.reason.ThreadCount < right.reason.ThreadCount
		}
		return left.account.CreatedAt < right.account.CreatedAt
	})
	return candidates[0].account, candidates[0].reason, nil
}

func routeReasonForSnapshot(snapshot AccountSnapshot) RouteReason {
	weekly, short := longestAndShortestWindow(snapshot.RateLimits)
	reason := RouteReason{ThreadCount: snapshot.ThreadCount}
	if weekly != nil {
		value := weekly.UsedPercent
		reason.WeeklyUsedPercent = &value
		if weekly.ResetsAt != nil {
			resetsAt := *weekly.ResetsAt
			reason.WeeklyResetsAt = &resetsAt
		}
	}
	if short != nil {
		value := short.UsedPercent
		reason.ShortUsedPercent = &value
	}
	return reason
}

func routeUrgencyScore(now time.Time, weekly *RateLimitWindow, credits resetCreditMetadata) float64 {
	if weekly == nil {
		return -1
	}
	remaining := math.Max(0, math.Min(100, 100-weekly.UsedPercent))
	horizon := routingFallbackWindow
	if weekly.WindowDurationMins != nil && *weekly.WindowDurationMins > 0 {
		horizon = time.Duration(*weekly.WindowDurationMins) * time.Minute
	}
	if weekly.ResetsAt != nil {
		untilReset := time.Unix(*weekly.ResetsAt, 0).Sub(now)
		if untilReset > 0 {
			horizon = untilReset
		}
	}
	if horizon < routingMinimumWindow {
		horizon = routingMinimumWindow
	}
	urgency := remaining / horizon.Hours()
	if credits.Known && credits.AvailableCount > 0 {
		creditCount := min(credits.AvailableCount, routingResetBonusCreditCap)
		urgency *= 1 + float64(creditCount)*routingResetBonusPerCredit
	}
	return urgency
}

func (m *Multiplexer) AggregatedRateLimits(ctx context.Context) (*RateLimits, error) {
	limits, err := aggregateRateLimits(m.accountSnapshots(ctx, false))
	if err != nil {
		return nil, err
	}
	if preview := m.currentRateLimitPreview(); preview != nil && preview.Mode.isAllDepleted() {
		limits.RateLimitReachedType = "legacy_rate_limit_reached"
	}
	return limits, nil
}

func aggregateRateLimits(snapshots []AccountSnapshot) (*RateLimits, error) {
	primary := make([]*RateLimitWindow, 0, len(snapshots))
	secondary := make([]*RateLimitWindow, 0, len(snapshots))
	hasSubscription := false
	hasCapacity := false
	for _, snapshot := range snapshots {
		if !snapshot.Enabled || !snapshot.Connected || snapshot.AuthType != "chatgpt" {
			continue
		}
		hasSubscription = true
		if snapshot.RateLimits != nil {
			primary = append(primary, snapshot.RateLimits.Primary)
			secondary = append(secondary, snapshot.RateLimits.Secondary)
		}
		weekly, _ := longestAndShortestWindow(snapshot.RateLimits)
		if weekly == nil || weekly.UsedPercent < 100 {
			hasCapacity = true
		}
	}
	if !hasSubscription {
		return nil, errors.New("no enabled ChatGPT subscription is connected")
	}
	result := &RateLimits{
		Primary:   averageRateLimitWindow(primary),
		Secondary: averageRateLimitWindow(secondary),
	}
	if !hasCapacity {
		result.RateLimitReachedType = "rate_limit_reached"
	}
	return result, nil
}

func averageRateLimitWindow(windows []*RateLimitWindow) *RateLimitWindow {
	var used float64
	var count int
	var longestDuration *int64
	var earliestReset *int64
	for _, window := range windows {
		if window == nil {
			continue
		}
		used += window.UsedPercent
		count++
		if window.WindowDurationMins != nil &&
			(longestDuration == nil || *window.WindowDurationMins > *longestDuration) {
			value := *window.WindowDurationMins
			longestDuration = &value
		}
		if window.ResetsAt != nil && (earliestReset == nil || *window.ResetsAt < *earliestReset) {
			value := *window.ResetsAt
			earliestReset = &value
		}
	}
	if count == 0 {
		return nil
	}
	return &RateLimitWindow{
		UsedPercent:        used / float64(count),
		WindowDurationMins: longestDuration,
		ResetsAt:           earliestReset,
	}
}

func longestAndShortestWindow(limits *RateLimits) (*RateLimitWindow, *RateLimitWindow) {
	if limits == nil {
		return nil, nil
	}
	windows := make([]*RateLimitWindow, 0, 2)
	if limits.Primary != nil {
		windows = append(windows, limits.Primary)
	}
	if limits.Secondary != nil {
		windows = append(windows, limits.Secondary)
	}
	if len(windows) == 0 {
		return nil, nil
	}
	sort.SliceStable(windows, func(i, j int) bool {
		return duration(windows[i]) < duration(windows[j])
	})
	return windows[len(windows)-1], windows[0]
}

func duration(window *RateLimitWindow) int64 {
	if window.WindowDurationMins == nil {
		return 0
	}
	return *window.WindowDurationMins
}

func contextWithControlTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 20*time.Second)
}
