package acp

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	githubDeviceCodeURL  = "https://github.com/login/device/code"
	githubAccessTokenURL = "https://github.com/login/oauth/access_token"
	githubDeviceClientID = "Iv1.b514d6eb6e6f3a8e"
	githubDeviceScope    = "read:user"
	authProviderID       = "ggcode-acp"
)

type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type DeviceTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error,omitempty"`
}

type AuthHandler struct {
	transport *Transport
	sessionID string
	store     *auth.Store

	accessTokenURL string
}

func NewAuthHandler(transport *Transport, sessionID string) *AuthHandler {
	return &AuthHandler{
		transport: transport,
		sessionID: sessionID,
		store:     auth.DefaultStore(),
	}
}

func (ah *AuthHandler) HandleAgentAuth(ctx context.Context) error {
	deviceResp, err := ah.requestDeviceCode(ctx)
	if err != nil {
		return fmt.Errorf("requesting device code: %w", err)
	}

	ah.sendAuthInstructions(deviceResp)

	token, err := ah.pollForToken(ctx, deviceResp)
	if err != nil {
		return fmt.Errorf("polling for token: %w", err)
	}

	if err := ah.saveToken(token); err != nil {
		return fmt.Errorf("saving token: %w", err)
	}

	return nil
}

func (ah *AuthHandler) HandleEnvVarAuth(vars []AuthEnvVar) error {
	for _, v := range vars {
		val := os.Getenv(v.Name)
		if val == "" {
			optional := v.Optional != nil && *v.Optional
			if !optional {
				return fmt.Errorf("required environment variable %s is not set", v.Name)
			}
		}
	}
	return nil
}

func (ah *AuthHandler) requestDeviceCode(ctx context.Context) (*DeviceCodeResponse, error) {
	data := url.Values{
		"client_id": {githubDeviceClientID},
		"scope":     {githubDeviceScope},
	}

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", githubDeviceCodeURL, bytes.NewBufferString(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("building device/code request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST device/code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := util.ReadAll(resp.Body, util.ReadLimitAuth)
		return nil, fmt.Errorf("device/code returned %d: %s", resp.StatusCode, string(body))
	}

	var result DeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding device code response: %w", err)
	}

	return &result, nil
}

func (ah *AuthHandler) sendAuthInstructions(resp *DeviceCodeResponse) {
	_ = ah.transport.WriteNotification("session/update", SessionUpdateParams{
		SessionID: ah.sessionID,
		Update: SessionUpdate{
			Type: "auth_required",
			Content: &ContentBlock{
				Type: "text",
				Text: fmt.Sprintf("To authenticate, visit: %s\nEnter code: %s", resp.VerificationURI, resp.UserCode),
			},
		},
	})
}

func (ah *AuthHandler) pollForToken(ctx context.Context, deviceResp *DeviceCodeResponse) (string, error) {
	interval := deviceResp.Interval
	if interval <= 0 {
		interval = 5
	}

	expiresIn := deviceResp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 900
	}

	expiry := time.After(time.Duration(expiresIn) * time.Second)
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	var streak transientStreakTracker
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-expiry:
			return "", fmt.Errorf("device code expired")
		case <-ticker.C:
			token, err := ah.checkToken(ctx, deviceResp.DeviceCode)
			if err == nil {
				return token, nil
			}
			switch classifyDevicePollError(err) {
			case devicePollSlowDown:
				interval = growDevicePollInterval(interval)
				ticker.Reset(time.Duration(interval) * time.Second)
			case devicePollAbort:
				return "", err
			default:
				if streak.record(err) {
					return "", fmt.Errorf("device flow: %d consecutive transient token poll failures, giving up (last error: %w)", maxConsecutiveTransientPollErrors, err)
				}
				debug.Log("acp", "device flow: token poll error (will retry): %v", err)
			}
		}
	}
}

func (ah *AuthHandler) checkToken(ctx context.Context, deviceCode string) (string, error) {
	data := url.Values{
		"client_id":   {githubDeviceClientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}

	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	endpoint := ah.accessTokenURL
	if endpoint == "" {
		endpoint = githubAccessTokenURL
	}
	req, err := http.NewRequestWithContext(reqCtx, "POST", endpoint, bytes.NewBufferString(data.Encode()))
	if err != nil {
		return "", &permanentDeviceFlowError{err: fmt.Errorf("building access_token request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", classifyTransportError(fmt.Errorf("POST access_token: %w", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return "", &transientDeviceFlowError{err: fmt.Errorf("access_token returned HTTP %d", resp.StatusCode)}
		}
		body, _ := util.ReadAll(resp.Body, util.ReadLimitAuth)
		return "", &permanentDeviceFlowError{err: fmt.Errorf("access_token returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
	}

	var result DeviceTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", &permanentDeviceFlowError{err: fmt.Errorf("decoding token response: %w", err)}
	}

	if result.Error != "" {
		switch result.Error {
		case "authorization_pending":
			return "", errDeviceAuthPending
		case "slow_down":
			return "", errDeviceSlowDown
		case "access_denied", "expired_token":
			return "", &terminalDeviceFlowError{err: fmt.Errorf("device flow: %s", result.Error)}
		}
		return "", &terminalDeviceFlowError{err: fmt.Errorf("device flow: unexpected OAuth error: %s", result.Error)}
	}

	if result.AccessToken == "" {
		return "", &permanentDeviceFlowError{err: fmt.Errorf("device flow: token response carried no access_token")}
	}

	return result.AccessToken, nil
}

func (ah *AuthHandler) saveToken(token string) error {
	return ah.store.Save(&auth.Info{
		ProviderID:  authProviderID,
		Type:        "github_device_flow",
		AccessToken: token,
	})
}

var (
	errDeviceAuthPending = errors.New("authorization_pending")
	errDeviceSlowDown    = errors.New("slow_down")
)

type terminalDeviceFlowError struct{ err error }

func (e *terminalDeviceFlowError) Error() string { return e.err.Error() }
func (e *terminalDeviceFlowError) Unwrap() error { return e.err }

type transientDeviceFlowError struct{ err error }

func (e *transientDeviceFlowError) Error() string { return e.err.Error() }
func (e *transientDeviceFlowError) Unwrap() error { return e.err }

type permanentDeviceFlowError struct{ err error }

func (e *permanentDeviceFlowError) Error() string { return e.err.Error() }
func (e *permanentDeviceFlowError) Unwrap() error { return e.err }

func classifyTransportError(err error) error {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && !dnsErr.IsTemporary && !dnsErr.IsTimeout {
		return &permanentDeviceFlowError{err: err}
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return &permanentDeviceFlowError{err: err}
	}
	var unknownCA x509.UnknownAuthorityError
	if errors.As(err, &unknownCA) {
		return &permanentDeviceFlowError{err: err}
	}
	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		return &permanentDeviceFlowError{err: err}
	}
	return &transientDeviceFlowError{err: err}
}

const (
	maxDevicePollIntervalSeconds      = 60
	maxConsecutiveTransientPollErrors = 30
)

func growDevicePollInterval(cur int) int {
	cur += 5
	if cur > maxDevicePollIntervalSeconds {
		cur = maxDevicePollIntervalSeconds
	}
	return cur
}

type transientStreakTracker struct{ n int }

func (t *transientStreakTracker) record(err error) bool {
	if errors.Is(err, errDeviceAuthPending) {
		t.n = 0
		return false
	}
	t.n++
	return t.n >= maxConsecutiveTransientPollErrors
}

type devicePollAction int

const (
	devicePollContinue devicePollAction = iota
	devicePollSlowDown
	devicePollAbort
)

func classifyDevicePollError(err error) devicePollAction {
	switch {
	case errors.Is(err, errDeviceAuthPending):
		return devicePollContinue
	case errors.Is(err, errDeviceSlowDown):
		return devicePollSlowDown
	default:
		var te *terminalDeviceFlowError
		if errors.As(err, &te) {
			return devicePollAbort
		}
		var pe *permanentDeviceFlowError
		if errors.As(err, &pe) {
			return devicePollAbort
		}
		return devicePollContinue
	}
}
