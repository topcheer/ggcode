package auth

// OpenCode OAuth: device authorization flow against the OpenCode console
// (https://opencode.ai/console), mirroring the official opencode CLI
// (packages/opencode/src/account/account.ts):
//
//	POST /console/auth/device/code   {"client_id":"opencode-cli"}
//	  -> device_code, user_code, verification_uri_complete, expires_in, interval
//	POST /console/auth/device/token   {"grant_type":"urn:ietf:params:oauth:grant-type:device_code",
//	                                   "device_code":..., "client_id":"opencode-cli"}
//	  -> access_token, refresh_token, expires_in   (or RFC 8628 error payloads:
//	     authorization_pending / slow_down / expired_token / access_denied)
//	POST /console/auth/device/token   {"grant_type":"refresh_token",
//	                                   "refresh_token":..., "client_id":"opencode-cli"}
//	  -> refreshed access/refresh token pair
//
// The access token doubles as the OpenCode Zen API bearer credential the same
// way the CLI injects OPENCODE_CONSOLE_TOKEN, so ggcode can offer both this
// OAuth flow and plain API keys (${OPENCODE_API_KEY}) for the same vendor.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// OpenCodeConsoleURL is the default OpenCode console base URL.
const OpenCodeConsoleURL = "https://opencode.ai/console"

// openCodeClientID mirrors the official opencode CLI's client identifier.
// Using the official value keeps ggcode requests indistinguishable from the
// CLI at the authorization server (user requirement: impersonate the client).
const openCodeClientID = "opencode-cli"

const (
	openCodeDeviceGrant  = "urn:ietf:params:oauth:grant-type:device_code"
	openCodeRefreshGrant = "refresh_token"
)

// OpenCodeDeviceAuth is the response from /auth/device/code.
type OpenCodeDeviceAuth struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// VerificationURL resolves the browser URL the user must open. The console
// returns paths relative to the SERVER ROOT (e.g. "/console/device?..."),
// not to the console base - the official CLI resolves them against
// `${server}/` where server is the root (scheme+host). Doing the same here
// avoids double-pathing when consoleURL itself carries "/console".
func (d *OpenCodeDeviceAuth) VerificationURL(consoleURL string) string {
	if strings.HasPrefix(d.VerificationURIComplete, "http://") || strings.HasPrefix(d.VerificationURIComplete, "https://") {
		return d.VerificationURIComplete
	}
	// An EMPTY consoleURL (unset OPENCODE_CONSOLE_URL) must still resolve to
	// the default console host: url.Parse("") yields no Host and the old code
	// returned a RELATIVE path like "/console/device?...", which downstream
	// openers (openSystemURL) reject as non-http(s) - the browser never
	// opened even though the flow itself was running fine.
	if strings.TrimSpace(consoleURL) == "" {
		consoleURL = OpenCodeConsoleURL
	}
	root := consoleURL
	if u, err := url.Parse(consoleURL); err == nil && u.Host != "" {
		u.Path = ""
		root = u.Scheme + "://" + u.Host
	}
	return strings.TrimRight(root, "/") + "/" + strings.TrimLeft(d.VerificationURIComplete, "/")
}

// OpenCodeToken is the successful token response from /auth/device/token.
type OpenCodeToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// ErrOpenCodePending, ErrOpenCodeSlowDown, ErrOpenCodeExpired and
// ErrOpenCodeDenied map the RFC 8628 device-flow error payloads.
var (
	ErrOpenCodePending  = errors.New("authorization pending")
	ErrOpenCodeSlowDown = errors.New("authorization pending (slow down)")
	ErrOpenCodeExpired  = errors.New("device code expired")
	ErrOpenCodeDenied   = errors.New("authorization denied by user")
)

type openCodeTokenError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (e *openCodeTokenError) toErr() error {
	switch e.Error {
	case "authorization_pending":
		return ErrOpenCodePending
	case "slow_down":
		return ErrOpenCodeSlowDown
	case "expired_token":
		return ErrOpenCodeExpired
	case "access_denied":
		return ErrOpenCodeDenied
	}
	if e.ErrorDescription != "" {
		return fmt.Errorf("opencode auth: %s: %s", e.Error, e.ErrorDescription)
	}
	return fmt.Errorf("opencode auth: %s", e.Error)
}

func openCodePost(ctx context.Context, client *http.Client, url string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("opencode auth: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("opencode auth: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// Impersonate the official CLI: opencode/<version> (Account requests in
	// opencode carry the installation UA via the shared HTTP client).
	req.Header.Set("User-Agent", "opencode/1.16.2")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("opencode auth: %s: %w", url, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("opencode auth: read response: %w", err)
	}
	if resp.StatusCode == http.StatusOK {
		// The device token endpoint reports RFC 8628 errors (authorization_
		// pending, slow_down, ...) as HTTP 200 with an {"error":...} body -
		// the CLI decodes a union type for exactly this reason. Probe the
		// error shape first, then the success shape.
		var probe openCodeTokenError
		if json.Unmarshal(data, &probe) == nil && probe.Error != "" {
			return probe.toErr()
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("opencode auth: decode response: %w", err)
		}
		return nil
	}
	var terr openCodeTokenError
	if json.Unmarshal(data, &terr) == nil && terr.Error != "" {
		return terr.toErr()
	}
	return fmt.Errorf("opencode auth: %s: HTTP %d: %s", url, resp.StatusCode, strings.TrimSpace(string(data)))
}

// StartOpenCodeDeviceFlow initiates the device authorization flow.
func StartOpenCodeDeviceFlow(ctx context.Context, consoleURL string) (*OpenCodeDeviceAuth, error) {
	if consoleURL == "" {
		consoleURL = OpenCodeConsoleURL
	}
	client := &http.Client{Timeout: 15 * time.Second}
	var auth OpenCodeDeviceAuth
	if err := openCodePost(ctx, client, strings.TrimRight(consoleURL, "/")+"/auth/device/code",
		map[string]string{"client_id": openCodeClientID}, &auth); err != nil {
		return nil, err
	}
	if auth.DeviceCode == "" || auth.UserCode == "" {
		return nil, fmt.Errorf("opencode auth: incomplete device/code response")
	}
	return &auth, nil
}

// ExchangeOpenCodeDeviceToken polls the token endpoint once. Callers poll
// with the interval from the device auth response; ErrOpenCodePending means
// keep polling.
func ExchangeOpenCodeDeviceToken(ctx context.Context, consoleURL, deviceCode string) (*OpenCodeToken, error) {
	if consoleURL == "" {
		consoleURL = OpenCodeConsoleURL
	}
	client := &http.Client{Timeout: 15 * time.Second}
	var tok OpenCodeToken
	if err := openCodePost(ctx, client, strings.TrimRight(consoleURL, "/")+"/auth/device/token",
		map[string]string{
			"grant_type":  openCodeDeviceGrant,
			"device_code": deviceCode,
			"client_id":   openCodeClientID,
		}, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

// RefreshOpenCodeToken exchanges a refresh token for a fresh token pair.
func RefreshOpenCodeToken(ctx context.Context, consoleURL, refreshToken string) (*OpenCodeToken, error) {
	if consoleURL == "" {
		consoleURL = OpenCodeConsoleURL
	}
	client := &http.Client{Timeout: 15 * time.Second}
	var tok OpenCodeToken
	if err := openCodePost(ctx, client, strings.TrimRight(consoleURL, "/")+"/auth/device/token",
		map[string]string{
			"grant_type":    openCodeRefreshGrant,
			"refresh_token": refreshToken,
			"client_id":     openCodeClientID,
		}, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

// FetchOpenCodeOrgID resolves the account's default org/workspace ID via
// GET /api/orgs (first entry, mirroring the CLI's firstOrgID default).
// Returns "" when the account has no orgs.
func FetchOpenCodeOrgID(ctx context.Context, consoleURL, accessToken string) (string, error) {
	if strings.TrimSpace(accessToken) == "" {
		return "", fmt.Errorf("opencode auth: no access token for org lookup")
	}
	if consoleURL == "" {
		consoleURL = OpenCodeConsoleURL
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}
	orgsURL := strings.TrimRight(consoleURL, "/") + "/api/orgs"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, orgsURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("opencode auth: %s: HTTP %d: %s", orgsURL, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var orgs []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &orgs); err != nil {
		return "", fmt.Errorf("opencode auth: decode orgs: %w", err)
	}
	if len(orgs) == 0 {
		debug.Log("auth", "opencode login: account has no orgs")
		return "", nil
	}
	return strings.TrimSpace(orgs[0].ID), nil
}

// PollOpenCodeDeviceFlow polls the OpenCode console device token endpoint
// until the user authorizes (or the flow expires / the context is cancelled),
// returning a provider store Info ready to persist - the OpenCode equivalent
// of PollCopilotDeviceFlow, used by both `ggcode login opencode` and the TUI
// provider panel.
func PollOpenCodeDeviceFlow(ctx context.Context, consoleURL string, dev *OpenCodeDeviceAuth) (*Info, error) {
	if dev == nil {
		return nil, fmt.Errorf("opencode auth: device flow is nil")
	}
	if consoleURL == "" {
		consoleURL = OpenCodeConsoleURL
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
	}
	tokenURL := strings.TrimRight(consoleURL, "/") + "/auth/device/token"
	interval := time.Duration(dev.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(dev.ExpiresIn) * time.Second)
	if dev.ExpiresIn <= 0 {
		deadline = time.Now().Add(15 * time.Minute)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var tok OpenCodeToken
		if err := openCodePost(ctx, client, tokenURL, map[string]string{
			"grant_type":  openCodeDeviceGrant,
			"device_code": dev.DeviceCode,
			"client_id":   openCodeClientID,
		}, &tok); err == nil {
			info := &Info{
				ProviderID:   ProviderOpenCode,
				Type:         "oauth",
				AccessToken:  tok.AccessToken,
				RefreshToken: tok.RefreshToken,
				UpdatedAt:    time.Now(),
			}
			if tok.ExpiresIn > 0 {
				info.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
			}
			// The console API requires the org/workspace ID on inference
			// requests (x-opencode-org-id); the CLI resolves it right after
			// login and so do we. Best-effort: an empty OrgID degrades to the
			// legacy zen gateway behavior instead of failing the login.
			if orgID, orgErr := FetchOpenCodeOrgID(ctx, consoleURL, tok.AccessToken); orgErr != nil {
				debug.Log("auth", "opencode login: org lookup failed (continuing without org id): %v", orgErr)
			} else {
				info.OrgID = orgID
			}
			return info, nil
		} else if errors.Is(err, ErrOpenCodePending) || errors.Is(err, ErrOpenCodeSlowDown) {
			if time.Now().Add(interval).After(deadline) {
				return nil, ErrOpenCodeExpired
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(interval):
			}
			continue
		} else {
			return nil, err
		}
	}
}
