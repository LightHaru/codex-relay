package mux

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/LightHaru/codex-relay/internal/state"
)

// usageStatusURL is the same authenticated account-status resource used by
// the native Usage settings surface. The Router calls it only from the local
// control process with an isolated account credential; the token is never
// returned to or visible in the renderer.
const usageStatusURL = "https://chatgpt.com/backend-api/wham/usage"

// UsageStatusAccount is the account-scoped part of the Usage & billing
// response. Usage is intentionally kept as the native JSON object instead of
// being converted into a guessed aggregate: new billing fields can therefore
// be displayed by the renderer without the Router having to understand every
// future plan or credit type.
type UsageStatusAccount struct {
	AccountID  string          `json:"accountId"`
	Label      string          `json:"label"`
	Controller bool            `json:"controller"`
	Enabled    bool            `json:"enabled"`
	Connected  bool            `json:"connected"`
	Usage      json.RawMessage `json:"usage,omitempty"`
	Error      string          `json:"error,omitempty"`
}

// UsageStatusCollection contains one native Usage payload per enabled
// subscription. The native /v1/usage endpoint remains account-scoped for the
// app's own billing actions; this collection is the explicit all-subscription
// view used by the Relay Usage & billing panel.
type UsageStatusCollection struct {
	Accounts       []UsageStatusAccount `json:"accounts"`
	RequestedCount int                  `json:"requestedCount"`
	AvailableCount int                  `json:"availableCount"`
	FailedCount    int                  `json:"failedCount"`
}

// UsageStatus returns the native Usage payload for the Router controller. If
// that account's isolated credentials have expired or are temporarily
// unavailable, it tries another enabled subscription before reporting an
// error. This keeps the native Settings -> Usage page useful without making
// the renderer depend on the original Store app browser session.
func (m *Multiplexer) UsageStatus(ctx context.Context) (json.RawMessage, error) {
	if m.store == nil {
		return nil, fmt.Errorf("subscription store is unavailable")
	}
	accounts := orderedUsageAccounts(m.store.Accounts())
	if len(accounts) == 0 {
		return nil, fmt.Errorf("no enabled subscription is available for usage")
	}
	endpoint := m.usageEndpoint
	if endpoint == "" {
		endpoint = usageStatusURL
	}
	client := m.profileClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	var failures []string
	for _, account := range accounts {
		status, err := fetchUsageStatus(
			ctx,
			client,
			endpoint,
			filepath.Join(account.CodexHome, "auth.json"),
		)
		if err == nil {
			return status, nil
		}
		// Keep the control response concise and never include a path, token, or
		// raw upstream response. The renderer only needs to know whether it can
		// fall back to its own native request.
		failures = append(failures, account.ID+": "+compactUsageError(err))
	}
	return nil, fmt.Errorf("read subscription usage: %s", strings.Join(failures, "; "))
}

// UsageStatusAll fetches the native Usage payload for every enabled
// subscription independently. A failed account is returned as an entry with a
// bounded error while successful accounts remain usable; this is important for
// a quota dashboard because one expired credential must not blank the other
// accounts. The account order is deterministic with Primary first.
func (m *Multiplexer) UsageStatusAll(ctx context.Context) (UsageStatusCollection, error) {
	if m.store == nil {
		return UsageStatusCollection{}, fmt.Errorf("subscription store is unavailable")
	}
	accounts := orderedUsageAccounts(m.store.Accounts())
	collection := UsageStatusCollection{
		Accounts:       make([]UsageStatusAccount, len(accounts)),
		RequestedCount: len(accounts),
	}
	if len(accounts) == 0 {
		return collection, nil
	}
	endpoint := m.usageEndpoint
	if endpoint == "" {
		endpoint = usageStatusURL
	}
	client := m.profileClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	type usageResult struct {
		index     int
		connected bool
		usage     json.RawMessage
		err       error
	}
	results := make(chan usageResult, len(accounts))
	var wait sync.WaitGroup
	for index, account := range accounts {
		collection.Accounts[index] = UsageStatusAccount{
			AccountID:  account.ID,
			Label:      account.Label,
			Controller: account.Controller,
			Enabled:    account.Enabled,
		}
		wait.Add(1)
		go func(index int, account state.Account) {
			defer wait.Done()
			// A pending sign-in has no auth.json yet. Keep it in the response so
			// the UI can explain why that subscription has no billing data, but do
			// not turn the missing file into a request against the native account.
			credentials, err := readAuthFile(filepath.Join(account.CodexHome, "auth.json"))
			if err != nil || strings.TrimSpace(credentials.Tokens.AccessToken) == "" {
				results <- usageResult{index: index, err: fmt.Errorf("subscription is not connected")}
				return
			}
			status, err := fetchUsageStatus(ctx, client, endpoint, filepath.Join(account.CodexHome, "auth.json"))
			results <- usageResult{index: index, connected: true, usage: status, err: err}
		}(index, account)
	}
	wait.Wait()
	close(results)
	for result := range results {
		entry := &collection.Accounts[result.index]
		entry.Connected = result.connected
		if result.err != nil {
			entry.Error = compactUsageError(result.err)
			collection.FailedCount++
			continue
		}
		entry.Connected = true
		entry.Usage = result.usage
		collection.AvailableCount++
	}
	return collection, nil
}

func orderedUsageAccounts(accounts []state.Account) []state.Account {
	ordered := make([]state.Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Enabled && account.Controller {
			ordered = append(ordered, account)
		}
	}
	for _, account := range accounts {
		if account.Enabled && !account.Controller {
			ordered = append(ordered, account)
		}
	}
	return ordered
}

func fetchUsageStatus(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	authPath string,
) (json.RawMessage, error) {
	credentials, err := readAuthFile(authPath)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create usage request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+credentials.Tokens.AccessToken)
	if credentials.Tokens.AccountID != "" {
		request.Header.Set("ChatGPT-Account-ID", credentials.Tokens.AccountID)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Codex Relay")

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch usage: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, profileMaxBytes))
		return nil, fmt.Errorf("fetch usage: status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, profileMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read usage: %w", err)
	}
	if len(body) > profileMaxBytes {
		return nil, fmt.Errorf("read usage: response exceeds %d bytes", profileMaxBytes)
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' || !json.Valid(body) {
		return nil, fmt.Errorf("decode usage: expected a JSON object")
	}
	return append(json.RawMessage(nil), body...), nil
}

func compactUsageError(err error) string {
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "unavailable"
	}
	// Error text is returned only to the local renderer. Still keep it bounded
	// so an unusual network library error cannot turn into a large response.
	if len(message) > 160 {
		return message[:160]
	}
	return message
}
