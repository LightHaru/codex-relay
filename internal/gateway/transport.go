package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LightHaru/codex-relay/internal/state"
)

const (
	defaultUpstreamURL   = "https://chatgpt.com/backend-api/codex/responses"
	maxRequestBytes      = 64 << 20
	maxBufferedSSEBytes  = 2 << 20
	maxSSESniffBytes     = 8 << 10
	maxReplayBytes       = 32 << 20
	requestReplayTTL     = 30 * time.Second
	sseKeepaliveInterval = time.Second
	// The native app-server has a shorter protocol watchdog than a long-lived
	// model request. After output is visible, fail closed if the provider stays
	// silent for this bounded interval; the recovery terminal is then delivered
	// before the client turns the idle connection into a stream-disconnect error.
	sseIdleRecoveryTimeout = 3 * time.Second
	// A completed output item is normally followed immediately by
	// response.completed. Hold that one frame briefly so the native app-server
	// cannot cancel the request before Relay can emit a standards-shaped
	// response.failed when the provider omits the terminal event.
	// The native app-server closes a response that has reached
	// response.output_item.done but has not received a terminal event within a
	// short watchdog window. Keep this below that watchdog so an upstream that
	// goes silent cannot turn into the misleading client-side
	// "stream disconnected before completion" error.
	sseTerminalGraceTimeout = 100 * time.Millisecond
	// Matching transport failures from independent sources in a short window
	// indicate a likely provider/edge outage. Stop after three sources so a
	// five-source pool does not create a retry storm.
	globalOutageWindow    = 10 * time.Second
	globalOutageThreshold = 3
)

type Transport struct {
	Store            *state.Store
	Client           *http.Client
	UpstreamURL      string
	LeaseTTL         time.Duration
	LocalBearerToken string
	// BalancedPool keeps the public Relay API/task authority singular while
	// selecting credentials from the additive pool with the quota-aware fair
	// scheduler. It is enabled by the production unified gateway; leaving it
	// false preserves the sticky behavior expected by legacy embedders/tests.
	BalancedPool bool
	// LoadCredentials is an optional in-process credential loader used by
	// deterministic integration tests and controlled embedders. Production
	// Relay leaves it nil, so credentials continue to be read from the selected
	// isolated source home for every dispatch.
	LoadCredentials func(state.Account) (accessToken, accountID string, err error)
	// DisableFailover is set for an unreviewed app-server compatibility
	// profile. Relay still exposes one API, but it fails closed instead of
	// attempting a credential continuation that has not been protocol-tested.
	DisableFailover bool
	flightMu        sync.Mutex
	flights         map[string]*requestFlight
	outageMu        sync.Mutex
	outageSamples   map[string]*outageSample
}

type outageSample struct {
	started time.Time
	sources map[string]struct{}
}

type requestFlight struct {
	done      chan struct{}
	header    http.Header
	status    int
	body      bytes.Buffer
	overflow  bool
	completed bool
	expiresAt time.Time
	mu        sync.Mutex
}

type flightResponseWriter struct {
	http.ResponseWriter
	flight *requestFlight
}

func (writer *flightResponseWriter) WriteHeader(status int) {
	writer.flight.mu.Lock()
	if writer.flight.status == 0 {
		writer.flight.status = status
		writer.flight.header = writer.Header().Clone()
	}
	writer.flight.mu.Unlock()
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *flightResponseWriter) Write(data []byte) (int, error) {
	writer.flight.mu.Lock()
	if writer.flight.status == 0 {
		writer.flight.status = http.StatusOK
		writer.flight.header = writer.Header().Clone()
	}
	if !writer.flight.overflow {
		if writer.flight.body.Len()+len(data) <= maxReplayBytes {
			_, _ = writer.flight.body.Write(data)
		} else {
			writer.flight.overflow = true
			writer.flight.body.Reset()
		}
	}
	writer.flight.mu.Unlock()
	return writer.ResponseWriter.Write(data)
}

func (writer *flightResponseWriter) Flush() {
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

type authFile struct {
	Tokens struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

// PrimeCredentialSources establishes only credential presence and validity at
// startup. It does not infer remaining quota: a source without fresh quota
// evidence enters PROBATION, and its first real Responses request is the
// bounded proof. Previously depleted sources are restored to probation only
// after their persisted reset epoch has passed.
func PrimeCredentialSources(store *state.Store) error {
	if store == nil {
		return errors.New("Relay Pool state is unavailable")
	}
	now := time.Now()
	for _, account := range store.Accounts() {
		_, credentialErr := readAuthFile(filepath.Join(account.CodexHome, "auth.json"))
		_, updateErr := store.UpdateCredentialSource(account.ID, func(source *state.CredentialSourceState) error {
			source.Enabled = account.Enabled
			source.LastObservedAt = now.UnixMilli()
			if credentialErr != nil {
				source.Connected = false
				source.AuthState = "disconnected"
				if account.PendingLogin {
					source.MembershipState = state.SourceLoginPending
				} else {
					source.MembershipState = state.SourceProvisioning
				}
				return nil
			}
			source.Connected = true
			source.AuthState = "authenticated"
			if !account.Enabled {
				source.MembershipState = state.SourceDraining
				return nil
			}
			if source.MembershipState == state.SourceDepleted {
				if source.ResetEpoch == 0 || source.ResetEpoch > now.Unix() {
					return nil
				}
			}
			if source.MembershipState != state.SourceAvailable && source.MembershipState != state.SourceActive {
				source.MembershipState = state.SourceProbation
				source.QuotaState = "unknown"
			}
			return nil
		})
		if updateErr != nil {
			return fmt.Errorf("prime quota source %s: %w", account.ID, updateErr)
		}
	}
	return nil
}

func (t *Transport) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if t.Store == nil {
		t.fail(writer, http.StatusServiceUnavailable, "pool_state_unavailable", "Relay Pool state is unavailable")
		return
	}
	if request.Method != http.MethodPost || request.URL.Path != "/v1/responses" {
		http.NotFound(writer, request)
		return
	}
	if !t.authorized(request) {
		t.fail(writer, http.StatusUnauthorized, "transport_authentication_failed", "Relay Pool transport authentication failed")
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maxRequestBytes+1))
	if err != nil || len(body) > maxRequestBytes {
		t.fail(writer, http.StatusRequestEntityTooLarge, "request_too_large", "Relay Pool request is too large")
		return
	}
	leaseID := firstHeader(request, "X-Client-Request-Id", "X-Request-Id")
	if leaseID == "" {
		leaseID = "relay-" + fmt.Sprint(time.Now().UnixNano())
	}
	logicalTurnID := leaseID
	for {
		flight, leader := t.beginRequestFlight(logicalTurnID)
		if leader {
			writer = &flightResponseWriter{ResponseWriter: writer, flight: flight}
			defer t.finishRequestFlight(logicalTurnID, flight)
			break
		}
		if t.replayRequestFlight(writer, request, flight) {
			return
		}
		// The previous leader ended without producing any HTTP response (for
		// example its renderer connection was canceled). Its flight is removed
		// on completion, so this request can become the new safe owner.
	}
	acquire := t.Store.AcquirePoolLease
	if t.BalancedPool {
		acquire = t.Store.AcquireBalancedPoolLease
	}
	lease, err := acquire(state.PoolLease{
		LeaseID: leaseID, LogicalSessionID: request.Header.Get("Session-Id"),
		LogicalTurnID: logicalTurnID, ThreadID: request.Header.Get("Thread-Id"),
	}, t.leaseTTL())
	if err != nil {
		code := "pool_exhausted"
		message := "Relay Pool has exhausted every usable quota source"
		status := http.StatusTooManyRequests
		lowerError := strings.ToLower(err.Error())
		if strings.Contains(lowerError, "requires recovery review") {
			code = "logical_turn_recovery_required"
			message = "This Relay request was interrupted by a previous app restart; continue with a new turn"
			status = http.StatusConflict
		} else if strings.Contains(lowerError, "already has active lease") {
			code = "logical_turn_already_active"
			message = "Relay Pool already has an active request for this logical turn"
			status = http.StatusConflict
		}
		t.fail(writer, status, code, message)
		return
	}
	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go t.heartbeatLease(lease.LeaseID, stopHeartbeat, heartbeatDone)
	defer func() {
		close(stopHeartbeat)
		<-heartbeatDone
	}()

	for {
		lease, _ = t.Store.MarkPoolLeaseProgress(lease.LeaseID, state.PoolLeaseDispatched, false, false)
		response, dispatchErr := t.dispatch(request.Context(), request, body, lease.SourceID)
		if dispatchErr != nil {
			if request.Context().Err() != nil {
				_ = t.Store.AbortPoolLease(lease.LeaseID, "")
				return
			}
			var credentialErr *sourceCredentialError
			if errors.As(dispatchErr, &credentialErr) {
				if t.DisableFailover {
					_ = t.Store.AbortPoolLease(lease.LeaseID, "compatibility-profile-unknown")
					t.fail(writer, http.StatusServiceUnavailable, "compatibility_profile_review", "Relay Pool compatibility profile requires review")
					return
				}
				lease, err = t.Store.MarkPoolSourceUnavailable(lease.LeaseID, lease.SourceID, "credential source requires attention")
				if err == nil {
					continue
				}
				_ = t.Store.AbortPoolLease(lease.LeaseID, "pool-depleted")
				t.fail(writer, http.StatusTooManyRequests, "pool_exhausted", "Relay Pool has exhausted every usable quota source")
				return
			}
			if !t.DisableFailover && retryableTransportError(dispatchErr) {
				failedSource := lease.SourceID
				fingerprint := classifyTransportError(dispatchErr)
				lease, err = t.Store.MarkPoolTransientFailure(lease.LeaseID, failedSource, fingerprint)
				if t.noteGlobalOutage(fingerprint, failedSource) {
					t.failProviderWideOutage(writer, lease, fingerprint)
					return
				}
				if err == nil {
					if !waitRetryBackoff(request.Context(), lease.AttemptNumber, "") {
						_ = t.Store.AbortPoolLease(lease.LeaseID, "")
						return
					}
					continue
				}
				t.failTransientRetryBudget(writer, lease, classifyTransportError(dispatchErr))
				return
			}
			_ = t.Store.AbortPoolLease(lease.LeaseID, "transport-error")
			t.fail(writer, http.StatusBadGateway, "upstream_transport_error", "Relay Pool could not reach the model service (transport error)")
			return
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			retryAfter := response.Header.Get("Retry-After")
			response.Body.Close()
			if !isQuotaResponse(response.StatusCode, responseBody) {
				if isCredentialResponse(response.StatusCode, responseBody) {
					if t.DisableFailover {
						_ = t.Store.AbortPoolLease(lease.LeaseID, "compatibility-profile-unknown")
						t.fail(writer, http.StatusServiceUnavailable, "compatibility_profile_review", "Relay Pool compatibility profile requires review")
						return
					}
					lease, err = t.Store.MarkPoolSourceUnavailable(lease.LeaseID, lease.SourceID, "credential source authentication failed")
					if err == nil {
						continue
					}
					_ = t.Store.AbortPoolLease(lease.LeaseID, "pool-depleted")
					t.fail(writer, http.StatusTooManyRequests, "pool_exhausted", "Relay Pool has exhausted every usable quota source")
					return
				}
				if !t.DisableFailover && retryableUpstreamStatus(response.StatusCode) {
					failedSource := lease.SourceID
					fingerprint := fmt.Sprintf("upstream_http_%d", response.StatusCode)
					lease, err = t.Store.MarkPoolTransientFailure(lease.LeaseID, failedSource, fmt.Sprintf("upstream HTTP %d before response commit", response.StatusCode))
					if t.noteGlobalOutage(fingerprint, failedSource) {
						t.failProviderWideOutage(writer, lease, fingerprint)
						return
					}
					if err == nil {
						if !waitRetryBackoff(request.Context(), lease.AttemptNumber, retryAfter) {
							_ = t.Store.AbortPoolLease(lease.LeaseID, "")
							return
						}
						continue
					}
					t.failTransientRetryBudget(writer, lease, fmt.Sprintf("upstream_http_%d", response.StatusCode))
					return
				}
				_ = t.Store.AbortPoolLease(lease.LeaseID, "upstream-error")
				t.fail(writer, http.StatusBadGateway, "upstream_http_error", safeUpstreamHTTPError(response.StatusCode, responseBody))
				return
			}
			if t.DisableFailover {
				_ = t.Store.AbortPoolLease(lease.LeaseID, "compatibility-profile-unknown")
				t.fail(writer, http.StatusServiceUnavailable, "compatibility_profile_review", "Relay Pool compatibility profile requires review")
				return
			}
			lease, err = t.Store.MarkPoolQuotaRejected(lease.LeaseID, lease.SourceID, "structured upstream quota rejection", 0)
			if err != nil {
				_ = t.Store.AbortPoolLease(lease.LeaseID, "pool-depleted")
				t.fail(writer, http.StatusTooManyRequests, "pool_exhausted", "Relay Pool has exhausted every usable quota source")
				return
			}
			continue
		}
		// The current ChatGPT Responses endpoint may omit Content-Type on a
		// chunked SSE response. Sniff a bounded prefix and put it back on the
		// body so the one public Relay API still accepts that provider shape
		// without buffering an entire model response in memory.
		if !isSSE(response.Header) {
			prefixBuffer := make([]byte, maxSSESniffBytes)
			prefixLength, _ := response.Body.Read(prefixBuffer)
			prefix := prefixBuffer[:prefixLength]
			response.Body = io.NopCloser(io.MultiReader(bytes.NewReader(prefix), response.Body))
			if looksLikeSSE(prefix) {
				response.Header.Set("Content-Type", "text/event-stream")
			}
		}
		if !isSSE(response.Header) {
			// Some provider revisions return a JSON error envelope with HTTP 200
			// instead of opening an SSE stream. Inspect that envelope before
			// rejecting the wire format so an account-limit response can still
			// fail over inside this same logical request.
			responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			response.Body.Close()
			if isQuotaResponse(response.StatusCode, responseBody) {
				if t.DisableFailover {
					_ = t.Store.AbortPoolLease(lease.LeaseID, "compatibility-profile-unknown")
					t.fail(writer, http.StatusServiceUnavailable, "compatibility_profile_review", "Relay Pool compatibility profile requires review")
					return
				}
				lease, err = t.Store.MarkPoolQuotaRejected(lease.LeaseID, lease.SourceID, "structured JSON quota rejection", 0)
				if err != nil {
					_ = t.Store.AbortPoolLease(lease.LeaseID, "pool-depleted")
					t.fail(writer, http.StatusTooManyRequests, "pool_exhausted", "Relay Pool has exhausted every usable quota source")
					return
				}
				continue
			}
			_ = t.Store.AbortPoolLease(lease.LeaseID, "unsupported-response")
			t.fail(writer, http.StatusBadGateway, "unsupported_upstream_response", fmt.Sprintf("Relay Pool model service returned an unsupported response (content type %q)", safeContentType(response.Header.Get("Content-Type"))))
			return
		}
		lease, _ = t.Store.MarkPoolLeaseProgress(lease.LeaseID, state.PoolLeaseAccepted, false, false)
		result, streamErr := t.forwardSSE(writer, response, lease)
		if streamErr == nil {
			_ = t.Store.CompletePoolLease(lease.LeaseID)
			t.clearGlobalOutageSamples()
			return
		}
		if errors.Is(streamErr, errClientStreamCanceled) {
			// The renderer/app-server intentionally went away (window close,
			// restart, or user cancel). Remove the lease without publishing a
			// router error or fabricating a recovery-required task.
			_ = t.Store.AbortPoolLease(lease.LeaseID, "")
			return
		}
		if errors.Is(streamErr, errTerminalResponseFailure) {
			// The upstream terminal failure/incomplete event has already been
			// forwarded. Close the lease without fabricating completion and without
			// replaying a deterministic model response on another credential.
			_ = t.Store.AbortPoolLease(lease.LeaseID, "")
			return
		}
		if errors.Is(streamErr, errLateQuotaRejection) {
			t.recordPoolError("stream_quota_after_output", http.StatusOK, "Relay Pool stopped safely after output began; retry the next turn to continue without replaying side effects.")
			return
		}
		if errors.Is(streamErr, errCommittedStreamFailure) {
			t.recordPoolError("stream_recovery_required", http.StatusOK, "Relay Pool stopped safely after output began; retry the next turn to continue without replaying side effects.")
			return
		}
		var retryableStream *retryableStreamError
		if errors.As(streamErr, &retryableStream) && !t.DisableFailover {
			failedSource := lease.SourceID
			lease, err = t.Store.MarkPoolTransientFailure(lease.LeaseID, failedSource, retryableStream.class)
			if err == nil {
				if !waitRetryBackoff(request.Context(), lease.AttemptNumber, "") {
					_ = t.Store.AbortPoolLease(lease.LeaseID, "")
					return
				}
				continue
			}
			t.failTransientRetryBudget(writer, lease, retryableStream.class)
			return
		}
		if !errors.Is(streamErr, errEarlyQuotaRejection) {
			_ = t.Store.AbortPoolLease(lease.LeaseID, "stream-error")
			t.fail(writer, http.StatusBadGateway, "upstream_stream_error", "Relay Pool stream ended before completion (upstream stream error)")
			return
		}
		if t.DisableFailover {
			_ = t.Store.AbortPoolLease(lease.LeaseID, "compatibility-profile-unknown")
			t.fail(writer, http.StatusServiceUnavailable, "compatibility_profile_review", "Relay Pool compatibility profile requires review")
			return
		}
		lease, err = t.Store.MarkPoolQuotaRejected(lease.LeaseID, result.SourceID, "structured stream quota rejection", 0)
		if err != nil {
			_ = t.Store.AbortPoolLease(lease.LeaseID, "pool-depleted")
			t.fail(writer, http.StatusTooManyRequests, "pool_exhausted", "Relay Pool has exhausted every usable quota source")
			return
		}
	}
}

func (t *Transport) beginRequestFlight(logicalTurnID string) (*requestFlight, bool) {
	now := time.Now()
	t.flightMu.Lock()
	defer t.flightMu.Unlock()
	if t.flights == nil {
		t.flights = make(map[string]*requestFlight)
	}
	for id, flight := range t.flights {
		flight.mu.Lock()
		expired := flight.completed && !flight.expiresAt.IsZero() && !flight.expiresAt.After(now)
		flight.mu.Unlock()
		if expired {
			delete(t.flights, id)
		}
	}
	if flight, exists := t.flights[logicalTurnID]; exists {
		return flight, false
	}
	flight := &requestFlight{done: make(chan struct{})}
	t.flights[logicalTurnID] = flight
	return flight, true
}

func (t *Transport) finishRequestFlight(logicalTurnID string, flight *requestFlight) {
	t.flightMu.Lock()
	defer t.flightMu.Unlock()
	flight.mu.Lock()
	if flight.completed {
		flight.mu.Unlock()
		return
	}
	flight.completed = true
	flight.expiresAt = time.Now().Add(requestReplayTTL)
	hasResponse := flight.status != 0
	flight.mu.Unlock()
	close(flight.done)
	if !hasResponse {
		if current := t.flights[logicalTurnID]; current == flight {
			delete(t.flights, logicalTurnID)
		}
	}
}

func (t *Transport) replayRequestFlight(writer http.ResponseWriter, request *http.Request, flight *requestFlight) bool {
	select {
	case <-request.Context().Done():
		return true
	case <-flight.done:
	}
	flight.mu.Lock()
	status := flight.status
	header := flight.header.Clone()
	body := append([]byte(nil), flight.body.Bytes()...)
	overflow := flight.overflow
	flight.mu.Unlock()
	if status == 0 {
		return false
	}
	if overflow {
		writePoolErrorCode(writer, http.StatusConflict, "logical_turn_response_too_large_to_replay", "The original Relay request completed, but its response exceeded the short-lived duplicate replay buffer")
		return true
	}
	copyResponseHeaders(writer.Header(), header)
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
	if flusher, ok := writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return true
}

func (t *Transport) authorized(request *http.Request) bool {
	expected := strings.TrimSpace(t.LocalBearerToken)
	if expected == "" {
		return true
	}
	provided := strings.TrimSpace(request.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(provided, prefix) {
		return false
	}
	provided = strings.TrimSpace(strings.TrimPrefix(provided, prefix))
	return len(provided) == len(expected) && subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func (t *Transport) heartbeatLease(leaseID string, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	interval := t.leaseTTL() / 3
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if _, err := t.Store.HeartbeatPoolLease(leaseID, t.leaseTTL()); err != nil {
				return
			}
		}
	}
}

func (t *Transport) leaseTTL() time.Duration {
	if t.LeaseTTL > 0 {
		return t.LeaseTTL
	}
	return 5 * time.Minute
}

func retryableUpstreamStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func retryableTransportError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
}

func classifyTransportError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "upstream_timeout"
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return "unexpected_eof"
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "connection reset"):
		return "connection_reset"
	case strings.Contains(lower, "refused_stream") || strings.Contains(lower, "http2") || strings.Contains(lower, "http/2"):
		return "http2_stream_reset"
	case strings.Contains(lower, "connection refused"):
		return "connection_refused"
	default:
		return "upstream_transport_error"
	}
}

func waitRetryBackoff(ctx context.Context, attempt uint64, retryAfter string) bool {
	delay := time.Duration(100+attempt*150) * time.Millisecond
	if delay > 1200*time.Millisecond {
		delay = 1200 * time.Millisecond
	}
	if value := strings.TrimSpace(retryAfter); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
			serverDelay := time.Duration(seconds) * time.Second
			if serverDelay > 2*time.Second {
				serverDelay = 2 * time.Second
			}
			if serverDelay > delay {
				delay = serverDelay
			}
		} else if date, err := http.ParseTime(value); err == nil {
			serverDelay := time.Until(date)
			if serverDelay > 2*time.Second {
				serverDelay = 2 * time.Second
			}
			if serverDelay > delay {
				delay = serverDelay
			}
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func correlationID(lease state.PoolLease, class string) string {
	sum := sha256.Sum256([]byte(lease.LeaseID + "\x00" + class + "\x00" + strconv.FormatUint(lease.AttemptNumber, 10)))
	return fmt.Sprintf("RP-%X", sum[:4])
}

func (t *Transport) failTransientRetryBudget(writer http.ResponseWriter, lease state.PoolLease, class string) {
	attempts := lease.AttemptNumber
	if attempts == 0 {
		attempts = 1
	}
	reference := correlationID(lease, class)
	_ = t.Store.AbortPoolLease(lease.LeaseID, "")
	t.fail(writer, http.StatusBadGateway, "retry_budget_exhausted", fmt.Sprintf(
		"Relay Pool could not obtain a complete model response after %d safe attempt(s) (%s; reference %s). No output or tool side effect was replayed.",
		attempts, sanitizeDiagnosticClass(class), reference,
	))
}

// noteGlobalOutage records only a sanitized transport fingerprint and the
// private source labels that observed it. It never persists credentials or
// upstream response bodies. The state is intentionally process-local: a
// restart already provides a natural boundary for a provider outage window.
func (t *Transport) noteGlobalOutage(fingerprint, sourceID string) bool {
	fingerprint = sanitizeDiagnosticClass(fingerprint)
	sourceID = strings.TrimSpace(sourceID)
	if t == nil || fingerprint == "" || sourceID == "" {
		return false
	}
	now := time.Now()
	t.outageMu.Lock()
	defer t.outageMu.Unlock()
	if t.outageSamples == nil {
		t.outageSamples = make(map[string]*outageSample)
	}
	for key, sample := range t.outageSamples {
		if sample == nil || now.Sub(sample.started) > globalOutageWindow {
			delete(t.outageSamples, key)
		}
	}
	sample := t.outageSamples[fingerprint]
	if sample == nil || now.Sub(sample.started) > globalOutageWindow {
		sample = &outageSample{started: now, sources: make(map[string]struct{})}
		t.outageSamples[fingerprint] = sample
	}
	sample.sources[sourceID] = struct{}{}
	return len(sample.sources) >= globalOutageThreshold
}

func (t *Transport) clearGlobalOutageSamples() {
	if t == nil {
		return
	}
	t.outageMu.Lock()
	t.outageSamples = nil
	t.outageMu.Unlock()
}

func (t *Transport) failProviderWideOutage(writer http.ResponseWriter, lease state.PoolLease, fingerprint string) {
	attempts := lease.AttemptNumber
	if attempts == 0 {
		attempts = 1
	}
	reference := correlationID(lease, "provider_wide_outage")
	_ = t.Store.AbortPoolLease(lease.LeaseID, "provider-wide-outage")
	t.fail(writer, http.StatusServiceUnavailable, "provider_wide_outage", fmt.Sprintf(
		"Relay Pool observed the same temporary upstream failure across at least %d independent sources after %d safe attempt(s) (%s; reference %s). Retry later; no output or tool side effect was replayed.",
		globalOutageThreshold, attempts, sanitizeDiagnosticClass(fingerprint), reference,
	))
}

func sanitizeDiagnosticClass(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > 64 {
		return "upstream_transport_error"
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' && character != '-' {
			return "upstream_transport_error"
		}
	}
	return value
}

func (t *Transport) dispatch(ctx context.Context, original *http.Request, body []byte, sourceID string) (*http.Response, error) {
	account, ok := t.Store.Account(sourceID)
	if !ok {
		return nil, fmt.Errorf("quota source is unavailable")
	}
	var accessToken, accountID string
	var err error
	if t.LoadCredentials != nil {
		accessToken, accountID, err = t.LoadCredentials(account)
		if err != nil {
			return nil, &sourceCredentialError{cause: err}
		}
		accessToken = strings.TrimSpace(accessToken)
		accountID = strings.TrimSpace(accountID)
		if accessToken == "" || accountID == "" {
			return nil, &sourceCredentialError{cause: errors.New("source credentials are incomplete")}
		}
	} else {
		credentials, readErr := readAuthFile(filepath.Join(account.CodexHome, "auth.json"))
		if readErr != nil {
			return nil, &sourceCredentialError{cause: readErr}
		}
		accessToken = credentials.Tokens.AccessToken
		accountID = credentials.Tokens.AccountID
	}
	endpoint := strings.TrimSpace(t.UpstreamURL)
	if endpoint == "" {
		endpoint = defaultUpstreamURL
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	copyRequestHeaders(request.Header, original.Header)
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("ChatGPT-Account-ID", accountID)
	request.Header.Set("Accept", "text/event-stream")
	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	return client.Do(request)
}

type sourceCredentialError struct{ cause error }

func (e *sourceCredentialError) Error() string { return "credential source is unavailable" }
func (e *sourceCredentialError) Unwrap() error { return e.cause }

func readAuthFile(path string) (authFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return authFile{}, fmt.Errorf("open source credentials: %w", err)
	}
	defer file.Close()
	var credentials authFile
	if err := json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&credentials); err != nil {
		return authFile{}, fmt.Errorf("decode source credentials: %w", err)
	}
	if strings.TrimSpace(credentials.Tokens.AccessToken) == "" || strings.TrimSpace(credentials.Tokens.AccountID) == "" {
		return authFile{}, errors.New("source credentials are incomplete")
	}
	return credentials, nil
}

func copyRequestHeaders(target, source http.Header) {
	for key, values := range source {
		switch strings.ToLower(key) {
		case "authorization", "chatgpt-account-id", "host", "content-length", "connection", "proxy-connection", "cookie":
			continue
		}
		for _, value := range values {
			target.Add(key, value)
		}
	}
}

func copyResponseHeaders(target, source http.Header) {
	for key, values := range source {
		switch strings.ToLower(key) {
		case "content-length", "connection", "transfer-encoding":
			continue
		}
		for _, value := range values {
			target.Add(key, value)
		}
	}
}

var errEarlyQuotaRejection = errors.New("quota rejected before visible output")
var errLateQuotaRejection = errors.New("quota rejected after visible output")
var errCommittedStreamFailure = errors.New("stream failed after response commitment")
var errClientStreamCanceled = errors.New("Relay client canceled the response stream")
var errTerminalResponseFailure = errors.New("upstream returned a terminal failed or incomplete response")

type retryableStreamError struct {
	class string
	cause error
}

func (e *retryableStreamError) Error() string {
	if e == nil || e.cause == nil {
		return "retryable upstream stream failure"
	}
	return "retryable upstream stream failure: " + e.cause.Error()
}

func (e *retryableStreamError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (t *Transport) forwardSSE(writer http.ResponseWriter, response *http.Response, lease state.PoolLease) (state.PoolLease, error) {
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	var buffered bytes.Buffer
	var lastSequenceNumber int64
	var haveSequenceNumber bool
	responseID := ""
	for {
		event, err := readSSEEvent(reader)
		debugSSEFrame("initial", event, err)
		if len(event) > 0 {
			// A ReadBytes call can return a partial SSE frame together with
			// io.EOF (or a connection reset). Never forward that unterminated
			// frame: doing so leaves the native Responses parser in the middle
			// of a JSON object and it reports the misleading
			// "stream disconnected before response.completed" error even when
			// Relay appends its own recovery terminal event below.
			if err != nil && !completeSSEEvent(event) {
				if responseRequestCanceled(response, err) {
					return lease, errClientStreamCanceled
				}
				if errors.Is(err, io.EOF) {
					return lease, &retryableStreamError{class: "clean_eof_without_terminal", cause: io.ErrUnexpectedEOF}
				}
				return lease, &retryableStreamError{class: classifyTransportError(err), cause: err}
			}
			if buffered.Len()+len(event) > maxBufferedSSEBytes {
				return lease, errors.New("initial response stream exceeds Relay buffer")
			}
			buffered.Write(event)
			if sequence, ok := sseSequenceNumber(event); ok {
				lastSequenceNumber = sequence
				haveSequenceNumber = true
			}
			if id, ok := sseResponseID(event); ok {
				responseID = id
			}
			category, visible, sideEffect := classifySSEEvent(event)
			if category == "quota" {
				return lease, errEarlyQuotaRejection
			}
			terminal := ""
			if completeSSEEvent(event) {
				terminal = classifySSETerminal(event)
			}
			if visible || sideEffect {
				lease, _ = t.Store.MarkPoolLeaseProgress(lease.LeaseID, state.PoolLeaseStreaming, visible, sideEffect)
				copyResponseHeaders(writer.Header(), response.Header)
				writer.WriteHeader(response.StatusCode)
				_, _ = writer.Write(buffered.Bytes())
				if flusher, ok := writer.(http.Flusher); ok {
					flusher.Flush()
				}
				if terminal == "completed" {
					return lease, nil
				}
				if terminal == "failed" || terminal == "incomplete" {
					return lease, errTerminalResponseFailure
				}
				var pendingTerminalEvents [][]byte
				for {
					readTimeout := sseIdleRecoveryTimeout
					if len(pendingTerminalEvents) > 0 {
						readTimeout = sseTerminalGraceTimeout
					}
					remaining, remainingErr := readSSEEventWithKeepaliveTimeout(reader, writer, responseContext(response), readTimeout)
					debugSSEFrame("continuation", remaining, remainingErr)
					if len(remaining) > 0 {
						remainingComplete := completeSSEEvent(remaining)
						if remainingComplete {
							if sequence, ok := sseSequenceNumber(remaining); ok {
								lastSequenceNumber = sequence
								haveSequenceNumber = true
							}
							if id, ok := sseResponseID(remaining); ok {
								responseID = id
							}
						}
						category, moreVisible, moreSideEffect := classifySSEEvent(remaining)
						if category == "quota" {
							lease, _ = t.Store.MarkPoolQuotaRejected(lease.LeaseID, lease.SourceID, "quota rejection after visible output", 0)
							recoverySequence := nextSSESequence(lastSequenceNumber, haveSequenceNumber)
							debugRecoverySSE(recoverySequence, responseID)
							_, _ = writer.Write(sanitizedRecoverySSE(recoverySequence, responseID))
							if flusher, ok := writer.(http.Flusher); ok {
								flusher.Flush()
							}
							return lease, errLateQuotaRejection
						}
						// Hold the final output-item frame for a short grace period. If
						// response.completed follows, it is flushed together with the
						// candidate. If the provider goes silent, the candidate is
						// flushed immediately before Relay's recovery terminal.
						if remainingComplete && isSSETerminalCandidate(remaining) && classifySSETerminal(remaining) == "" {
							debugStreamDecision("hold_terminal_candidate", nil)
							pendingTerminalEvents = append(pendingTerminalEvents, append([]byte(nil), remaining...))
							continue
						}
						// Only complete frames are safe to forward. A partial frame is
						// intentionally discarded before the recovery terminal below.
						if remainingComplete {
							for _, pending := range pendingTerminalEvents {
								_, _ = writer.Write(pending)
								if flusher, ok := writer.(http.Flusher); ok {
									flusher.Flush()
								}
							}
							pendingTerminalEvents = nil
							_, _ = writer.Write(remaining)
							if flusher, ok := writer.(http.Flusher); ok {
								flusher.Flush()
							}
							if moreVisible || moreSideEffect {
								lease, _ = t.Store.MarkPoolLeaseProgress(lease.LeaseID, state.PoolLeaseStreaming, moreVisible, moreSideEffect)
							}
						}
						terminal = ""
						if remainingComplete {
							terminal = classifySSETerminal(remaining)
						}
						if terminal == "completed" {
							return lease, nil
						}
						if terminal == "failed" || terminal == "incomplete" {
							return lease, errTerminalResponseFailure
						}
					}
					if remainingErr != nil {
						for _, pending := range pendingTerminalEvents {
							_, _ = writer.Write(pending)
							if flusher, ok := writer.(http.Flusher); ok {
								flusher.Flush()
							}
						}
						pendingTerminalEvents = nil
						// Closing/restarting the Relay renderer cancels the local
						// app-server request and therefore the upstream HTTP context.
						// That is an expected client lifecycle event, not an upstream
						// stream failure. Persisting RECOVERY_REQUIRED here leaves a
						// stale lease that the restarted app retries forever.
						if responseRequestCanceled(response, remainingErr) {
							// The native app-server can cancel its HTTP request when it
							// sees the final output-item boundary but the provider has
							// not sent response.completed yet. If that boundary is held
							// here, finish the logical turn with Relay's terminal event
							// before treating the cancellation as an ordinary user stop.
							if len(pendingTerminalEvents) == 0 {
								debugStreamDecision("client_canceled_after_output", remainingErr)
								return lease, errClientStreamCanceled
							}
							debugStreamDecision("recovery_terminal_after_output_client_cancel", remainingErr)
							_, _ = t.Store.MarkPoolLeaseProgress(lease.LeaseID, state.PoolLeaseRecoveryRequired, false, false)
							recoverySequence := nextSSESequence(lastSequenceNumber, haveSequenceNumber)
							debugRecoverySSE(recoverySequence, responseID)
							_, _ = writer.Write(sanitizedRecoverySSE(recoverySequence, responseID))
							if flusher, ok := writer.(http.Flusher); ok {
								flusher.Flush()
							}
							return lease, errCommittedStreamFailure
						}
						debugStreamDecision("recovery_terminal_after_output", remainingErr)
						// Any EOF/reset after the commit boundary but before an
						// explicit terminal event is uncertain. Never replay it. Send a
						// standards-shaped terminal event so the native Responses
						// consumer does not turn a deliberate recovery stop into the
						// misleading "stream disconnected before completion" error.
						_, _ = t.Store.MarkPoolLeaseProgress(lease.LeaseID, state.PoolLeaseRecoveryRequired, false, false)
						recoverySequence := nextSSESequence(lastSequenceNumber, haveSequenceNumber)
						debugRecoverySSE(recoverySequence, responseID)
						_, _ = writer.Write(sanitizedRecoverySSE(recoverySequence, responseID))
						if flusher, ok := writer.(http.Flusher); ok {
							flusher.Flush()
						}
						return lease, errCommittedStreamFailure
					}
				}
			}
			if terminal == "completed" || terminal == "failed" || terminal == "incomplete" {
				copyResponseHeaders(writer.Header(), response.Header)
				writer.WriteHeader(response.StatusCode)
				_, writeErr := writer.Write(buffered.Bytes())
				if writeErr != nil {
					return lease, writeErr
				}
				if terminal == "completed" {
					return lease, nil
				}
				return lease, errTerminalResponseFailure
			}
		}
		if err != nil {
			if responseRequestCanceled(response, err) {
				debugStreamDecision("client_canceled_before_commit", err)
				return lease, errClientStreamCanceled
			}
			if errors.Is(err, io.EOF) {
				// A provider can advertise SSE and still return one raw JSON
				// error envelope when the account has no Codex messages left.
				// Inspect the buffered payload before committing it to the native
				// app-server; otherwise the app-server turns the provider limit
				// into an opaque -32600 and the pool never gets a chance to retry.
				if isQuotaResponse(response.StatusCode, buffered.Bytes()) {
					return lease, errEarlyQuotaRejection
				}
				// EOF without response.completed/failed/incomplete is not a
				// successful response. Because nothing was committed yet, the
				// exact logical turn can safely rotate to another pool source.
				return lease, &retryableStreamError{class: "clean_eof_without_terminal", cause: io.ErrUnexpectedEOF}
			}
			return lease, &retryableStreamError{class: classifyTransportError(err), cause: err}
		}
	}
}

func classifySSETerminal(event []byte) string {
	switch sseEventName(event) {
	case "response.completed":
		return "completed"
	case "response.incomplete":
		return "incomplete"
	case "response.failed":
		return "failed"
	}
	// A few compatible upstreams omit the SSE event field and put the event
	// type/status only in the JSON envelope. Inspect only top-level fields (or
	// the response object), never arbitrary nested item.status values: an
	// ordinary response.output_item.done carries item.status="completed" but
	// is not the terminal response.completed event.
	for _, line := range bytes.Split(event, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		var envelope struct {
			Type     string `json:"type"`
			Status   string `json:"status"`
			Response struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"response"`
		}
		if json.Unmarshal(payload, &envelope) != nil {
			continue
		}
		for _, value := range []string{envelope.Type, envelope.Status, envelope.Response.Type, envelope.Response.Status} {
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "response.completed", "completed":
				return "completed"
			case "response.incomplete", "incomplete":
				return "incomplete"
			case "response.failed", "failed":
				return "failed"
			}
		}
	}
	return ""
}

func completeSSEEvent(event []byte) bool {
	return bytes.HasSuffix(event, []byte("\n\n")) || bytes.HasSuffix(event, []byte("\r\n\r\n"))
}

func responseRequestCanceled(response *http.Response, streamErr error) bool {
	if errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded) {
		return true
	}
	return response != nil && response.Request != nil && response.Request.Context().Err() != nil
}

func nextSSESequence(last int64, present bool) int64 {
	if !present || last < 0 {
		return 0
	}
	return last + 1
}

func sseSequenceNumber(event []byte) (int64, bool) {
	for _, line := range bytes.Split(event, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		var envelope struct {
			SequenceNumber *int64 `json:"sequence_number"`
		}
		if json.Unmarshal(payload, &envelope) == nil && envelope.SequenceNumber != nil {
			return *envelope.SequenceNumber, true
		}
	}
	return 0, false
}

func sseResponseID(event []byte) (string, bool) {
	for _, line := range bytes.Split(event, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		var envelope struct {
			ID       string `json:"id"`
			Response struct {
				ID string `json:"id"`
			} `json:"response"`
		}
		if json.Unmarshal(payload, &envelope) != nil {
			continue
		}
		if id := strings.TrimSpace(envelope.Response.ID); id != "" {
			return id, true
		}
		if id := strings.TrimSpace(envelope.ID); id != "" {
			return id, true
		}
	}
	return "", false
}

// isSSETerminalCandidate identifies the last output frame that some Codex
// upstreams emit immediately before they decide whether to send a terminal
// response event.  Holding this frame for the short terminal grace window
// lets response.completed/failed/incomplete arrive without exposing an
// output-item boundary that causes the native app-server to cancel the stream.
// It is deliberately narrow: no arbitrary output frame is deferred, and a
// candidate is still flushed unchanged before Relay emits its own recovery
// terminal when the upstream remains silent.
func isSSETerminalCandidate(event []byte) bool {
	return sseEventName(event) == "response.output_item.done"
}

func sseEventName(event []byte) string {
	for _, line := range bytes.Split(event, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, []byte("event:")) {
			return safeSSEEventName(string(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("event:")))))
		}
	}
	return ""
}

// debugSSEFrame is deliberately opt-in and logs only event metadata. It is a
// field diagnostic for an installed app-server that reports a stream
// disconnect; credentials, prompts, output text, and raw upstream payloads
// are never written to stderr.
func debugSSEFrame(phase string, event []byte, err error) {
	if os.Getenv("CODEX_RELAY_DEBUG_SSE") != "1" {
		return
	}
	eventName := sseEventName(event)
	sequence, present := sseSequenceNumber(event)
	errText := ""
	if err != nil {
		errText = sanitizeDiagnosticClass(err.Error())
	}
	responseID, responsePresent := sseResponseID(event)
	if responsePresent {
		responseID = safeSSEEventName(responseID)
	}
	fmt.Fprintf(os.Stderr, "codex-mux: sse phase=%s event=%s complete=%t sequence_present=%t sequence=%d response_id_present=%t response_id=%s err=%s\n", sanitizeDiagnosticClass(phase), eventName, completeSSEEvent(event), present, sequence, responsePresent, responseID, errText)
}

func safeSSEEventName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > 96 {
		return "unknown"
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' && character != '-' && character != '.' {
			return "unknown"
		}
	}
	return value
}

func debugRecoverySSE(sequenceNumber int64, responseID string) {
	if os.Getenv("CODEX_RELAY_DEBUG_SSE") != "1" {
		return
	}
	present := strings.TrimSpace(responseID) != ""
	if !present {
		responseID = "fallback"
	}
	fmt.Fprintf(os.Stderr, "codex-mux: sse recovery event=response.failed complete=true sequence=%d response_id_present=%t response_id=%s\n", sequenceNumber, present, safeSSEEventName(responseID))
}

func debugStreamDecision(decision string, err error) {
	if os.Getenv("CODEX_RELAY_DEBUG_SSE") != "1" {
		return
	}
	errText := ""
	if err != nil {
		errText = sanitizeDiagnosticClass(err.Error())
	}
	fmt.Fprintf(os.Stderr, "codex-mux: sse decision=%s err=%s\n", sanitizeDiagnosticClass(decision), errText)
}

func sanitizedRecoverySSE(sequenceNumber int64, responseID string) []byte {
	// response.failed is the terminal Responses event understood by both the
	// current and older Codex app-server adapters. It tells the native client
	// that the turn ended deliberately and must be continued in a new turn,
	// rather than making it infer a transport disconnect. The payload is
	// intentionally credential-free and contains no upstream body or prompt.
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		responseID = "resp_relay_pool_recovery"
	}
	payload := map[string]any{
		"type":            "response.failed",
		"sequence_number": sequenceNumber,
		"response": map[string]any{
			"id":         responseID,
			"object":     "response",
			"created_at": time.Now().Unix(),
			"status":     "failed",
			"error": map[string]string{
				"code": "relay_pool_recovery_required",
				// The current native app-server only propagates the human-readable
				// error.message from response.failed into turn/completed. Include the
				// stable marker there as well so the unified mux can recognize and
				// normalize the notification instead of showing its generic
				// "stream disconnected before completion" wrapper.
				"message": "Relay Pool stopped safely after output began; continue with a new turn to avoid replaying side effects. (relay_pool_recovery_required)",
			},
			"incomplete_details":   nil,
			"instructions":         nil,
			"max_output_tokens":    nil,
			"model":                "relay_pool",
			"output":               []any{},
			"parallel_tool_calls":  true,
			"previous_response_id": nil,
			"reasoning":            map[string]any{"effort": nil, "summary": nil},
			"store":                false,
			"text":                 map[string]any{"format": map[string]string{"type": "text"}},
			"tool_choice":          "auto",
			"tools":                []any{},
			"top_p":                nil,
			"truncation":           "disabled",
			"usage":                nil,
			"metadata":             map[string]string{"relay_error_code": "relay_pool_recovery_required"},
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return []byte("event: response.failed\ndata: {\"type\":\"response.failed\"}\n\n")
	}
	return append([]byte("event: response.failed\ndata: "), append(encoded, []byte("\n\n")...)...)
}

func readSSEEvent(reader *bufio.Reader) ([]byte, error) {
	var event bytes.Buffer
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			event.Write(line)
			if bytes.Equal(line, []byte("\n")) || bytes.Equal(line, []byte("\r\n")) {
				return event.Bytes(), nil
			}
		}
		if err != nil {
			return event.Bytes(), err
		}
	}
}

type sseReadResult struct {
	event []byte
	err   error
}

var errSSEIdleRecoveryTimeout = errors.New("upstream SSE idle after visible output")

// readSSEEventWithKeepalive prevents an upstream that has already emitted
// visible output from being mistaken for a dead client while its final frame
// is still in flight. SSE comments are ignored by Responses consumers and are
// flushed only after the downstream response is committed; they never become
// assistant output or a replayable side effect.
func readSSEEventWithKeepalive(reader *bufio.Reader, writer http.ResponseWriter, ctx context.Context) ([]byte, error) {
	return readSSEEventWithKeepaliveTimeout(reader, writer, ctx, sseIdleRecoveryTimeout)
}

func readSSEEventWithKeepaliveTimeout(reader *bufio.Reader, writer http.ResponseWriter, ctx context.Context, idleTimeout time.Duration) ([]byte, error) {
	if idleTimeout <= 0 {
		idleTimeout = sseIdleRecoveryTimeout
	}
	result := make(chan sseReadResult, 1)
	go func() {
		event, err := readSSEEvent(reader)
		result <- sseReadResult{event: event, err: err}
	}()
	ticker := time.NewTicker(sseKeepaliveInterval)
	defer ticker.Stop()
	idleTimer := time.NewTimer(idleTimeout)
	defer idleTimer.Stop()
	for {
		select {
		case read := <-result:
			return read.event, read.err
		case <-ticker.C:
			if writer == nil {
				continue
			}
			if _, err := writer.Write([]byte(": relay-pool-keepalive\n\n")); err != nil {
				return nil, err
			}
			if flusher, ok := writer.(http.Flusher); ok {
				flusher.Flush()
			}
		case <-idleTimer.C:
			return nil, errSSEIdleRecoveryTimeout
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func responseContext(response *http.Response) context.Context {
	if response != nil && response.Request != nil && response.Request.Context() != nil {
		return response.Request.Context()
	}
	return context.Background()
}

func classifySSEEvent(event []byte) (category string, visible, sideEffect bool) {
	lower := strings.ToLower(string(event))
	// Providers do not use one stable error code for a streaming quota
	// rejection. In particular, ChatGPT can return HTTP 200 followed by a
	// response.failed event whose only useful signal is the human-readable
	// “You've hit your usage limit” or “You're out of Codex messages” message.
	// Treat both code-style and message-style limit markers as pre-output quota
	// failures so the exact request can continue through the next source in the
	// same pool. The provider has emitted both event names and failed response
	// objects across Codex releases, so the failure check intentionally accepts
	// all of those wire shapes.
	failureEvent := strings.Contains(lower, "response.failed") ||
		strings.Contains(lower, "event: error") ||
		strings.Contains(lower, `"type":"error"`) ||
		strings.Contains(lower, `"type": "error"`) ||
		strings.Contains(lower, `"status":"failed"`) ||
		strings.Contains(lower, `"status": "failed"`)
	if quotaEvidenceInText(lower) && failureEvent {
		return "quota", false, false
	}
	visible = strings.Contains(lower, "response.output_text.delta") || strings.Contains(lower, "response.reasoning_summary_text.delta")
	sideEffect = strings.Contains(lower, `"type":"function_call"`) || strings.Contains(lower, `"type": "function_call"`)
	return "", visible, sideEffect
}

func firstHeader(request *http.Request, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(request.Header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func isQuotaResponse(status int, body []byte) bool {
	// A 429/529 is a quota/rate-limit response even when the provider returns an
	// empty body. Other status codes need explicit, credential-free quota
	// evidence. A few Codex releases return a JSON error with HTTP 200, so allow
	// that shape too while never treating a normal successful JSON response as a
	// quota rejection merely because it contains a usage field.
	if status == http.StatusTooManyRequests || status == 529 {
		return true
	}
	if !quotaEvidenceInText(strings.ToLower(string(body))) {
		return false
	}
	if status >= 400 {
		return true
	}
	return jsonErrorPayload(body)
}

// quotaEvidenceInText accepts the stable semantic signals emitted by the
// ChatGPT/Codex Responses service. The service has changed the machine code
// and the prose several times; keeping the matcher in one place makes the
// HTTP and SSE classifiers evolve together. These phrases are deliberately
// account-limit specific (for example, “message limit” and “out of Codex
// messages”), so a normal model context-length error is not routed to another
// subscription by accident.
func quotaEvidenceInText(lower string) bool {
	for _, marker := range []string{
		"usage_limit", "usage limit", "usage-limit", "usage exhausted",
		"rate_limit", "rate limit", "rate-limit",
		"quota", "insufficient_quota", "insufficient quota",
		"too_many_requests", "too many requests",
		"limit_reached", "limit reached", "limit has been reached",
		"out of codex messages", "out of codex message",
		"out of messages", "out of message",
		"run out of codex messages", "run out of messages",
		"you've run out", "you have run out", "no messages left",
		"no codex messages left", "message limit", "codex message limit",
		"weekly usage limit", "monthly usage limit", "daily usage limit",
		"out of credits", "credits exhausted", "no credits available",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func jsonErrorPayload(body []byte) bool {
	var payload struct {
		Type     string          `json:"type"`
		Error    json.RawMessage `json:"error"`
		Status   string          `json:"status"`
		Response struct {
			Error  json.RawMessage `json:"error"`
			Status string          `json:"status"`
		} `json:"response"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	for _, raw := range []json.RawMessage{payload.Error, payload.Response.Error} {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
			return true
		}
	}
	return strings.EqualFold(strings.TrimSpace(payload.Status), "failed") ||
		strings.EqualFold(strings.TrimSpace(payload.Response.Status), "failed") ||
		strings.EqualFold(strings.TrimSpace(payload.Type), "error") ||
		strings.EqualFold(strings.TrimSpace(payload.Type), "response.failed")
}

func isCredentialResponse(status int, body []byte) bool {
	if status == http.StatusUnauthorized {
		return true
	}
	if status != http.StatusForbidden {
		return false
	}
	lower := strings.ToLower(string(body))
	for _, marker := range []string{"unauthorized", "invalid token", "invalid_api_key", "authentication"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// safeUpstreamHTTPError gives the client enough detail to diagnose a rejected
// request without copying an upstream body into the app-server protocol. In
// particular, response bodies may contain account identifiers, prompt text,
// local paths, or other provider diagnostics that do not belong in the Relay
// UI. Only a small machine-readable code/type is allowed through.
func safeUpstreamHTTPError(status int, body []byte) string {
	var payload struct {
		Code  string `json:"code"`
		Type  string `json:"type"`
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &payload)
	code := safeProviderToken(payload.Error.Code)
	if code == "" {
		code = safeProviderToken(payload.Code)
	}
	if code == "" {
		code = safeProviderToken(payload.Error.Type)
	}
	if code == "" {
		code = safeProviderToken(payload.Type)
	}
	if code != "" {
		return fmt.Sprintf("Relay Pool model service rejected the request (HTTP %d; upstream code %s)", status, code)
	}
	return fmt.Sprintf("Relay Pool model service rejected the request (HTTP %d)", status)
}

func safeProviderToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 80 {
		return ""
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '_' && character != '-' && character != '.' {
			return ""
		}
	}
	return value
}

func safeContentType(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r", ""), "\n", ""))
	if len(value) > 80 {
		return value[:80]
	}
	if value == "" {
		return "unknown"
	}
	return value
}

func isSSE(header http.Header) bool {
	return strings.Contains(strings.ToLower(header.Get("Content-Type")), "text/event-stream")
}

func looksLikeSSE(prefix []byte) bool {
	for _, line := range bytes.Split(prefix, []byte{'\n'}) {
		line = bytes.TrimSpace(bytes.TrimSuffix(line, []byte{'\r'}))
		if bytes.HasPrefix(line, []byte("event:")) || bytes.HasPrefix(line, []byte("data:")) || bytes.HasPrefix(line, []byte(":")) {
			return true
		}
	}
	return false
}

func (t *Transport) recordPoolError(code string, status int, message string) {
	if t == nil || t.Store == nil {
		return
	}
	_ = t.Store.RecordPoolError(code, status, message)
}

func (t *Transport) fail(writer http.ResponseWriter, status int, code, message string) {
	t.recordPoolError(code, status, message)
	writePoolErrorCode(writer, status, code, message)
}

func writePoolError(writer http.ResponseWriter, status int, message string) {
	writePoolErrorCode(writer, status, "relay_pool_unavailable", message)
}

func writePoolErrorCode(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]any{
		"type": "relay_pool_error", "code": code, "message": message,
	}})
}
