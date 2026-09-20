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
