package auth

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

const OpenCodeConsoleURL = "https://opencode.ai/console"

const openCodeClientID = "opencode-cli"

const (
	openCodeDeviceGrant  = "urn:ietf:params:oauth:grant-type:device_code"
	openCodeRefreshGrant = "refresh_token"
)

type OpenCodeDeviceAuth struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

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

type OpenCodeToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

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
