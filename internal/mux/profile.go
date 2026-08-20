package mux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LightHaru/codex-relay/internal/state"
)

const (
	profileURL      = "https://chatgpt.com/backend-api/wham/profiles/me"
	profileCacheTTL = 10 * time.Minute
	profileMaxBytes = 1 << 20
)

type profileCacheEntry struct {
	imageURL    string
	displayName string
	username    string
	expiresAt   time.Time
}

type profileIdentity struct {
	ImageURL    string
	DisplayName string
	Username    string
}

type authFile struct {
	Tokens struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

type profileResponse struct {
	Profile struct {
		ProfilePictureURL string `json:"profile_picture_url"`
		DisplayName       string `json:"display_name"`
		Username          string `json:"username"`
	} `json:"profile"`
}

func (m *Multiplexer) profileIdentity(ctx context.Context, account state.Account) profileIdentity {
	now := time.Now()
	m.profileMu.Lock()
	cached, ok := m.profileCache[account.ID]
	m.profileMu.Unlock()
	if ok && now.Before(cached.expiresAt) {
		return profileIdentity{
			ImageURL: cached.imageURL, DisplayName: cached.displayName, Username: cached.username,
		}
	}

	identity, err := fetchProfileIdentity(
		ctx,
		m.profileClient,
		profileURL,
		filepath.Join(account.CodexHome, "auth.json"),
	)
	if err != nil {
		return profileIdentity{}
	}
	m.profileMu.Lock()
	m.profileCache[account.ID] = profileCacheEntry{
		imageURL: identity.ImageURL, displayName: identity.DisplayName,
		username: identity.Username, expiresAt: now.Add(profileCacheTTL),
	}
	m.profileMu.Unlock()
	return identity
}

func (m *Multiplexer) profileImageURL(ctx context.Context, account state.Account) string {
	return m.profileIdentity(ctx, account).ImageURL
}

func fetchProfileImageURL(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	authPath string,
) (string, error) {
	identity, err := fetchProfileIdentity(ctx, client, endpoint, authPath)
	if err != nil {
		return "", err
	}
	return identity.ImageURL, nil
}

func fetchProfileIdentity(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	authPath string,
) (profileIdentity, error) {
	credentials, err := readAuthFile(authPath)
	if err != nil {
		return profileIdentity{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return profileIdentity{}, fmt.Errorf("create profile request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+credentials.Tokens.AccessToken)
	if credentials.Tokens.AccountID != "" {
		request.Header.Set("ChatGPT-Account-ID", credentials.Tokens.AccountID)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Codex Relay")

	response, err := client.Do(request)
	if err != nil {
		return profileIdentity{}, fmt.Errorf("fetch profile: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, profileMaxBytes))
		return profileIdentity{}, fmt.Errorf("fetch profile: status %d", response.StatusCode)
	}
	var profile profileResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, profileMaxBytes))
	if err := decoder.Decode(&profile); err != nil {
		return profileIdentity{}, fmt.Errorf("decode profile: %w", err)
	}
	imageURL, err := validatedProfileImageURL(profile.Profile.ProfilePictureURL)
	if err != nil {
		return profileIdentity{}, err
	}
	return profileIdentity{
		ImageURL:    imageURL,
		DisplayName: normalizedProfileName(profile.Profile.DisplayName),
		Username:    normalizedProfileName(profile.Profile.Username),
	}, nil
}

func normalizedProfileName(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 200 {
		return value[:200]
	}
	return value
}

func readAuthFile(path string) (authFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return authFile{}, fmt.Errorf("open account credentials: %w", err)
	}
	defer file.Close()
	var credentials authFile
	decoder := json.NewDecoder(io.LimitReader(file, profileMaxBytes))
	if err := decoder.Decode(&credentials); err != nil {
		return authFile{}, fmt.Errorf("decode account credentials: %w", err)
	}
	if credentials.Tokens.AccessToken == "" {
		return authFile{}, errors.New("account access token is unavailable")
	}
	return credentials, nil
}

func validatedProfileImageURL(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", errors.New("profile image URL is not HTTPS")
	}
	return parsed.String(), nil
}
