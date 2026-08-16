package mux

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/b-nnett/codex-subscription-router/internal/state"
)

const (
	rateLimitResetCreditsURL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	rateLimitResetMaxBytes   = 2 << 20
)

// ResetCreditsPreview exists only behind the UI-test control route. It lets us
// exercise the real ChatGPT reset sheet without redeeming a real reset credit.
type ResetCreditsPreview struct {
	AccountID      string `json:"accountId"`
	AvailableCount int    `json:"availableCount"`
}

type consumeResetCreditInput struct {
	CreditID        *string `json:"credit_id"`
	RedeemRequestID string  `json:"redeem_request_id"`
}

func (m *Multiplexer) RateLimitResetCredits(ctx context.Context, accountID string) (json.RawMessage, error) {
	account, err := m.resetAccount(accountID)
	if err != nil {
		return nil, err
	}
	if preview, ok := m.resetCreditsPreview(accountID); ok {
		return previewResetCredits(preview), nil
	}
	return fetchRateLimitResetCredits(
		ctx,
		m.profileClient,
		rateLimitResetCreditsURL,
		account,
	)
}

func (m *Multiplexer) ConsumeRateLimitResetCredit(
	ctx context.Context,
	accountID string,
	creditID *string,
	redeemRequestID string,
) (json.RawMessage, error) {
	account, err := m.resetAccount(accountID)
	if err != nil {
		return nil, err
	}
	redeemRequestID = strings.TrimSpace(redeemRequestID)
	if redeemRequestID == "" || len(redeemRequestID) > 200 {
		return nil, errors.New("redeemRequestId is required")
	}
	if creditID != nil && len(*creditID) > 500 {
		return nil, errors.New("creditId is too long")
	}

	if preview, ok := m.resetCreditsPreview(accountID); ok {
		if preview.AvailableCount <= 0 {
			return json.RawMessage(`{"code":"no_credit"}`), nil
		}
		preview.AvailableCount--
		m.resetPreviewMu.Lock()
		m.resetPreviews[accountID] = preview
		m.resetPreviewMu.Unlock()
		credit := "codex-mux-preview-reset"
		if creditID != nil && *creditID != "" {
			credit = *creditID
		}
		payload, _ := json.Marshal(map[string]any{
			"code":   "reset",
			"credit": map[string]any{"id": credit},
		})
		m.publish(Event{Type: "account-updated", AccountID: accountID, Message: "Reset preview redeemed"})
		return payload, nil
	}

	body, err := json.Marshal(consumeResetCreditInput{
		CreditID: creditID, RedeemRequestID: redeemRequestID,
	})
	if err != nil {
		return nil, fmt.Errorf("encode reset redemption: %w", err)
	}
	result, err := requestRateLimitResetCredits(
		ctx,
		m.profileClient,
		rateLimitResetCreditsURL+"/consume",
		http.MethodPost,
		account,
		body,
	)
	if err == nil {
		m.publishAccountRefresh(accountID)
	}
	return result, err
}

func (m *Multiplexer) SetResetCreditsPreview(preview ResetCreditsPreview) error {
	if preview.AccountID == "" {
		return errors.New("accountId is required")
	}
	if _, ok := m.store.Account(preview.AccountID); !ok {
		return errors.New("preview account was not found")
	}
	if preview.AvailableCount < 0 || preview.AvailableCount > 100 {
		return errors.New("availableCount must be between 0 and 100")
	}
	m.resetPreviewMu.Lock()
	// An explicit zero is still a useful deterministic preview: it prevents
	// UI tests from falling through to the live endpoint for that account.
	m.resetPreviews[preview.AccountID] = preview
	m.resetPreviewMu.Unlock()
	m.publish(Event{Type: "account-updated", AccountID: preview.AccountID, Message: "Reset preview changed"})
	return nil
}

func (m *Multiplexer) resetAccount(accountID string) (state.Account, error) {
	account, ok := m.store.Account(accountID)
	if !ok {
		return state.Account{}, fmt.Errorf("account %q not found", accountID)
	}
	if !account.Enabled {
		return state.Account{}, fmt.Errorf("account %q is disabled", accountID)
	}
	return account, nil
}

func (m *Multiplexer) resetCreditsPreview(accountID string) (ResetCreditsPreview, bool) {
	m.resetPreviewMu.RLock()
	defer m.resetPreviewMu.RUnlock()
	preview, ok := m.resetPreviews[accountID]
	return preview, ok
}

func previewResetCredits(preview ResetCreditsPreview) json.RawMessage {
	credits := make([]map[string]any, 0, preview.AvailableCount)
	for index := 0; index < preview.AvailableCount; index++ {
		credits = append(credits, map[string]any{
			"id":         fmt.Sprintf("codex-mux-preview-reset-%d", index+1),
			"status":     "available",
			"title":      "Usage reset",
			"expires_at": time.Now().AddDate(0, 1, 0).UTC().Format(time.RFC3339),
		})
	}
	payload, _ := json.Marshal(map[string]any{
		"available_count":                   preview.AvailableCount,
		"applicable_available_count":        preview.AvailableCount,
		"credits":                           credits,
		"immediate_reset_purchase_eligible": false,
		"total_earned_count":                preview.AvailableCount,
	})
	return payload
}

func fetchRateLimitResetCredits(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	account state.Account,
) (json.RawMessage, error) {
	return requestRateLimitResetCredits(ctx, client, endpoint, http.MethodGet, account, nil)
}

func requestRateLimitResetCredits(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	method string,
	account state.Account,
	body []byte,
) (json.RawMessage, error) {
	credentials, err := readAuthFile(filepath.Join(account.CodexHome, "auth.json"))
	if err != nil {
		return nil, err
	}
	if credentials.Tokens.AccountID == "" {
		return nil, errors.New("account identifier is unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create rate-limit reset request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+credentials.Tokens.AccessToken)
	request.Header.Set("ChatGPT-Account-ID", credentials.Tokens.AccountID)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Codex Subscription Router")
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request rate-limit resets: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, rateLimitResetMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read rate-limit reset response: %w", err)
	}
	if len(data) > rateLimitResetMaxBytes {
		return nil, errors.New("rate-limit reset response is too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("rate-limit reset request returned status %d", response.StatusCode)
	}
	if !json.Valid(data) {
		return nil, errors.New("rate-limit reset response is not valid JSON")
	}
	return json.RawMessage(data), nil
}
