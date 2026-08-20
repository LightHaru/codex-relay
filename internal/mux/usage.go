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
	"time"

	"github.com/LightHaru/codex-relay/internal/state"
)

// usageStatusURL is the same authenticated account-status resource used by
// the native Usage settings surface. The Router calls it only from the local
// control process with an isolated account credential; the token is never
// returned to or visible in the renderer.
const usageStatusURL = "https://chatgpt.com/backend-api/wham/usage"

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
