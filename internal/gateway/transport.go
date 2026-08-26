package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LightHaru/codex-relay/internal/state"
)

const (
	defaultUpstreamURL  = "https://chatgpt.com/backend-api/codex/responses"
	maxRequestBytes     = 64 << 20
	maxBufferedSSEBytes = 2 << 20
)

type Transport struct {
	Store            *state.Store
	Client           *http.Client
	UpstreamURL      string
	LeaseTTL         time.Duration
	LocalBearerToken string
	// LoadCredentials is an optional in-process credential loader used by
	// deterministic integration tests and controlled embedders. Production
	// Relay leaves it nil, so credentials continue to be read from the selected
	// isolated source home for every dispatch.
	LoadCredentials func(state.Account) (accessToken, accountID string, err error)
	// DisableFailover is set for an unreviewed app-server compatibility
	// profile. Relay still exposes one API, but it fails closed instead of
	// attempting a credential continuation that has not been protocol-tested.
	DisableFailover bool
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
		writePoolError(writer, http.StatusServiceUnavailable, "Relay Pool state is unavailable")
		return
	}
	if request.Method != http.MethodPost || request.URL.Path != "/v1/responses" {
		http.NotFound(writer, request)
		return
	}
	if !t.authorized(request) {
		writePoolError(writer, http.StatusUnauthorized, "Relay Pool transport authentication failed")
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maxRequestBytes+1))
	if err != nil || len(body) > maxRequestBytes {
		writePoolError(writer, http.StatusRequestEntityTooLarge, "Relay Pool request is too large")
		return
	}
	leaseID := firstHeader(request, "X-Client-Request-Id", "X-Request-Id")
	if leaseID == "" {
		leaseID = "relay-" + fmt.Sprint(time.Now().UnixNano())
	}
	logicalTurnID := leaseID
	lease, err := t.Store.AcquirePoolLease(state.PoolLease{
		LeaseID: leaseID, LogicalSessionID: request.Header.Get("Session-Id"),
		LogicalTurnID: logicalTurnID, ThreadID: request.Header.Get("Thread-Id"),
	}, t.leaseTTL())
	if err != nil {
		writePoolError(writer, http.StatusTooManyRequests, "Relay Pool has exhausted every usable quota source")
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
			var credentialErr *sourceCredentialError
			if errors.As(dispatchErr, &credentialErr) {
				if t.DisableFailover {
					_ = t.Store.AbortPoolLease(lease.LeaseID, "compatibility-profile-unknown")
					writePoolError(writer, http.StatusServiceUnavailable, "Relay Pool compatibility profile requires review")
					return
				}
				lease, err = t.Store.MarkPoolSourceUnavailable(lease.LeaseID, lease.SourceID, "credential source requires attention")
				if err == nil {
					continue
				}
				_ = t.Store.AbortPoolLease(lease.LeaseID, "pool-depleted")
				writePoolError(writer, http.StatusTooManyRequests, "Relay Pool has exhausted every usable quota source")
				return
			}
			_ = t.Store.AbortPoolLease(lease.LeaseID, "transport-error")
			writePoolError(writer, http.StatusBadGateway, "Relay Pool could not reach the model service (transport error)")
			return
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			response.Body.Close()
			if !isQuotaResponse(response.StatusCode, responseBody) {
				if isCredentialResponse(response.StatusCode, responseBody) {
					if t.DisableFailover {
						_ = t.Store.AbortPoolLease(lease.LeaseID, "compatibility-profile-unknown")
						writePoolError(writer, http.StatusServiceUnavailable, "Relay Pool compatibility profile requires review")
						return
					}
					lease, err = t.Store.MarkPoolSourceUnavailable(lease.LeaseID, lease.SourceID, "credential source authentication failed")
					if err == nil {
						continue
					}
					_ = t.Store.AbortPoolLease(lease.LeaseID, "pool-depleted")
					writePoolError(writer, http.StatusTooManyRequests, "Relay Pool has exhausted every usable quota source")
					return
				}
				_ = t.Store.AbortPoolLease(lease.LeaseID, "upstream-error")
				writePoolError(writer, http.StatusBadGateway, safeUpstreamHTTPError(response.StatusCode, responseBody))
				return
			}
			if t.DisableFailover {
				_ = t.Store.AbortPoolLease(lease.LeaseID, "compatibility-profile-unknown")
				writePoolError(writer, http.StatusServiceUnavailable, "Relay Pool compatibility profile requires review")
				return
			}
			lease, err = t.Store.MarkPoolQuotaRejected(lease.LeaseID, lease.SourceID, "structured upstream quota rejection", 0)
			if err != nil {
				_ = t.Store.AbortPoolLease(lease.LeaseID, "pool-depleted")
				writePoolError(writer, http.StatusTooManyRequests, "Relay Pool has exhausted every usable quota source")
				return
			}
			continue
		}
		if !isSSE(response.Header) {
			response.Body.Close()
			_ = t.Store.AbortPoolLease(lease.LeaseID, "unsupported-response")
			writePoolError(writer, http.StatusBadGateway, fmt.Sprintf("Relay Pool model service returned an unsupported response (content type %q)", safeContentType(response.Header.Get("Content-Type"))))
			return
		}
		lease, _ = t.Store.MarkPoolLeaseProgress(lease.LeaseID, state.PoolLeaseAccepted, false, false)
		result, streamErr := t.forwardSSE(writer, response, lease)
		if streamErr == nil {
			_ = t.Store.CompletePoolLease(lease.LeaseID)
			return
		}
		if errors.Is(streamErr, errLateQuotaRejection) || errors.Is(streamErr, errCommittedStreamFailure) {
			return
		}
		if !errors.Is(streamErr, errEarlyQuotaRejection) {
			_ = t.Store.AbortPoolLease(lease.LeaseID, "stream-error")
			writePoolError(writer, http.StatusBadGateway, "Relay Pool stream ended before completion (upstream stream error)")
			return
		}
		if t.DisableFailover {
			_ = t.Store.AbortPoolLease(lease.LeaseID, "compatibility-profile-unknown")
			writePoolError(writer, http.StatusServiceUnavailable, "Relay Pool compatibility profile requires review")
			return
		}
		lease, err = t.Store.MarkPoolQuotaRejected(lease.LeaseID, result.SourceID, "structured stream quota rejection", 0)
		if err != nil {
			_ = t.Store.AbortPoolLease(lease.LeaseID, "pool-depleted")
			writePoolError(writer, http.StatusTooManyRequests, "Relay Pool has exhausted every usable quota source")
			return
		}
	}
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

func (t *Transport) forwardSSE(writer http.ResponseWriter, response *http.Response, lease state.PoolLease) (state.PoolLease, error) {
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	var buffered bytes.Buffer
	for {
		event, err := readSSEEvent(reader)
		if len(event) > 0 {
			if buffered.Len()+len(event) > maxBufferedSSEBytes {
				return lease, errors.New("initial response stream exceeds Relay buffer")
			}
			buffered.Write(event)
			category, visible, sideEffect := classifySSEEvent(event)
			if category == "quota" {
				return lease, errEarlyQuotaRejection
			}
			if visible || sideEffect {
				lease, _ = t.Store.MarkPoolLeaseProgress(lease.LeaseID, state.PoolLeaseStreaming, visible, sideEffect)
				copyResponseHeaders(writer.Header(), response.Header)
				writer.WriteHeader(response.StatusCode)
				_, _ = writer.Write(buffered.Bytes())
				if flusher, ok := writer.(http.Flusher); ok {
					flusher.Flush()
				}
				for {
					remaining, remainingErr := readSSEEvent(reader)
					if len(remaining) > 0 {
						category, moreVisible, moreSideEffect := classifySSEEvent(remaining)
						if category == "quota" {
							lease, _ = t.Store.MarkPoolQuotaRejected(lease.LeaseID, lease.SourceID, "quota rejection after visible output", 0)
							_, _ = writer.Write(sanitizedRecoverySSE())
							if flusher, ok := writer.(http.Flusher); ok {
								flusher.Flush()
							}
							return lease, errLateQuotaRejection
						}
						_, _ = writer.Write(remaining)
						if flusher, ok := writer.(http.Flusher); ok {
							flusher.Flush()
						}
						if moreVisible || moreSideEffect {
							lease, _ = t.Store.MarkPoolLeaseProgress(lease.LeaseID, state.PoolLeaseStreaming, moreVisible, moreSideEffect)
						}
					}
					if remainingErr != nil {
						if errors.Is(remainingErr, io.EOF) {
							return lease, nil
						}
						_, _ = t.Store.MarkPoolLeaseProgress(lease.LeaseID, state.PoolLeaseRecoveryRequired, false, false)
						return lease, errCommittedStreamFailure
					}
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				copyResponseHeaders(writer.Header(), response.Header)
				writer.WriteHeader(response.StatusCode)
				_, writeErr := writer.Write(buffered.Bytes())
				return lease, writeErr
			}
			return lease, err
		}
	}
}

func sanitizedRecoverySSE() []byte {
	return []byte("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"relay_pool_recovery_required\",\"code\":\"relay_pool_recovery_required\",\"message\":\"Relay Pool stopped safely after output began; retry the next turn to continue without replaying side effects.\"}}\n\n")
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

func classifySSEEvent(event []byte) (category string, visible, sideEffect bool) {
	lower := strings.ToLower(string(event))
	if (strings.Contains(lower, "quota") || strings.Contains(lower, "usage_limit") || strings.Contains(lower, "rate_limit")) &&
		(strings.Contains(lower, "response.failed") || strings.Contains(lower, "error")) {
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
	if status == http.StatusTooManyRequests {
		return true
	}
	if status != http.StatusBadRequest && status != http.StatusForbidden && status != http.StatusPaymentRequired {
		return false
	}
	lower := strings.ToLower(string(body))
	for _, marker := range []string{"usage_limit", "usage limit", "quota", "rate_limit", "rate limit", "insufficient_quota"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
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

func writePoolError(writer http.ResponseWriter, status int, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]any{
		"type": "relay_pool_error", "code": "relay_pool_unavailable", "message": message,
	}})
}
