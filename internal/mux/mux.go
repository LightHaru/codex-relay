package mux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LightHaru/codex-relay/internal/backend"
	"github.com/LightHaru/codex-relay/internal/protocol"
	"github.com/LightHaru/codex-relay/internal/state"
)

const requestTimeout = 30 * time.Second

type Options struct {
	RealExecutable string
	RealArgs       []string
	Environment    []string
	// CompatibilityProfile is emitted by a reviewed installer profile. An
	// empty/unknown value deliberately disables cross-account history handoff.
	CompatibilityProfile string
	Store                *state.Store
	Output               io.Writer
}

type externalRoute struct {
	accountID       string
	method          string
	message         protocol.Message
	excluded        map[string]struct{}
	capacityRetries int
	reservationID   string
}

// activeTurnRoute remains live after the turn/start response. Codex reports
// quota failures asynchronously through an error notification and a failed
// turn/completed event, so the original request must stay available until the
// turn reaches a terminal state.
type activeTurnRoute struct {
	route                externalRoute
	attemptID            string
	generation           uint64
	sideEffectsStarted   bool
	visibleOutputStarted bool
}

type serverRequestRoute struct {
	accountID  string
	threadID   string
	attemptID  string
	generation uint64
	original   json.RawMessage
}

type Event struct {
	ID                string `json:"id,omitempty"`
	Type              string `json:"type"`
	ThreadID          string `json:"threadId,omitempty"`
	AttemptID         string `json:"logicalTurnId,omitempty"`
	AccountID         string `json:"accountId,omitempty"`
	PreviousAccountID string `json:"previousAccountId,omitempty"`
	RouteGeneration   uint64 `json:"routeGeneration,omitempty"`
	Timestamp         int64  `json:"timestamp"`
	Message           string `json:"message,omitempty"`
	Data              any    `json:"data,omitempty"`
}

// Multiplexer presents one app-server connection to ChatGPT.app while owning
// one real app-server process per ChatGPT subscription.
type Multiplexer struct {
	realExecutable       string
	realArgs             []string
	environment          []string
	compatibilityProfile string
	safeHandoff          bool
	store                *state.Store
	output               io.Writer

	childrenMu sync.RWMutex
	children   map[string]*backend.Child
	// accountMutationMu serializes destructive/account-controller transitions
	// so a simultaneous Primary switch, cancellation, or removal cannot leave
	// the child map and persisted state describing different worlds.
	accountMutationMu sync.Mutex
	asyncMu           sync.Mutex
	asyncClosed       bool
	asyncWG           sync.WaitGroup
	runtimeMu         sync.Mutex
	runtimeCancel     context.CancelFunc
	runtimeWG         sync.WaitGroup
	inbound           chan backend.Inbound
	cancelMu          sync.Mutex
	cancelling        map[string]struct{}

	initializationMu sync.RWMutex
	initializeParams json.RawMessage
	initialized      bool

	externalMu      sync.Mutex
	externalRoutes  map[string]externalRoute
	serverMu        sync.Mutex
	serverRoutes    map[string]serverRequestRoute
	serverSequence  atomic.Uint64
	turnMu          sync.Mutex
	activeTurns     map[string]activeTurnRoute
	failedTurnPeers map[string]map[string]struct{}
	outputMu        sync.Mutex
	canonicalMu     sync.Mutex
	eventsMu        sync.RWMutex
	events          map[chan Event]struct{}

	profileMu       sync.Mutex
	profileClient   *http.Client
	profileCache    map[string]profileCacheEntry
	usageEndpoint   string
	now             func() time.Time
	quotaMu         sync.RWMutex
	quotaSnapshots  map[string]AccountSnapshot
	usageQuotaCache map[string]usageQuotaCacheEntry

	resetCreditsMu       sync.Mutex
	resetCreditsCache    map[string]resetCreditsCacheEntry
	resetCreditsEndpoint string

	previewMu        sync.RWMutex
	rateLimitPreview *RateLimitPreview

	resetPreviewMu sync.RWMutex
	resetPreviews  map[string]ResetCreditsPreview
}

func New(options Options) (*Multiplexer, error) {
	if options.RealExecutable == "" || options.Store == nil || options.Output == nil {
		return nil, errors.New("real executable, store, and output are required")
	}
	compatibilityProfile := strings.TrimSpace(options.CompatibilityProfile)
	return &Multiplexer{
		realExecutable:       options.RealExecutable,
		realArgs:             append([]string(nil), options.RealArgs...),
		environment:          append([]string(nil), options.Environment...),
		compatibilityProfile: compatibilityProfile,
		safeHandoff:          compatibilityProfile != "" && !strings.EqualFold(compatibilityProfile, "unknown"),
		store:                options.Store,
		output:               options.Output,
		children:             make(map[string]*backend.Child),
		inbound:              make(chan backend.Inbound, 1024),
		cancelling:           make(map[string]struct{}),
		externalRoutes:       make(map[string]externalRoute),
		serverRoutes:         make(map[string]serverRequestRoute),
		activeTurns:          make(map[string]activeTurnRoute),
		failedTurnPeers:      make(map[string]map[string]struct{}),
		events:               make(map[chan Event]struct{}),
		profileClient:        &http.Client{Timeout: 10 * time.Second},
		profileCache:         make(map[string]profileCacheEntry),
		usageEndpoint:        usageStatusURL,
		now:                  time.Now,
		quotaSnapshots:       make(map[string]AccountSnapshot),
		usageQuotaCache:      make(map[string]usageQuotaCacheEntry),
		resetCreditsCache:    make(map[string]resetCreditsCacheEntry),
		resetCreditsEndpoint: rateLimitResetCreditsURL,
		resetPreviews:        make(map[string]ResetCreditsPreview),
	}, nil
}

func (m *Multiplexer) Start(ctx context.Context) error {
	runtimeCtx, runtimeCancel := context.WithCancel(ctx)
	m.runtimeMu.Lock()
	m.runtimeCancel = runtimeCancel
	m.runtimeMu.Unlock()
	if err := m.recoverInterruptedHandoffs(); err != nil {
		return fmt.Errorf("recover interrupted handoffs: %w", err)
	}
	if err := m.recoverInterruptedAttempts(); err != nil {
		return fmt.Errorf("recover interrupted attempts: %w", err)
	}
	// Explainability is a bounded projection. Compact any append-only v0.4.0
	// per-task decision files before accepting new work; failures remain
	// best-effort diagnostics and never prevent the authoritative state starting.
	m.compactCanonicalDecisionLedgers()
	for _, account := range m.store.Accounts() {
		if _, err := m.startChild(ctx, account); err != nil {
			fmt.Fprintf(os.Stderr, "codex-mux: start account %s: %v\n", account.ID, err)
		}
	}
	if len(m.childEntries()) == 0 {
		return errors.New("no Codex app-server process could be started")
	}
	m.runtimeWG.Add(2)
	go func() { defer m.runtimeWG.Done(); m.inboundLoop(runtimeCtx) }()
	go func() { defer m.runtimeWG.Done(); m.syncManagedConfigLoop(runtimeCtx) }()
	return nil
}

func (m *Multiplexer) syncManagedConfigLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.store.SyncManagedConfig(); err != nil {
				fmt.Fprintf(os.Stderr, "codex-mux: sync shared plugin config: %v\n", err)
			}
		}
	}
}

func (m *Multiplexer) Close() {
	m.runtimeMu.Lock()
	if m.runtimeCancel != nil {
		m.runtimeCancel()
	}
	m.runtimeMu.Unlock()
	m.asyncMu.Lock()
	m.asyncClosed = true
	m.asyncMu.Unlock()
	for _, entry := range m.childEntries() {
		_ = entry.child.Close()
	}
	m.runtimeWG.Wait()
	done := make(chan struct{})
	go func() { m.asyncWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

func (m *Multiplexer) runAsync(work func()) {
	if work == nil {
		return
	}
	m.asyncMu.Lock()
	if m.asyncClosed {
		m.asyncMu.Unlock()
		return
	}
	m.asyncWG.Add(1)
	m.asyncMu.Unlock()
	go func() { defer m.asyncWG.Done(); work() }()
}

// restartChildrenLocked restarts every Router-owned Codex app-server child
// while keeping the parent mux process (and its local control API) alive.
// Callers must hold accountMutationMu. The native Store Codex process is never
// part of m.children and therefore cannot be touched by this operation.
func (m *Multiplexer) restartChildrenLocked(ctx context.Context) (int, error) {
	entries := m.childEntries()
	if len(entries) == 0 {
		return 0, nil
	}

	var restartErrors []error
	for _, entry := range entries {
		if err := entry.child.Close(); err != nil {
			restartErrors = append(restartErrors, fmt.Errorf("stop %s: %w", entry.account.ID, err))
		}
	}

	waitCtx, cancelWait := context.WithTimeout(ctx, 25*time.Second)
	defer cancelWait()
	for _, entry := range entries {
		if err := entry.child.Wait(waitCtx); err != nil {
			restartErrors = append(restartErrors, fmt.Errorf("wait for %s: %w", entry.account.ID, err))
			continue
		}
		m.removeChild(entry.account.ID, entry.child)
	}
	if len(restartErrors) > 0 {
		return 0, errors.Join(restartErrors...)
	}

	accounts := m.store.Accounts()
	started := 0
	startCtx, cancelStart := context.WithTimeout(ctx, 25*time.Second)
	defer cancelStart()
	for _, account := range accounts {
		if _, err := m.startChild(startCtx, account); err != nil {
			restartErrors = append(restartErrors, fmt.Errorf("start %s: %w", account.ID, err))
			continue
		}
		started++
	}
	if len(restartErrors) > 0 {
		return started, errors.Join(restartErrors...)
	}
	return started, nil
}

// restartIdleChildForHandoff releases stale rollout handles held by one
// Router-owned target app-server. It is allowed only when that subscription
// has no active turn, and it never addresses the native Store Codex process.
func (m *Multiplexer) restartIdleChildForHandoff(ctx context.Context, account state.Account) (*backend.Child, error) {
	m.accountMutationMu.Lock()
	defer m.accountMutationMu.Unlock()
	if m.accountHasActiveTurn(account.ID) {
		return nil, errors.New("target subscription has another active turn")
	}
	child, ok := m.child(account.ID)
	if !ok {
		return m.startChild(ctx, account)
	}
	if err := child.Close(); err != nil {
		return nil, fmt.Errorf("stop target app-server: %w", err)
	}
	waitCtx, cancelWait := context.WithTimeout(ctx, 15*time.Second)
	defer cancelWait()
	if err := child.Wait(waitCtx); err != nil {
		return nil, fmt.Errorf("wait for target app-server: %w", err)
	}
	m.removeChild(account.ID, child)
	startCtx, cancelStart := context.WithTimeout(ctx, 15*time.Second)
	defer cancelStart()
	restarted, err := m.startChild(startCtx, account)
	if err != nil {
		return nil, fmt.Errorf("restart target app-server: %w", err)
	}
	return restarted, nil
}

func (m *Multiplexer) HandleClient(message protocol.Message) {
	if message.Method == "" && len(message.ID) > 0 {
		m.handleServerRequestResponse(message)
		return
	}
	if message.Method == "initialize" && len(message.ID) > 0 {
		m.runAsync(func() { m.initialize(message) })
		return
	}
	if len(message.ID) == 0 {
		m.handleClientNotification(message)
		return
	}

	switch message.Method {
	case "thread/list":
		m.runAsync(func() { m.aggregateThreadList(message) })
	case "thread/start":
		m.runAsync(func() { m.routeNewThread(message) })
	case "account/rateLimits/read":
		m.runAsync(func() { m.routeAggregatedRateLimits(message) })
	default:
		m.routeExistingRequest(message)
	}
}

func (m *Multiplexer) initialize(message protocol.Message) {
	m.initializationMu.Lock()
	m.initializeParams = append(json.RawMessage(nil), message.Params...)
	m.initializationMu.Unlock()

	// Initialize every account concurrently. A disconnected or slow account
	// must not hold the native desktop handshake behind one 30-second timeout
	// per subscription; the Router can still present the first successful
	// account while the remaining children finish their own handshake.
	entries := m.childEntries()
	type initializationResult struct {
		result json.RawMessage
		err    error
	}
	results := make(chan initializationResult, len(entries))
	for _, entry := range entries {
		go func(entry childEntry) {
			ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
			response, err := entry.child.Request(ctx, "initialize", message.Params)
			cancel()
			results <- initializationResult{result: response.Result, err: err}
		}(entry)
	}

	var firstResult json.RawMessage
	var firstErr error
	for range entries {
		outcome := <-results
		if outcome.err != nil {
			if firstErr == nil {
				firstErr = outcome.err
			}
			continue
		}
		if firstResult == nil {
			firstResult = outcome.result
		}
	}
	if firstResult == nil {
		m.write(protocol.Failure(message.ID, -32000, fmt.Sprintf("failed to initialize account pool: %v", firstErr)))
		return
	}
	m.write(protocol.Success(message.ID, firstResult))
}

func (m *Multiplexer) handleClientNotification(message protocol.Message) {
	if message.Method == "initialized" {
		m.initializationMu.Lock()
		m.initialized = true
		m.initializationMu.Unlock()
		for _, entry := range m.childEntries() {
			_ = entry.child.Send(message)
		}
		return
	}
	if threadID := threadIDFromAnyParams(message.Params); threadID != "" {
		m.turnMu.Lock()
		active, activeOK := m.activeTurns[threadID]
		m.turnMu.Unlock()
		accountID := ""
		if activeOK {
			accountID = active.route.accountID
		}
		if accountID == "" {
			accountID, _ = m.store.ThreadOwner(threadID)
		}
		if child, ok := m.child(accountID); ok {
			_ = child.Send(message)
		}
		return
	}
	if activeBoundMethod(message.Method) {
		accountID := m.activeAccountForUnscoped()
		if child, ok := m.child(accountID); ok {
			_ = child.Send(message)
			return
		}
	}
	if controller, ok := m.controllerChild(); ok {
		_ = controller.Send(message)
	}
}

func (m *Multiplexer) routeNewThread(message protocol.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	reservationID := "request-" + protocol.RequestIDKey(message.ID)
	account, reason, err := m.chooseAccountReserved(ctx, nil, reservationID)
	if err != nil {
		if errors.Is(err, errNoSubscriptionCapacity) {
			m.write(m.allSubscriptionsDepleted(ctx, message.ID))
			return
		}
		m.write(protocol.Failure(message.ID, -32020, err.Error()))
		return
	}
	if err := m.forward(account.ID, message); err != nil {
		_ = m.store.RollbackReservation(reservationID)
		m.write(protocol.Failure(message.ID, -32021, err.Error()))
		return
	}
	m.recordRoutingDecision("", "", account.ID, "new chat selected by persistent quota scheduler")
	m.publish(Event{
		Type:      "thread-routed",
		AccountID: account.ID,
		Message:   fmt.Sprintf("New chat pinned to %s", account.Label),
		Data:      reason,
	})
}

func (m *Multiplexer) routeExistingRequest(message protocol.Message) {
	accountID := ""
	if scopedAccountID, cleanedParams, ok := scopedPluginRequest(message.Method, message.Params); ok {
		if account, exists := m.store.Account(scopedAccountID); exists && account.Enabled {
			message.Params = cleanedParams
			if err := m.forward(scopedAccountID, message); err != nil {
				m.write(protocol.Failure(message.ID, -32023, err.Error()))
			}
			return
		}
	}
	threadID := threadIDFromParams(message.Params)
	if threadID != "" {
		accountID, _ = m.store.ThreadOwner(threadID)
	}
	if message.Method == "thread/resume" && threadID != "" {
		// A chat created before Relay was installed may have no persisted owner,
		// or a previous failover may have left the mapping pointing at a home
		// that does not contain the rollout. Prefer the account whose managed
		// history actually contains this thread before falling back to the
		// selected Router Primary.
		accountID = m.accountForThreadResume(threadID, accountID)
	}
	if accountID == "" && activeBoundMethod(message.Method) {
		accountID = m.activeAccountForUnscoped()
	}
	if accountID == "" {
		if controller, ok := m.store.Controller(); ok {
			accountID = controller.ID
		}
	}
	if accountID == "" {
		m.write(protocol.Failure(message.ID, -32022, "no controller account is configured"))
		return
	}
	if message.Method == "thread/resume" && threadID != "" {
		// A legacy rollout can still be referenced by an account's SQLite row
		// while the JSONL itself lives in the former native Store home. Copy that
		// one rollout into the selected Relay account before forwarding resume;
		// this keeps the credentials and future writes account-local while making
		// old chats resumable without asking the native Store child to serve them.
		m.runAsync(func() { m.routeThreadResume(message, threadID, accountID) })
		return
	}
	if message.Method == "turn/start" && threadID != "" {
		m.runAsync(func() { m.routeTurnStart(message, threadID, accountID) })
		return
	}
	if err := m.forward(accountID, message); err != nil {
		m.write(protocol.Failure(message.ID, -32023, err.Error()))
	}
}

func activeBoundMethod(method string) bool {
	lower := strings.ToLower(method)
	return strings.HasPrefix(method, "turn/") || strings.HasPrefix(method, "item/") ||
		strings.HasPrefix(method, "hook/") || strings.Contains(lower, "approval") ||
		strings.Contains(lower, "cancel") || strings.Contains(lower, "interrupt") || strings.Contains(lower, "tool")
}

func (m *Multiplexer) activeAccountForUnscoped() string {
	m.turnMu.Lock()
	defer m.turnMu.Unlock()
	accountID := ""
	for _, active := range m.activeTurns {
		if accountID != "" && accountID != active.route.accountID {
			return ""
		}
		accountID = active.route.accountID
	}
	return accountID
}

func (m *Multiplexer) accountForThreadResume(threadID, mappedAccountID string) string {
	if mappedAccountID != "" {
		if account, exists := m.store.Account(mappedAccountID); exists && account.Enabled {
			if _, found := findThreadHistory(account.CodexHome, threadID); found {
				return mappedAccountID
			}
		}
	}
	for _, account := range m.store.Accounts() {
		if !account.Enabled {
			continue
		}
		if _, found := findThreadHistory(account.CodexHome, threadID); found {
			return account.ID
		}
	}
	if account, exists := m.store.Account(mappedAccountID); exists && account.Enabled {
		return account.ID
	}
	return ""
}

func (m *Multiplexer) routeThreadResume(message protocol.Message, threadID, accountID string) {
	if err := m.ensureThreadHistoryOnAccount(threadID, accountID); err != nil {
		m.write(protocol.Failure(message.ID, -32027, fmt.Sprintf("prepare chat history: %v", err)))
		return
	}
	if err := m.forward(accountID, message); err != nil {
		m.write(protocol.Failure(message.ID, -32023, err.Error()))
	}
}

// ensureThreadHistoryOnAccount migrates a single rollout from the old native
// Store home when the Router metadata still points at that chat but the
// selected Relay account has no local copy. It deliberately does not inspect
// auth.json, config.toml, or any other credential/configuration file.
func (m *Multiplexer) ensureThreadHistoryOnAccount(threadID, accountID string) error {
	account, ok := m.store.Account(accountID)
	if !ok || !account.Enabled {
		return fmt.Errorf("account %q is unavailable", accountID)
	}
	if _, found := findThreadHistory(account.CodexHome, threadID); found {
		return nil
	}
	legacyHome := m.store.LegacyPrimaryCodexHome()
	if strings.TrimSpace(legacyHome) == "" || samePath(account.CodexHome, legacyHome) {
		return nil
	}
	legacyPath, found := findThreadHistory(legacyHome, threadID)
	if !found {
		return nil
	}
	if !m.safeHandoff {
		return m.safeHandoffUnavailableError()
	}
	if err := copyThreadHistory(legacyHome, account.CodexHome, legacyPath); err != nil {
		return fmt.Errorf("copy legacy chat history: %w", err)
	}
	return nil
}

func (m *Multiplexer) forward(accountID string, message protocol.Message) error {
	return m.forwardWithExclusions(accountID, message, nil)
}

func (m *Multiplexer) forwardWithExclusions(accountID string, message protocol.Message, excluded map[string]struct{}) error {
	return m.forwardRoute(accountID, message, excluded, 0)
}

// forwardRoute registers an external request before sending it to a child.
// capacityRetries is carried with the route so a transient model-capacity
// response can be retried without changing the original request payload or
// accidentally creating an unbounded retry loop.
func (m *Multiplexer) forwardRoute(
	accountID string,
	message protocol.Message,
	excluded map[string]struct{},
	capacityRetries int,
) error {
	child, ok := m.child(accountID)
	if !ok {
		return fmt.Errorf("account %s is unavailable", accountID)
	}
	key := protocol.RequestIDKey(message.ID)
	reservationID := ""
	if message.Method == "thread/start" {
		reservationID = "request-" + key
		reservation, exists := m.store.Scheduler().Reservations[reservationID]
		if !exists {
			reservation = state.Reservation{ID: reservationID, AccountID: accountID, Weight: 1, ExpiresAt: time.Now().Add(2 * time.Minute).UnixMilli()}
		}
		reservation.AccountID = accountID
		reservation.ExpiresAt = time.Now().Add(2 * time.Minute).UnixMilli()
		if err := m.store.PutReservation(reservation); err != nil {
			return fmt.Errorf("reserve new chat capacity: %w", err)
		}
	}
	m.externalMu.Lock()
	m.externalRoutes[key] = externalRoute{
		accountID:       accountID,
		method:          message.Method,
		message:         message,
		excluded:        cloneAccountSet(excluded),
		capacityRetries: capacityRetries,
		reservationID:   reservationID,
	}
	m.externalMu.Unlock()
	if message.Method == "turn/start" {
		if threadID := threadIDFromParams(message.Params); threadID != "" {
			m.rememberActiveTurn(threadID, externalRoute{
				accountID:       accountID,
				method:          message.Method,
				message:         message,
				excluded:        cloneAccountSet(excluded),
				capacityRetries: capacityRetries,
			})
		}
	}
	if err := child.Send(message); err != nil {
		m.externalMu.Lock()
		delete(m.externalRoutes, key)
		m.externalMu.Unlock()
		if reservationID != "" {
			_ = m.store.RollbackReservation(reservationID)
		}
		if message.Method == "turn/start" {
			m.removeActiveTurn(threadIDFromParams(message.Params), accountID)
		}
		return err
	}
	return nil
}

func (m *Multiplexer) rememberActiveTurn(threadID string, route externalRoute) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	m.turnMu.Lock()
	active := m.activeTurns[threadID]
	active.route = route
	if active.attemptID == "" {
		if threadRoute, ok := m.store.ThreadRoute(threadID); ok && threadRoute.ActiveAttemptID != "" {
			active.attemptID = threadRoute.ActiveAttemptID
			active.generation = threadRoute.Generation
			if attempt, found := m.store.TurnAttempt(active.attemptID); found {
				active.sideEffectsStarted = attempt.SideEffectsStarted
				active.visibleOutputStarted = attempt.FirstVisibleOutputAt != 0
			}
		}
	}
	m.activeTurns[threadID] = active
	m.turnMu.Unlock()
}

func (m *Multiplexer) removeActiveTurn(threadID, accountID string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	m.turnMu.Lock()
	if route, ok := m.activeTurns[threadID]; ok && (accountID == "" || route.route.accountID == accountID) {
		delete(m.activeTurns, threadID)
	}
	m.turnMu.Unlock()
}

// takeActiveTurnForFailure atomically claims a route for the first quota
// failure notification. Codex can emit both an error event and a failed
// turn/completed event; only the first one may trigger migration.
func (m *Multiplexer) takeActiveTurnForFailure(threadID, accountID string) (activeTurnRoute, bool) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || accountID == "" {
		return activeTurnRoute{}, false
	}
	m.turnMu.Lock()
	defer m.turnMu.Unlock()
	route, ok := m.activeTurns[threadID]
	if !ok || route.route.accountID != accountID {
		return activeTurnRoute{}, false
	}
	if peers := m.failedTurnPeers[threadID]; peers != nil {
		if _, suppressed := peers[accountID]; suppressed {
			return activeTurnRoute{}, false
		}
	}
	delete(m.activeTurns, threadID)
	peers := m.failedTurnPeers[threadID]
	if peers == nil {
		peers = make(map[string]struct{})
		m.failedTurnPeers[threadID] = peers
	}
	peers[accountID] = struct{}{}
	if route.attemptID != "" {
		if attempt, found := m.store.TurnAttempt(route.attemptID); found {
			attempt.Phase = "MIGRATING"
			attempt.UpdatedAt = time.Now().UnixMilli()
			_ = m.store.PutTurnAttempt(attempt)
		}
	}
	return route, true
}

func (m *Multiplexer) suppressFailedTurnEvent(threadID, accountID string) bool {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || accountID == "" {
		return false
	}
	m.turnMu.Lock()
	defer m.turnMu.Unlock()
	peers := m.failedTurnPeers[threadID]
	if peers == nil {
		return false
	}
	_, ok := peers[accountID]
	return ok
}

func (m *Multiplexer) completeActiveTurn(threadID, accountID string, completedTurnID ...string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	m.turnMu.Lock()
	var completed activeTurnRoute
	if route, ok := m.activeTurns[threadID]; ok && route.route.accountID == accountID {
		completed = route
		delete(m.activeTurns, threadID)
		delete(m.failedTurnPeers, threadID)
	}
	m.turnMu.Unlock()
	if completed.attemptID != "" {
		m.finishTurnAttempt(threadID, completed, "COMPLETED", "")
	}
	if route, ok := m.store.ThreadRoute(threadID); ok && route.AccountID == accountID {
		if len(completedTurnID) > 0 {
			route.LastCompletedTurnID = strings.TrimSpace(completedTurnID[0])
		}
		route.LastCompletedTurnAt = time.Now().UnixMilli()
		route.LastCompletedAccountID = accountID
		route.FirstTurnPending = false
		route.ConsecutiveOwnerTurns++
		route.CurrentState = "idle"
		_ = m.store.PutThreadRoute(route)
		_ = m.persistCanonicalSnapshot(threadID)
		m.publish(Event{Type: "turn-completed", ThreadID: threadID, AttemptID: completed.attemptID, AccountID: accountID, RouteGeneration: route.Generation})
		m.scheduleQuotaAttribution(threadID, accountID, completed.attemptID)
	}
}

func (m *Multiplexer) routeAggregatedRateLimits(message protocol.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	rateLimits, err := m.AggregatedRateLimits(ctx)
	if err != nil {
		m.write(protocol.Failure(message.ID, -32024, err.Error()))
		return
	}
	result, err := json.Marshal(map[string]any{"rateLimits": rateLimits})
	if err != nil {
		m.write(protocol.Failure(message.ID, -32025, err.Error()))
		return
	}
	m.write(protocol.Success(message.ID, result))
}

func (m *Multiplexer) routeTurnStart(message protocol.Message, threadID, ownerID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
	defer cancel()
	active, err := m.beginTurnAttempt(threadID, ownerID, message)
	if err != nil {
		m.write(protocol.Failure(message.ID, -32029, err.Error()))
		return
	}
	rollbackDispatch := func() {
		m.rollbackTurnReservation(threadID, active.attemptID, active.route.accountID, active.generation, "dispatch_not_committed")
	}
	policy := m.effectiveRoutingPolicy()
	threadRoute, routeExists := m.store.ThreadRoute(threadID)
	firstTurnPending := routeExists && threadRoute.FirstTurnPending
	// A newly created thread has no authoritative rollout until its first turn
	// has completed. Keep that first turn on the worker that accepted
	// thread/start; otherwise a balanced/rotate selection can try to migrate a
	// history file that does not exist yet. Subsequent turns are safe routing
	// boundaries because the completed first turn has made a canonical rollout
	// available for verification and handoff.
	canRebalanceCompletedThread := !firstTurnPending
	if policy != state.RoutingPolicySticky && canRebalanceCompletedThread {
		var selected state.Account
		var selectErr error
		if policy == state.RoutingPolicyRotate {
			selected, _, selectErr = m.chooseAccountReserved(ctx, map[string]struct{}{ownerID: {}}, active.attemptID)
		} else {
			selected, _, selectErr = m.chooseAccountReserved(ctx, nil, active.attemptID)
		}
		if selectErr == nil && selected.ID != "" && selected.ID != ownerID {
			handoffReason := "handoff_balanced_boundary"
			if policy == state.RoutingPolicyRotate {
				handoffReason = "handoff_rotate_boundary"
			}
			if err := m.resumeThreadOnAccount(ctx, threadID, ownerID, selected.ID, handoffReason); err != nil {
				rollbackDispatch()
				m.failTurnAttempt(threadID, err)
				m.write(protocol.Failure(message.ID, -32027, fmt.Sprintf("move chat to %s: %v", selected.Label, err)))
				return
			}
			ownerID = selected.ID
			route, _ := m.store.ThreadRoute(threadID)
			if err := m.moveTurnAttempt(threadID, ownerID, route.Generation); err != nil {
				rollbackDispatch()
				m.failTurnAttempt(threadID, err)
				m.write(protocol.Failure(message.ID, -32028, err.Error()))
				return
			}
		}
	}
	currentRoute, _ := m.store.ThreadRoute(threadID)
	if err := m.moveTurnAttempt(threadID, ownerID, currentRoute.Generation); err != nil {
		rollbackDispatch()
		m.failTurnAttempt(threadID, err)
		m.write(protocol.Failure(message.ID, -32028, err.Error()))
		return
	}
	snapshot, err := m.accountSnapshotWithProfile(ctx, ownerID, false)
	if err == nil && accountHasCapacity(snapshot) {
		m.recordAttemptQuotaBefore(threadID, snapshot)
		if err := m.forward(ownerID, message); err != nil {
			rollbackDispatch()
			m.failTurnAttempt(threadID, err)
			m.write(protocol.Failure(message.ID, -32023, err.Error()))
		} else {
			reason := "turn routed at a completed-turn boundary"
			if firstTurnPending {
				reason = "first turn stayed with the worker that created the task"
			}
			m.recordRoutingDecision(threadID, "", ownerID, reason)
			route, _ := m.store.ThreadRoute(threadID)
			m.publish(Event{Type: "turn-routed", ThreadID: threadID, AttemptID: route.ActiveAttemptID, AccountID: ownerID, RouteGeneration: route.Generation})
		}
		return
	}
	// The first selection never reached the worker. Remove its exact charge
	// before selecting a failover candidate with the same logical-turn ID.
	rollbackDispatch()
	excluded := map[string]struct{}{ownerID: {}}
	m.failoverTurn(ctx, message, threadID, ownerID, excluded)
}

func (m *Multiplexer) failoverTurn(
	ctx context.Context,
	message protocol.Message,
	threadID string,
	sourceAccountID string,
	excluded map[string]struct{},
) {
	activeRoute, routeFound := m.store.ThreadRoute(threadID)
	reservationID := ""
	if routeFound {
		reservationID = activeRoute.ActiveAttemptID
	}
	rollbackDispatch := func() {
		if reservationID != "" {
			m.rollbackTurnReservation(threadID, reservationID, sourceAccountID, activeRoute.Generation, "dispatch_not_committed")
		}
	}
	if !m.safeHandoff {
		err := m.safeHandoffUnavailableError()
		rollbackDispatch()
		m.failTurnAttempt(threadID, err)
		m.write(protocol.Failure(message.ID, -32030, err.Error()))
		return
	}
	fallback, _, err := m.chooseAccountReserved(ctx, excluded, reservationID)
	if err != nil {
		rollbackDispatch()
		m.failTurnAttempt(threadID, err)
		m.write(m.allSubscriptionsDepleted(ctx, message.ID))
		return
	}
	if err := m.resumeThreadOnAccount(ctx, threadID, sourceAccountID, fallback.ID, "handoff_quota_exhausted"); err != nil {
		rollbackDispatch()
		m.failTurnAttempt(threadID, err)
		m.write(protocol.Failure(message.ID, -32027, fmt.Sprintf("move chat to %s: %v", fallback.Label, err)))
		return
	}
	if err := m.store.SetThreadOwner(threadID, fallback.ID); err != nil {
		rollbackDispatch()
		m.failTurnAttempt(threadID, err)
		m.write(protocol.Failure(message.ID, -32028, err.Error()))
		return
	}
	committedRoute, _ := m.store.ThreadRoute(threadID)
	if err := m.moveTurnAttempt(threadID, fallback.ID, committedRoute.Generation); err != nil {
		rollbackDispatch()
		m.failTurnAttempt(threadID, err)
		m.write(protocol.Failure(message.ID, -32028, err.Error()))
		return
	}
	if snapshot, snapshotErr := m.accountSnapshotWithProfile(ctx, fallback.ID, false); snapshotErr == nil {
		m.recordAttemptQuotaBefore(threadID, snapshot)
	}
	if err := m.forwardWithExclusions(fallback.ID, message, excluded); err != nil {
		rollbackDispatch()
		m.failTurnAttempt(threadID, err)
		m.write(protocol.Failure(message.ID, -32023, err.Error()))
		return
	}
	m.recordRoutingDecision(threadID, sourceAccountID, fallback.ID, "owner unavailable or depleted before turn start")
	m.publish(Event{
		Type:      "thread-failed-over",
		AccountID: fallback.ID,
		Message:   fmt.Sprintf("Chat continued with %s", fallback.Label),
		Data:      map[string]any{"threadId": threadID, "previousAccountId": sourceAccountID},
	})
}

// failoverCompletedTurnAfterUsageLimit handles app-server-owned continuations
// (for example an active Goal) that did not originate from a renderer
// turn/start. The failed turn is already terminal, so there is no request to
// replay. Relay instead transfers the exact completed rollout plus goal state
// and lets the target app-server continue from that durable boundary.
func (m *Multiplexer) failoverCompletedTurnAfterUsageLimit(threadID, sourceAccountID string, failureRaw []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*requestTimeout)
	defer cancel()
	route, ok := m.store.ThreadRoute(threadID)
	if !ok || route.AccountID != sourceAccountID {
		// Ownership already advanced while this terminal event was queued. The
		// stale source notification must not overwrite the new owner's UI state.
		return
	}
	m.turnMu.Lock()
	_, active := m.activeTurns[threadID]
	m.turnMu.Unlock()
	if active {
		// A renderer-owned route won the race and owns retry semantics.
		return
	}

	excluded := map[string]struct{}{sourceAccountID: {}}
	reservationID := fmt.Sprintf("terminal-quota-%s-%d", threadID, m.serverSequence.Add(1))
	defer func() { _ = m.store.ReleaseReservation(reservationID) }()
	var migrationFailures []string
	for attempts := 0; attempts < len(m.store.Accounts()); attempts++ {
		fallback, _, err := m.chooseAccountReserved(ctx, excluded, reservationID)
		if err != nil {
			break
		}
		if err := m.resumeThreadOnAccount(ctx, threadID, sourceAccountID, fallback.ID, "handoff_quota_exhausted"); err != nil {
			excluded[fallback.ID] = struct{}{}
			_, safe := sanitizeRoutingFailure(err.Error())
			migrationFailures = append(migrationFailures, fallback.Label+": "+safe)
			continue
		}
		m.recordRoutingDecision(threadID, sourceAccountID, fallback.ID, "terminal quota failure moved the task at a completed-turn boundary")
		m.publish(Event{
			Type: "thread-failed-over", ThreadID: threadID,
			AccountID: fallback.ID, PreviousAccountID: sourceAccountID,
			Message: fmt.Sprintf("Task continued with %s after quota recovery", fallback.Label),
			Data:    map[string]any{"threadId": threadID, "previousAccountId": sourceAccountID, "autonomous": true},
		})
		m.publishAccountRefresh(fallback.ID)
		return
	}

	_ = m.store.RollbackReservation(reservationID)
	if len(migrationFailures) > 0 {
		if current, found := m.store.ThreadRoute(threadID); found && current.AccountID == sourceAccountID {
			now := time.Now()
			if m.now != nil {
				now = m.now()
			}
			current.RecoveryRequired = true
			current.CurrentState = "recovery_required"
			current.UpdatedAt = now.UnixMilli()
			_ = m.store.PutThreadRoute(current)
			_ = m.persistCanonicalSnapshot(threadID)
		}
		m.publish(Event{
			Type: "thread-recovery-required", ThreadID: threadID, AccountID: sourceAccountID,
			Message: strings.Join(migrationFailures, "; "),
		})
	}
	// If every subscription is depleted (or migration could not be proven),
	// preserve the native terminal signal so the app can show its usual retry
	// state. No prompt/tool request is replayed on an uncertain boundary.
	m.writeRaw(failureRaw)
}

func (m *Multiplexer) resumeThreadOnAccount(ctx context.Context, threadID, sourceAccountID, targetAccountID string, requestedReasonCode ...string) error {
	if !m.safeHandoff {
		return m.safeHandoffUnavailableError()
	}
	if m.hasPendingServerRequest(threadID, sourceAccountID) {
		return errors.New("source task still has a pending approval or tool callback")
	}
	target, ok := m.child(targetAccountID)
	if !ok {
		return fmt.Errorf("target subscription is unavailable")
	}
	sourceAccount, ok := m.store.Account(sourceAccountID)
	if !ok {
		return fmt.Errorf("source subscription %q is unavailable", sourceAccountID)
	}
	targetAccount, ok := m.store.Account(targetAccountID)
	if !ok {
		return fmt.Errorf("target subscription %q is unavailable", targetAccountID)
	}
	source, _ := m.child(sourceAccountID)
	goal, err := readTransferableThreadGoal(ctx, source, threadID)
	if err != nil {
		return err
	}
	resumeInfo, err := m.loadThreadResumeInfo(ctx, threadID, sourceAccount, targetAccount)
	if err != nil {
		return fmt.Errorf("read existing chat: %w", err)
	}
	route, routeExists := m.store.ThreadRoute(threadID)
	if !routeExists {
		route = state.ThreadRoute{ThreadID: threadID, AccountID: sourceAccountID, Generation: 1}
	}
	if route.AccountID != sourceAccountID {
		return fmt.Errorf("source generation changed: route belongs to %q", route.AccountID)
	}
	reasonCode := "handoff_balanced_boundary"
	if m.effectiveRoutingPolicy() == state.RoutingPolicyRotate {
		reasonCode = "handoff_rotate_boundary"
	}
	if len(requestedReasonCode) > 0 && strings.TrimSpace(requestedReasonCode[0]) != "" {
		reasonCode = strings.TrimSpace(requestedReasonCode[0])
	}
	handoffReason := routingReasonText(reasonCode)
	handoff := state.Handoff{
		ID:               fmt.Sprintf("handoff-%d-%d", time.Now().UnixMilli(), m.serverSequence.Add(1)),
		ThreadID:         threadID,
		SourceAccountID:  sourceAccountID,
		TargetAccountID:  targetAccountID,
		SourceGeneration: route.Generation,
		TargetGeneration: route.Generation + 1,
		Phase:            "PREPARED",
		ReasonCode:       reasonCode,
		Reason:           handoffReason,
		StartedAt:        time.Now().UnixMilli(),
	}
	route.ActiveMigrationID = handoff.ID
	if err := m.store.TransitionHandoff(handoff, route); err != nil {
		return fmt.Errorf("prepare handoff journal: %w", err)
	}
	m.persistCanonicalHandoff(handoff)
	m.recordHandoffTimeline(handoff, "handoff_prepared", handoffReason)
	m.publish(Event{Type: "handoff-prepared", ThreadID: threadID, AccountID: targetAccountID, PreviousAccountID: sourceAccountID, RouteGeneration: route.Generation, Data: handoff})
	failHandoff := func(cause error) error {
		handoff.Phase = "FAILED"
		_, handoff.Failure = sanitizeRoutingFailure(cause.Error())
		handoff.UpdatedAt = time.Now().UnixMilli()
		route.AccountID = sourceAccountID
		route.Generation = handoff.SourceGeneration
		route.HistoryGeneration = handoff.SourceGeneration
		route.ActiveMigrationID = ""
		if persistErr := m.store.TransitionHandoff(handoff, route); persistErr != nil {
			return errors.Join(cause, fmt.Errorf("persist failed handoff: %w", persistErr))
		}
		m.persistCanonicalHandoff(handoff)
		m.recordHandoffTimeline(handoff, "handoff_failed", handoff.Failure)
		m.publish(Event{Type: "handoff-rolled-back", ThreadID: threadID, AccountID: sourceAccountID, PreviousAccountID: targetAccountID, RouteGeneration: route.Generation, Message: handoff.Failure, Data: handoff})
		return cause
	}
	// The target may have loaded an older replica during a previous ownership
	// generation. Ask it to release that task before replacing the target path;
	// failure is harmless because locked-file materialization has a generation
	// fallback and resume verification remains strict.
	unsubscribeParams, _ := json.Marshal(map[string]string{"threadId": threadID})
	unsubscribeCtx, cancelUnsubscribe := context.WithTimeout(ctx, 2*time.Second)
	_, _ = target.Request(unsubscribeCtx, "thread/unsubscribe", unsubscribeParams)
	cancelUnsubscribe()
	resumePath := resumeInfo.historyPath
	if !samePath(resumeInfo.historyHome, targetAccount.CodexHome) {
		materialized, err := m.checkpointAndMaterialize(
			threadID,
			resumeInfo.historyHome,
			resumeInfo.historyPath,
			targetAccount.CodexHome,
		)
		if err != nil {
			return failHandoff(fmt.Errorf("copy existing chat history: %w", err))
		}
		resumePath = materialized.Path
		handoff.HistorySHA256 = materialized.SHA256
		handoff.HistorySize = materialized.Size
		route.HistorySHA256 = materialized.SHA256
		route.HistorySize = materialized.Size
		handoff.Phase = "COPIED"
		handoff.UpdatedAt = time.Now().UnixMilli()
		if err := m.store.TransitionHandoff(handoff, route); err != nil {
			return failHandoff(fmt.Errorf("persist copied handoff: %w", err))
		}
		m.persistCanonicalHandoff(handoff)
		m.recordHandoffTimeline(handoff, "handoff_copied", "authoritative history copied and verified")
		m.publish(Event{Type: "handoff-copied", ThreadID: threadID, AccountID: targetAccountID, PreviousAccountID: sourceAccountID, RouteGeneration: route.Generation, Data: handoff})
	}
	// Prefer the exact materialized path. A target's state DB can contain a
	// legacy absolute rollout_path for this same thread ID (for example the
	// former native ~/.codex home). ID-only resume would load that stale row and
	// make the target write outside its isolated account home even though the
	// verified replica exists locally. Current app-server builds give a non-
	// empty path precedence for non-running threads, which also repairs the row.
	// Older reviewed builds may reject this unstable field; the verified ID-only
	// fallback below remains fail-closed because verifyResumedThread requires the
	// returned path and checkpoint to belong to the target home.
	resumeParams := map[string]any{"threadId": threadID, "path": resumePath}
	if resumeInfo.cwd != "" {
		resumeParams["cwd"] = resumeInfo.cwd
	}
	if resumeInfo.modelProvider != "" {
		resumeParams["modelProvider"] = resumeInfo.modelProvider
	}
	encodedResumeParams, _ := json.Marshal(resumeParams)
	if _, err := target.Request(ctx, "thread/resume", encodedResumeParams); err != nil {
		// A previous target generation can remain loaded even after unsubscribe
		// and reject the new verified path as a consistency mismatch. Restart
		// only this idle Router-owned child, then retry the exact path once.
		if resumePath != "" && !isUnsupportedPathResumeError(err) {
			if restarted, restartErr := m.restartIdleChildForHandoff(ctx, targetAccount); restartErr == nil {
				target = restarted
				_, err = target.Request(ctx, "thread/resume", encodedResumeParams)
			}
		}
		// Some app-server builds reject optional resume fields even though they
		// accept the ID-only form. The rollout has already been copied into the
		// target home, so retrying the minimal request is safe and preserves
		// compatibility with those older builds.
		if resumePath != "" || resumeInfo.cwd != "" || resumeInfo.modelProvider != "" {
			minimalParams, _ := json.Marshal(map[string]string{"threadId": threadID})
			if _, retryErr := target.Request(ctx, "thread/resume", minimalParams); retryErr == nil {
				err = nil
			}
		}
		if err != nil {
			return failHandoff(fmt.Errorf("resume existing chat: %w", err))
		}
	}
	if err := m.verifyResumedThread(ctx, target, targetAccount, threadID, handoff); err != nil {
		return failHandoff(err)
	}
	if err := applyTransferableThreadGoal(ctx, target, threadID, goal); err != nil {
		return failHandoff(err)
	}
	m.recordResumeCapability(targetAccountID)
	handoff.Phase = "RESUMED"
	handoff.UpdatedAt = time.Now().UnixMilli()
	if err := m.store.TransitionHandoff(handoff, route); err != nil {
		return failHandoff(fmt.Errorf("persist resumed handoff: %w", err))
	}
	m.persistCanonicalHandoff(handoff)
	m.recordHandoffTimeline(handoff, "handoff_resumed", handoffReason)
	m.publish(Event{Type: "handoff-resumed", ThreadID: threadID, AccountID: targetAccountID, PreviousAccountID: sourceAccountID, RouteGeneration: route.Generation, Data: handoff})
	route.PreviousAccountID = sourceAccountID
	route.AccountID = targetAccountID
	route.Generation = handoff.TargetGeneration
	route.HistoryGeneration = handoff.TargetGeneration
	route.Policy = m.effectiveRoutingPolicy()
	route.CurrentState = "idle"
	route.ActiveMigrationID = ""
	handoff.Phase = "COMMITTED"
	handoff.UpdatedAt = time.Now().UnixMilli()
	if err := m.store.TransitionHandoff(handoff, route); err != nil {
		return failHandoff(fmt.Errorf("commit handoff: %w", err))
	}
	m.persistCanonicalHandoff(handoff)
	m.recordHandoffTimeline(handoff, reasonCode, handoffReason)
	_ = m.persistCanonicalSnapshot(threadID)
	m.publish(Event{Type: "handoff-committed", ThreadID: threadID, AccountID: targetAccountID, PreviousAccountID: sourceAccountID, RouteGeneration: route.Generation, Data: handoff})
	// The source is no longer authoritative after commit. Releasing its loaded
	// task prevents a future return handoff from encountering a locked obsolete
	// generation. This is best-effort and thread-scoped.
	if source != nil {
		releaseCtx, cancelRelease := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = source.Request(releaseCtx, "thread/unsubscribe", unsubscribeParams)
		cancelRelease()
	}
	return nil
}

func isUnsupportedPathResumeError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "path is unsupported") ||
		strings.Contains(text, "unknown field") ||
		strings.Contains(text, "invalid params") ||
		strings.Contains(text, "invalid parameter")
}

func (m *Multiplexer) verifyResumedThread(ctx context.Context, target *backend.Child, targetAccount state.Account, threadID string, handoff state.Handoff) error {
	params, _ := json.Marshal(map[string]any{"threadId": threadID, "includeTurns": false})
	response, err := target.Request(ctx, "thread/read", params)
	if err != nil {
		return fmt.Errorf("verify resumed chat: %w", err)
	}
	var decoded struct {
		Thread struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(response.Result, &decoded); err != nil {
		return fmt.Errorf("verify resumed chat response: %w", err)
	}
	if decoded.Thread.ID != threadID {
		return fmt.Errorf("verify resumed chat: target returned thread %q", decoded.Thread.ID)
	}
	if handoff.HistorySHA256 != "" {
		_, targetPath, err := resolveSourceHistoryPath(targetAccount.CodexHome, decoded.Thread.Path)
		if err != nil {
			return fmt.Errorf("verify resumed chat history path: %w", err)
		}
		hash, size, err := hashRegularFile(targetPath)
		if err != nil {
			return fmt.Errorf("verify resumed chat history: %w", err)
		}
		if hash != handoff.HistorySHA256 || size != handoff.HistorySize {
			return errors.New("verify resumed chat: target checkpoint hash or size does not match")
		}
	}
	return nil
}

func (m *Multiplexer) recoverInterruptedHandoffs() error {
	for _, handoff := range m.store.Handoffs() {
		if handoff.Phase == "COMMITTED" || handoff.Phase == "FAILED" || handoff.Phase == "ROLLED_BACK" {
			continue
		}
		route, ok := m.store.ThreadRoute(handoff.ThreadID)
		if !ok {
			route = state.ThreadRoute{ThreadID: handoff.ThreadID, AccountID: handoff.SourceAccountID, Generation: handoff.SourceGeneration}
		}
		route.AccountID = handoff.SourceAccountID
		route.Generation = handoff.SourceGeneration
		route.HistoryGeneration = handoff.SourceGeneration
		route.ActiveMigrationID = ""
		handoff.Phase = "ROLLED_BACK"
		handoff.Failure = "recovered after interrupted handoff"
		handoff.UpdatedAt = time.Now().UnixMilli()
		if err := m.store.TransitionHandoff(handoff, route); err != nil {
			return err
		}
		m.persistCanonicalHandoff(handoff)
		m.publish(Event{Type: "handoff-rolled-back", ThreadID: handoff.ThreadID, AccountID: handoff.SourceAccountID, PreviousAccountID: handoff.TargetAccountID, RouteGeneration: route.Generation, Message: handoff.Failure, Data: handoff})
	}
	return nil
}

func (m *Multiplexer) recoverInterruptedAttempts() error {
	for _, route := range m.store.ThreadRoutes() {
		if route.ActiveAttemptID == "" {
			continue
		}
		attemptID := route.ActiveAttemptID
		attempt, ok := m.store.TurnAttempt(attemptID)
		if ok && (attempt.Phase == "COMPLETED" || attempt.Phase == "FAILED") {
			route.ActiveAttemptID = ""
			if err := m.store.PutThreadRoute(route); err != nil {
				return err
			}
			_ = m.store.ReleaseReservation(attemptID)
			continue
		}
		if ok {
			attempt.Phase = "RECOVERY_REQUIRED"
			attempt.Failure = "Relay restarted before the turn reached a proven terminal state"
			attempt.UpdatedAt = time.Now().UnixMilli()
			if err := m.store.PutTurnAttempt(attempt); err != nil {
				return err
			}
		}
		route.ActiveAttemptID = ""
		route.RecoveryRequired = true
		if err := m.store.PutThreadRoute(route); err != nil {
			return err
		}
		_ = m.store.ReleaseReservation(attemptID)
	}
	return nil
}

func (m *Multiplexer) handleServerRequestResponse(message protocol.Message) {
	key := protocol.RequestIDKey(message.ID)
	m.serverMu.Lock()
	route, ok := m.serverRoutes[key]
	if ok {
		delete(m.serverRoutes, key)
	}
	m.serverMu.Unlock()
	if !ok {
		return
	}
	message.ID = route.original
	if child, exists := m.child(route.accountID); exists {
		_ = child.Send(message)
	}
}

func (m *Multiplexer) hasPendingServerRequest(threadID, accountID string) bool {
	m.serverMu.Lock()
	defer m.serverMu.Unlock()
	for _, route := range m.serverRoutes {
		if route.accountID == accountID && (route.threadID == threadID || route.threadID == "") {
			return true
		}
	}
	return false
}

func (m *Multiplexer) inboundLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case inbound := <-m.inbound:
			m.handleInbound(inbound)
		}
	}
}

func (m *Multiplexer) handleInbound(inbound backend.Inbound) {
	message := inbound.Message
	if isVisibleOutputNotification(message.Method, message.Params) {
		m.markVisibleOutput(inbound.AccountID, message.Method, message.Params)
	}
	if message.Method == "item/started" || message.Method == "item/completed" || strings.HasPrefix(message.Method, "hook/") {
		m.markAccountSideEffects(inbound.AccountID, message.Method, message.Params)
	}
	if message.Method == "" && len(message.ID) > 0 {
		key := protocol.RequestIDKey(message.ID)
		m.externalMu.Lock()
		route, ok := m.externalRoutes[key]
		if ok {
			if route.method == "turn/start" && message.Error == nil {
				m.markTurnAccepted(threadIDFromParams(route.message.Params), inbound.AccountID)
			}
			delete(m.externalRoutes, key)
		}
		m.externalMu.Unlock()
		if ok {
			if route.reservationID != "" {
				if route.method == "thread/start" && message.Error != nil {
					_ = m.store.RollbackReservation(route.reservationID)
				} else {
					_ = m.store.ReleaseReservation(route.reservationID)
				}
			}
			// Model capacity is transient and is independent from subscription
			// quota. Retry the exact same request on the same account first; do
			// not silently substitute a different model or consume another
			// subscription while the selected model is merely busy.
			if route.method == "turn/start" && isModelCapacityResponse(message) {
				m.runAsync(func() { m.retryTurnAfterModelCapacity(route, route.accountID, inbound.Raw) })
				return
			}
			if route.method == "turn/start" && isUsageLimitResponse(message) {
				m.recordAccountFailure(inbound.AccountID, "quota rejected turn")
				m.removeActiveTurn(threadIDFromParams(route.message.Params), inbound.AccountID)
				if !m.safeHandoff {
					m.failTurnAttempt(threadIDFromParams(route.message.Params), m.safeHandoffUnavailableError())
					m.writeRaw(inbound.Raw)
					return
				}
				m.runAsync(func() { m.retryTurnAfterUsageLimit(route, inbound.AccountID) })
				return
			}
			if route.method == "thread/start" && isUsageLimitResponse(message) {
				m.recordAccountFailure(inbound.AccountID, "quota rejected new chat")
				// A new-thread request does not have a thread ID yet, so it cannot
				// use the existing-chat migration path. Retry the identical request
				// on another subscription and keep the quota error out of the UI.
				m.runAsync(func() { m.retryNewThreadAfterUsageLimit(route, inbound.AccountID) })
				return
			}
			m.learnThreadOwner(route, inbound.AccountID, message.Result)
			m.writeRaw(inbound.Raw)
		}
		return
	}
	if message.Method == "error" || message.Method == "turn/completed" {
		threadID := threadIDFromTurnNotification(message.Params)
		if threadID != "" && isUsageLimitNotification(message) {
			if active, ok := m.takeActiveTurnForFailure(threadID, inbound.AccountID); ok {
				m.recordAccountFailure(inbound.AccountID, "quota rejected active turn")
				if active.sideEffectsStarted || active.visibleOutputStarted {
					reason := "quota exhausted after visible assistant output started"
					if active.sideEffectsStarted {
						reason = "quota exhausted after side effects started"
					}
					m.requireTurnRecovery(threadID, active, reason)
					m.writeRaw(inbound.Raw)
					return
				}
				if !m.safeHandoff {
					m.finishTurnAttempt(threadID, active, "FAILED", m.safeHandoffUnavailableError().Error())
					m.writeRaw(inbound.Raw)
					return
				}
				m.runAsync(func() { m.retryTurnAfterAsyncUsageLimit(active.route, inbound.AccountID) })
				return
			}
			if m.suppressFailedTurnEvent(threadID, inbound.AccountID) {
				// Codex can emit both error and failed turn/completed for one
				// turn. The first event starts failover; suppress the duplicate
				// without incrementing the circuit a second time.
				return
			}
			// Goals can start their next model turn inside app-server without a
			// renderer-originated turn/start. At the terminal turn/completed
			// boundary there is no replayable request in activeTurns, but the
			// persisted rollout and goal are safe to transfer. Claim this exact
			// terminal turn once, then resume the goal on another worker rather
			// than leaving it pinned to an exhausted subscription.
			if message.Method == "turn/completed" &&
				m.claimCompletedQuotaFailure(threadID, inbound.AccountID, turnIDFromCompletedNotification(message.Params)) {
				m.recordAccountFailure(inbound.AccountID, "quota rejected autonomous turn")
				if !m.safeHandoff {
					m.writeRaw(inbound.Raw)
					return
				}
				failureRaw := append([]byte(nil), inbound.Raw...)
				m.runAsync(func() {
					m.failoverCompletedTurnAfterUsageLimit(threadID, inbound.AccountID, failureRaw)
				})
				return
			}
		}
	}
	if message.Method != "" && len(message.ID) > 0 {
		m.markAccountSideEffects(inbound.AccountID, message.Method, message.Params)
		m.forwardServerRequest(inbound)
		return
	}
	if message.Method == "account/rateLimits/updated" {
		m.runAsync(func() { m.forwardAggregatedRateLimitNotification(inbound.Raw) })
		return
	}
	if message.Method == "thread/started" {
		if threadID := threadIDFromNotification(message.Params); threadID != "" {
			m.markNewThreadOwner(threadID, inbound.AccountID)
		}
	}
	if message.Method == "turn/completed" {
		if !isUsageLimitNotification(message) {
			m.recordAccountSuccess(inbound.AccountID)
		}
		completedThreadID := threadIDFromTurnNotification(message.Params)
		m.completeActiveTurn(completedThreadID, inbound.AccountID, turnIDFromCompletedNotification(message.Params))
		if completedThreadID != "" && !isUsageLimitNotification(message) {
			m.runAsync(func() { m.checkpointCompletedTurn(completedThreadID, inbound.AccountID) })
		}
	}
	if message.Method == "turn/completed" ||
		message.Method == "account/login/completed" ||
		message.Method == "account/updated" {
		m.runAsync(func() { m.publishAccountRefresh(inbound.AccountID) })
	}
	if m.shouldForwardNotification(inbound.AccountID, message.Method, message.Params) {
		m.writeRaw(inbound.Raw)
	}
}

const (
	maxModelCapacityRetries = 3
	modelCapacityRetryBase  = 750 * time.Millisecond
)

// retryTurnAfterModelCapacity retries a transient "model at capacity" error
// with the original turn/start message. The backoff is deliberately short
// enough for an interactive chat while the cap guarantees that a persistently
// unavailable model returns the upstream error instead of hanging forever.
func (m *Multiplexer) retryTurnAfterModelCapacity(
	route externalRoute,
	accountID string,
	failureRaw []byte,
) {
	if route.capacityRetries >= maxModelCapacityRetries {
		threadID := threadIDFromParams(route.message.Params)
		if threadID != "" {
			m.failTurnAttempt(threadID, errors.New("selected model remained at capacity after bounded retries"))
		}
		m.writeRaw(failureRaw)
		return
	}
	backoff := modelCapacityRetryBase * time.Duration(1<<route.capacityRetries)
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	<-timer.C
	if err := m.forwardRoute(accountID, route.message, route.excluded, route.capacityRetries+1); err != nil {
		threadID := threadIDFromParams(route.message.Params)
		if threadID != "" {
			m.failTurnAttempt(threadID, fmt.Errorf("retry selected model: %w", err))
		}
		m.write(protocol.Failure(route.message.ID, -32023, fmt.Sprintf("retry selected model: %v", err)))
	}
}

func isVisibleOutputNotification(method string, params []byte) bool {
	method = strings.ToLower(strings.TrimSpace(method))
	if strings.HasPrefix(method, "assistant/") || strings.Contains(method, "agentmessage") ||
		strings.Contains(method, "assistantmessage") || strings.Contains(method, "output") {
		return true
	}
	if !strings.HasPrefix(method, "item/") {
		return false
	}
	var payload struct {
		Item struct {
			Type string `json:"type"`
		} `json:"item"`
	}
	if json.Unmarshal(params, &payload) != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(payload.Item.Type)) {
	case "agentmessage", "assistantmessage", "reasoning", "plan":
		return true
	default:
		return false
	}
}

func (m *Multiplexer) forwardAggregatedRateLimitNotification(fallback []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	rateLimits, err := m.AggregatedRateLimits(ctx)
	if err != nil {
		m.writeRaw(fallback)
		return
	}
	params, err := json.Marshal(map[string]any{"rateLimits": rateLimits})
	if err != nil {
		m.writeRaw(fallback)
		return
	}
	m.write(protocol.Message{Method: "account/rateLimits/updated", Params: params})
}

func (m *Multiplexer) retryTurnAfterUsageLimit(route externalRoute, exhaustedAccountID string) {
	threadID := threadIDFromParams(route.message.Params)
	if threadID == "" {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		m.write(m.allSubscriptionsDepleted(ctx, route.message.ID))
		return
	}
	excluded := cloneAccountSet(route.excluded)
	if excluded == nil {
		excluded = make(map[string]struct{})
	}
	excluded[exhaustedAccountID] = struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
	defer cancel()
	m.failoverTurn(ctx, route.message, threadID, exhaustedAccountID, excluded)
}

// retryNewThreadAfterUsageLimit handles the immediate quota-rejection shape
// for thread/start. The selected subscription may have stale usage metadata,
// or the upstream may enforce a window that was not present in the last
// snapshot. Keep trying capacity-bearing subscriptions until one accepts the
// exact original payload or all of them are exhausted.
func (m *Multiplexer) retryNewThreadAfterUsageLimit(route externalRoute, exhaustedAccountID string) {
	excluded := cloneAccountSet(route.excluded)
	if excluded == nil {
		excluded = make(map[string]struct{})
	}
	excluded[exhaustedAccountID] = struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
	defer cancel()

	for {
		reservationID := "request-" + protocol.RequestIDKey(route.message.ID)
		fallback, _, err := m.chooseAccountReserved(ctx, excluded, reservationID)
		if err != nil {
			m.write(m.allSubscriptionsDepleted(ctx, route.message.ID))
			return
		}
		if err := m.forwardRoute(fallback.ID, route.message, excluded, 0); err != nil {
			_ = m.store.RollbackReservation(reservationID)
			excluded[fallback.ID] = struct{}{}
			if ctx.Err() != nil {
				m.write(protocol.Failure(route.message.ID, -32027, fmt.Sprintf("retry new chat with %s: %v", fallback.Label, err)))
				return
			}
			continue
		}
		m.publish(Event{
			Type:      "thread-failed-over",
			AccountID: fallback.ID,
			Message:   fmt.Sprintf("New chat retried with %s", fallback.Label),
			Data:      map[string]any{"previousAccountId": exhaustedAccountID},
		})
		return
	}
}

// retryTurnAfterAsyncUsageLimit handles the app-server's v2 failure shape. A
// turn/start request can be accepted successfully and only later emit an
// `error` event / failed `turn/completed` when the upstream quota is rejected.
// In that case the desktop already received the initial turn response, so the
// fallback turn/start is sent as an internal child request and its response is
// intentionally not duplicated to the desktop client.
func (m *Multiplexer) retryTurnAfterAsyncUsageLimit(route externalRoute, exhaustedAccountID string) {
	threadID := threadIDFromParams(route.message.Params)
	if threadID == "" {
		return
	}
	excluded := cloneAccountSet(route.excluded)
	if excluded == nil {
		excluded = make(map[string]struct{})
	}
	excluded[exhaustedAccountID] = struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
	defer cancel()

	for {
		fallback, _, err := m.chooseAccountExcluding(ctx, excluded)
		if err != nil {
			m.write(m.allSubscriptionsDepleted(ctx, route.message.ID))
			return
		}
		if err := m.resumeThreadOnAccount(ctx, threadID, exhaustedAccountID, fallback.ID, "handoff_quota_exhausted"); err != nil {
			excluded[fallback.ID] = struct{}{}
			if ctx.Err() != nil {
				m.write(protocol.Failure(route.message.ID, -32027, fmt.Sprintf("move chat to %s: %v", fallback.Label, err)))
				return
			}
			continue
		}
		child, ok := m.child(fallback.ID)
		if !ok {
			excluded[fallback.ID] = struct{}{}
			continue
		}
		// Register the target before sending turn/start. A target can accept the
		// request and emit its asynchronous quota failure immediately; registering
		// only after Request returns would race and leak that failure to the UI.
		m.rememberActiveTurn(threadID, externalRoute{
			accountID: fallback.ID,
			method:    route.method,
			message:   route.message,
			excluded:  cloneAccountSet(excluded),
		})
		routeState, _ := m.store.ThreadRoute(threadID)
		if err := m.moveTurnAttempt(threadID, fallback.ID, routeState.Generation); err != nil {
			m.failTurnAttempt(threadID, err)
			m.write(protocol.Failure(route.message.ID, -32028, err.Error()))
			return
		}
		if snapshot, snapshotErr := m.accountSnapshotWithProfile(ctx, fallback.ID, false); snapshotErr == nil {
			m.recordAttemptQuotaBefore(threadID, snapshot)
		}
		response, err := child.Request(ctx, "turn/start", route.message.Params)
		if err == nil {
			_ = m.store.SetThreadOwner(threadID, fallback.ID)
			m.recordRoutingDecision(threadID, exhaustedAccountID, fallback.ID, "quota rejected active turn before side effects")
			m.publish(Event{
				Type:      "thread-failed-over",
				AccountID: fallback.ID,
				Message:   fmt.Sprintf("Chat continued with %s", fallback.Label),
				Data:      map[string]any{"threadId": threadID, "previousAccountId": exhaustedAccountID},
			})
			return
		}
		m.removeActiveTurn(threadID, fallback.ID)
		if isUsageLimitResponse(response) {
			excluded[fallback.ID] = struct{}{}
			continue
		}
		m.write(protocol.Failure(route.message.ID, -32027, fmt.Sprintf("continue chat with %s: %v", fallback.Label, err)))
		return
	}
}

func (m *Multiplexer) forwardServerRequest(inbound backend.Inbound) {
	sequence := m.serverSequence.Add(1)
	newID := protocol.StringID(fmt.Sprintf("codex-mux:%s:%d", inbound.AccountID, sequence))
	key := protocol.RequestIDKey(newID)
	threadID := threadIDFromAnyParams(inbound.Message.Params)
	attemptID := ""
	generation := uint64(0)
	m.turnMu.Lock()
	if threadID != "" {
		if active, ok := m.activeTurns[threadID]; ok && active.route.accountID == inbound.AccountID {
			attemptID = active.attemptID
			generation = active.generation
		}
	} else {
		candidateCount := 0
		for candidateThreadID, active := range m.activeTurns {
			if active.route.accountID != inbound.AccountID {
				continue
			}
			candidateCount++
			threadID = candidateThreadID
			attemptID = active.attemptID
			generation = active.generation
		}
		if candidateCount != 1 {
			threadID, attemptID, generation = "", "", 0
		}
	}
	m.turnMu.Unlock()
	m.serverMu.Lock()
	m.serverRoutes[key] = serverRequestRoute{
		accountID:  inbound.AccountID,
		threadID:   threadID,
		attemptID:  attemptID,
		generation: generation,
		original:   append(json.RawMessage(nil), inbound.Message.ID...),
	}
	m.serverMu.Unlock()
	inbound.Message.ID = newID
	m.write(inbound.Message)
}

func (m *Multiplexer) shouldForwardNotification(accountID, method string, params json.RawMessage) bool {
	if strings.HasPrefix(method, "thread/") || strings.HasPrefix(method, "turn/") ||
		strings.HasPrefix(method, "item/") || strings.HasPrefix(method, "hook/") ||
		strings.HasPrefix(method, "rawResponse") {
		threadID := threadIDFromAnyParams(params)
		if threadID != "" {
			m.turnMu.Lock()
			active, activeOK := m.activeTurns[threadID]
			m.turnMu.Unlock()
			if activeOK {
				return active.route.accountID == accountID
			}
			if route, ok := m.store.ThreadRoute(threadID); ok {
				return route.AccountID == accountID
			}
			return false
		}
		// A notification without a recognizable thread is not safe to attribute
		// to a non-controller worker.
		controller, ok := m.store.Controller()
		return ok && controller.ID == accountID
	}
	controller, ok := m.store.Controller()
	if ok && controller.ID == accountID {
		return true
	}
	return false
}

func (m *Multiplexer) learnThreadOwner(route externalRoute, accountID string, result json.RawMessage) {
	switch route.method {
	case "thread/start":
		if threadID := threadIDFromResult(result); threadID != "" {
			m.markNewThreadOwner(threadID, accountID)
		}
	case "thread/fork", "thread/resume", "thread/unarchive":
		if threadID := threadIDFromResult(result); threadID != "" {
			_ = m.store.SetThreadOwner(threadID, accountID)
		}
	}
}

func (m *Multiplexer) markNewThreadOwner(threadID, accountID string) {
	if err := m.store.SetThreadOwner(threadID, accountID); err != nil {
		return
	}
	if route, ok := m.store.ThreadRoute(threadID); ok && route.AccountID == accountID {
		route.FirstTurnPending = true
		_ = m.store.PutThreadRoute(route)
	}
}

func (m *Multiplexer) write(message protocol.Message) {
	encoded, err := protocol.Encode(message)
	if err != nil {
		fmt.Fprintf(os.Stderr, "codex-mux: encode response: %v\n", err)
		return
	}
	m.writeRaw(encoded)
}

func (m *Multiplexer) writeRaw(encoded []byte) {
	m.outputMu.Lock()
	defer m.outputMu.Unlock()
	_, _ = m.output.Write(append(encoded, '\n'))
}

type childEntry struct {
	account state.Account
	child   *backend.Child
}

func (m *Multiplexer) childEntries() []childEntry {
	accounts := m.store.Accounts()
	m.childrenMu.RLock()
	defer m.childrenMu.RUnlock()
	entries := make([]childEntry, 0, len(accounts))
	for _, account := range accounts {
		if child := m.children[account.ID]; child != nil {
			entries = append(entries, childEntry{account: account, child: child})
		}
	}
	return entries
}

func (m *Multiplexer) child(accountID string) (*backend.Child, bool) {
	m.childrenMu.RLock()
	defer m.childrenMu.RUnlock()
	child, ok := m.children[accountID]
	return child, ok
}

func (m *Multiplexer) removeChild(accountID string, expected *backend.Child) {
	m.childrenMu.Lock()
	defer m.childrenMu.Unlock()
	if child, ok := m.children[accountID]; ok && (expected == nil || child == expected) {
		delete(m.children, accountID)
	}
}

// removeIsolatedAccountHome performs a deliberately narrow cleanup after the
// state entry was safely removed. It never derives a deletion target from a
// caller-provided path: the account home must be the exact child home created
// by state.Store underneath CODEX_MUX_HOME/accounts/<id>. The native Codex
// home is preserved even when its Router metadata is removed.
func (m *Multiplexer) removeIsolatedAccountHome(account state.Account) error {
	if account.ID == "" || filepath.Base(account.ID) != account.ID {
		return errors.New("refusing to remove a non-secondary account home")
	}
	if sameFilesystemPath(account.CodexHome, m.store.PrimaryCodexHome()) {
		return nil
	}
	expected, err := filepath.Abs(filepath.Join(m.store.Root(), "accounts", account.ID, "codex-home"))
	if err != nil {
		return fmt.Errorf("resolve expected account home: %w", err)
	}
	actual, err := filepath.Abs(account.CodexHome)
	if err != nil {
		return fmt.Errorf("resolve account home: %w", err)
	}
	if actual != expected {
		return errors.New("refusing to remove an unexpected account home")
	}
	return os.RemoveAll(filepath.Dir(actual))
}

func sameFilesystemPath(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	leftAbsolute, leftErr := filepath.Abs(left)
	rightAbsolute, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return filepath.Clean(left) == filepath.Clean(right)
	}
	return filepath.Clean(leftAbsolute) == filepath.Clean(rightAbsolute)
}

// purgeAccountReferences removes only transient router data for a secondary
// account that has already been discarded from persistent state. An
// unconnected provisional account cannot own a useful route, but clearing any
// stale references prevents a late child notification from reviving UI state.
func (m *Multiplexer) purgeAccountReferences(accountID string) {
	m.externalMu.Lock()
	for key, route := range m.externalRoutes {
		if route.accountID == accountID {
			delete(m.externalRoutes, key)
		}
	}
	m.externalMu.Unlock()

	m.serverMu.Lock()
	for key, route := range m.serverRoutes {
		if route.accountID == accountID {
			delete(m.serverRoutes, key)
		}
	}
	m.serverMu.Unlock()

	m.profileMu.Lock()
	delete(m.profileCache, accountID)
	m.profileMu.Unlock()

	m.resetCreditsMu.Lock()
	delete(m.resetCreditsCache, accountID)
	m.resetCreditsMu.Unlock()

	m.resetPreviewMu.Lock()
	delete(m.resetPreviews, accountID)
	m.resetPreviewMu.Unlock()

	m.previewMu.Lock()
	if m.rateLimitPreview != nil && m.rateLimitPreview.AccountID == accountID {
		m.rateLimitPreview = nil
	}
	m.previewMu.Unlock()
}

func (m *Multiplexer) controllerChild() (*backend.Child, bool) {
	controller, ok := m.store.Controller()
	if !ok {
		return nil, false
	}
	return m.child(controller.ID)
}

func (m *Multiplexer) startChild(ctx context.Context, account state.Account) (*backend.Child, error) {
	if child, ok := m.child(account.ID); ok {
		return child, nil
	}
	child, err := backend.Start(
		account.ID,
		account.CodexHome,
		m.realExecutable,
		m.realArgs,
		m.environment,
		m.inbound,
	)
	if err != nil {
		return nil, err
	}
	m.childrenMu.Lock()
	m.children[account.ID] = child
	m.childrenMu.Unlock()

	m.initializationMu.RLock()
	params := append(json.RawMessage(nil), m.initializeParams...)
	initialized := m.initialized
	m.initializationMu.RUnlock()
	if len(params) > 0 {
		requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		_, err := child.Request(requestCtx, "initialize", params)
		cancel()
		if err != nil {
			_ = child.Close()
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = child.Wait(shutdownCtx)
			shutdownCancel()
			m.removeChild(account.ID, child)
			return nil, err
		}
		if initialized {
			_ = child.Send(protocol.Message{Method: "initialized"})
		}
	}
	return child, nil
}

func (m *Multiplexer) SubscribeEvents() (<-chan Event, func()) {
	channel := make(chan Event, 32)
	m.eventsMu.Lock()
	m.events[channel] = struct{}{}
	m.eventsMu.Unlock()
	return channel, func() {
		m.eventsMu.Lock()
		if _, ok := m.events[channel]; ok {
			delete(m.events, channel)
			close(channel)
		}
		m.eventsMu.Unlock()
	}
}

func (m *Multiplexer) publish(event Event) {
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().UnixMilli()
	}
	m.eventsMu.RLock()
	defer m.eventsMu.RUnlock()
	for channel := range m.events {
		select {
		case channel <- event:
		default:
		}
	}
}

func (m *Multiplexer) publishAccountRefresh(accountID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snapshot, err := m.accountSnapshot(ctx, accountID)
	if err == nil {
		m.publish(Event{Type: "account-updated", AccountID: accountID, Data: snapshot})
	}
}

func threadIDFromParams(params json.RawMessage) string {
	if len(params) == 0 {
		return ""
	}
	var decoded map[string]any
	if json.Unmarshal(params, &decoded) != nil {
		return ""
	}
	for _, key := range []string{"threadId", "thread_id", "conversationId", "conversation_id"} {
		if value, ok := decoded[key].(string); ok {
			return value
		}
	}
	return ""
}

func threadIDFromResult(result json.RawMessage) string {
	var decoded struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(result, &decoded) != nil {
		return ""
	}
	return decoded.Thread.ID
}

func threadIDFromNotification(params json.RawMessage) string {
	return threadIDFromResult(params)
}

func threadIDFromTurnNotification(params json.RawMessage) string {
	if len(params) == 0 {
		return ""
	}
	var decoded struct {
		ThreadID       string `json:"threadId"`
		ConversationID string `json:"conversationId"`
		Thread         struct {
			ID string `json:"id"`
		} `json:"thread"`
		Turn struct {
			ThreadID string `json:"threadId"`
			Thread   struct {
				ID string `json:"id"`
			} `json:"thread"`
		} `json:"turn"`
	}
	if json.Unmarshal(params, &decoded) != nil {
		return ""
	}
	for _, value := range []string{
		decoded.ThreadID,
		decoded.ConversationID,
		decoded.Thread.ID,
		decoded.Turn.ThreadID,
		decoded.Turn.Thread.ID,
	} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func turnIDFromCompletedNotification(params json.RawMessage) string {
	var decoded struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(params, &decoded) != nil {
		return ""
	}
	return strings.TrimSpace(decoded.Turn.ID)
}

func threadIDFromAnyParams(params json.RawMessage) string {
	for _, value := range []string{threadIDFromParams(params), threadIDFromTurnNotification(params), threadIDFromResult(params)} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func accountHasCapacity(snapshot AccountSnapshot) bool {
	if !snapshot.Enabled || !snapshot.Connected || snapshot.AuthType != "chatgpt" {
		return false
	}
	// Explicit deny signals are authoritative. An explicit allow is useful
	// recovery evidence, but it cannot override a 100%-used window because the
	// current Usage endpoint can temporarily report allowed=true at the exact
	// boundary where model turns are still rejected.
	if snapshot.QuotaLimitReached != nil && *snapshot.QuotaLimitReached {
		return false
	}
	if snapshot.QuotaAllowed != nil && !*snapshot.QuotaAllowed {
		return false
	}
	// Codex exposes a short and a longer quota window. A subscription is only
	// routable when every reported window has remaining capacity; otherwise a
	// short-window exhaustion would still receive one avoidable failing turn.
	if snapshot.RateLimits == nil {
		return true
	}
	for _, window := range []*RateLimitWindow{
		snapshot.RateLimits.Primary,
		snapshot.RateLimits.Secondary,
	} {
		if window != nil && window.UsedPercent >= 100 {
			return false
		}
	}
	return true
}

func isUsageLimitResponse(message protocol.Message) bool {
	if message.Error == nil {
		return false
	}
	text := strings.ToLower(message.Error.Message + " " + string(message.Error.Data))
	return strings.Contains(text, "usage_limit") ||
		strings.Contains(text, "usage limit") ||
		strings.Contains(text, "rate_limit") ||
		strings.Contains(text, "rate limit") ||
		strings.Contains(text, "quota")
}

func isUsageLimitNotification(message protocol.Message) bool {
	if message.Method == "error" {
		return isUsageLimitText(string(message.Params))
	}
	if message.Method != "turn/completed" {
		return false
	}
	var decoded struct {
		Turn struct {
			Status string          `json:"status"`
			Error  json.RawMessage `json:"error"`
		} `json:"turn"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(message.Params, &decoded) != nil {
		return false
	}
	// Current Codex app-server schemas mark these turns failed, but goal-driven
	// releases have also emitted a terminal error before/without the status
	// field. The machine-readable TurnError is sufficient evidence on its own.
	// Inspect only error objects—not all turn items—so a user's ordinary message
	// about "quota" can never be mistaken for an upstream rejection.
	if len(decoded.Turn.Error) > 0 && string(decoded.Turn.Error) != "null" && isUsageLimitText(string(decoded.Turn.Error)) {
		return true
	}
	if len(decoded.Error) > 0 && string(decoded.Error) != "null" && isUsageLimitText(string(decoded.Error)) {
		return true
	}
	return false
}

func isUsageLimitText(value string) bool {
	text := strings.ToLower(value)
	compact := strings.NewReplacer("_", "", "-", "", " ", "", "\t", "", "\r", "", "\n", "").Replace(text)
	return strings.Contains(text, "usage_limit") ||
		strings.Contains(text, "usage limit") ||
		strings.Contains(text, "usage_limit_exceeded") ||
		strings.Contains(text, "rate_limit") ||
		strings.Contains(text, "rate limit") ||
		strings.Contains(text, "quota") ||
		strings.Contains(compact, "usagelimitexceeded") ||
		strings.Contains(compact, "ratelimitexceeded")
}

// isModelCapacityResponse recognizes the stable upstream wording and its
// machine-readable variants without treating every generic "capacity" error
// as a reason to retry. The request's model is never inspected or changed;
// this predicate only decides whether the same payload should be attempted
// again.
func isModelCapacityResponse(message protocol.Message) bool {
	if message.Error == nil {
		return false
	}
	text := strings.ToLower(message.Error.Message + " " + string(message.Error.Data))
	normalized := strings.Join(strings.Fields(text), " ")
	return strings.Contains(normalized, "selected model is at capacity") ||
		strings.Contains(normalized, "model is at capacity") ||
		strings.Contains(text, "model_at_capacity") ||
		strings.Contains(text, "model_capacity")
}

func (m *Multiplexer) allSubscriptionsDepleted(ctx context.Context, id json.RawMessage) protocol.Message {
	var resetsAt *int64
	if preview := m.currentRateLimitPreview(); preview != nil && preview.Mode.isAllDepleted() {
		resetsAt = preview.ResetsAt
	} else if limits, err := m.AggregatedRateLimits(ctx); err == nil {
		weekly, _ := longestAndShortestWindow(limits)
		if weekly != nil {
			resetsAt = weekly.ResetsAt
		}
	}
	message := allSubscriptionsDepleted(id, resetsAt)
	m.recordTimelineEvent("", stateRoutingEvent{
		ID: "all-depleted:" + protocol.RequestIDKey(id), EventType: "all_accounts_depleted",
		ReasonCode: "skipped_depleted", Reason: "all connected subscriptions are depleted",
		Result: "blocked", CreatedAt: time.Now().UnixMilli(),
	})
	return message
}

func allSubscriptionsDepleted(id json.RawMessage, resetsAt *int64) protocol.Message {
	message := "All connected subscriptions are depleted. Add another subscription or wait for usage to reset."
	if resetsAt != nil {
		reset := time.Unix(*resetsAt, 0).In(time.Local)
		message = fmt.Sprintf(
			"All connected subscriptions are depleted. Usage resets on %s.",
			reset.Format("Monday, 2 January at 3:04 PM"),
		)
	}
	return protocol.Failure(
		id,
		-32026,
		message,
	)
}

func cloneAccountSet(source map[string]struct{}) map[string]struct{} {
	if len(source) == 0 {
		return nil
	}
	clone := make(map[string]struct{}, len(source))
	for accountID := range source {
		clone[accountID] = struct{}{}
	}
	return clone
}

func sortThreads(threads []map[string]any) {
	sort.SliceStable(threads, func(i, j int) bool {
		return numericField(threads[i], "updatedAt", "createdAt") > numericField(threads[j], "updatedAt", "createdAt")
	})
}

func numericField(value map[string]any, keys ...string) float64 {
	for _, key := range keys {
		if number, ok := value[key].(float64); ok {
			return number
		}
	}
	return 0
}
