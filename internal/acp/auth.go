package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

// GitHub Device Flow constants
const (
	githubDeviceCodeURL  = "https://github.com/login/device/code"
	githubAccessTokenURL = "https://github.com/login/oauth/access_token"
	githubDeviceClientID = "Iv1.b514d6eb6e6f3a8e" // Placeholder — replace with actual Client ID
	githubDeviceScope    = "read:user"
	authProviderID       = "ggcode-acp"
)

// DeviceCodeResponse represents GitHub's device code response.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// DeviceTokenResponse represents GitHub's token polling response.
type DeviceTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error,omitempty"`
}

// AuthHandler manages ACP authentication.
type AuthHandler struct {
	transport *Transport
	sessionID string
	store     *auth.Store

	// accessTokenURL overrides the token polling endpoint (tests).
	accessTokenURL string
}

// NewAuthHandler creates a new auth handler.
func NewAuthHandler(transport *Transport, sessionID string) *AuthHandler {
	return &AuthHandler{
		transport: transport,
		sessionID: sessionID,
		store:     auth.DefaultStore(),
	}
}

// HandleAgentAuth performs GitHub Device Flow authentication.
// It sends the user_code and verification_uri to the Client via session/update
// notifications, then polls GitHub for the token.
func (ah *AuthHandler) HandleAgentAuth(ctx context.Context) error {
	// Step 1: Request device code
	deviceResp, err := ah.requestDeviceCode(ctx)
	if err != nil {
		return fmt.Errorf("requesting device code: %w", err)
	}

	// Step 2: Send user_code to Client via notification
	ah.sendAuthInstructions(deviceResp)

	// Step 3: Poll for token
	token, err := ah.pollForToken(ctx, deviceResp)
	if err != nil {
		return fmt.Errorf("polling for token: %w", err)
	}

	// Step 4: Save token
	if err := ah.saveToken(token); err != nil {
		return fmt.Errorf("saving token: %w", err)
	}

	return nil
}

// HandleEnvVarAuth validates that required environment variables are set.
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

// requestDeviceCode initiates the GitHub Device Flow.
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

// sendAuthInstructions sends the verification instructions to the Client.
func (ah *AuthHandler) sendAuthInstructions(resp *DeviceCodeResponse) {
	// Send user_code + verification_uri as a session/update notification
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

// pollForToken polls GitHub for the access token.
func (ah *AuthHandler) pollForToken(ctx context.Context, deviceResp *DeviceCodeResponse) (string, error) {
	interval := deviceResp.Interval
	if interval <= 0 {
		interval = 5
	}

	expiry := time.After(time.Duration(deviceResp.ExpiresIn) * time.Second)
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

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
				// RFC 8628 §3.5: each slow_down response requires the polling
				// interval to grow by 5 seconds cumulatively (5→10→15…), not a
				// constant reset back to base+5. (#668)
				interval += 5
				ticker.Reset(time.Duration(interval) * time.Second)
			case devicePollAbort:
				return "", err
			default:
				// authorization_pending, or a transient network/transport error.
				// The user may already have visited the verification URI and
				// entered the user_code — aborting on a proxy blip would waste
				// that. Keep polling until the device code expires. (#668)
				debug.Log("acp", "device flow: token poll error (will retry): %v", err)
			}
		}
	}
}

// checkToken checks if the user has completed the device flow.
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
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST access_token: %w", err)
	}
	defer resp.Body.Close()

	var result DeviceTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding token response: %w", err)
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

	return result.AccessToken, nil
}

// saveToken persists the token to the auth store.
func (ah *AuthHandler) saveToken(token string) error {
	return ah.store.Save(&auth.Info{
		ProviderID:  authProviderID,
		Type:        "github_device_flow",
		AccessToken: token,
	})
}

// Device-flow poll error sentinels (#668).
var (
	errDeviceAuthPending = errors.New("authorization_pending")
	errDeviceSlowDown    = errors.New("slow_down")
)

// terminalDeviceFlowError marks OAuth errors that must abort the whole device
// flow (RFC 8628 §3.5: access_denied, expired_token — plus unrecognized OAuth
// errors). Network/transport failures are NOT terminal: the user may already
// have entered the user_code, so a transient blip must not invalidate it.
type terminalDeviceFlowError struct{ err error }

func (e *terminalDeviceFlowError) Error() string { return e.err.Error() }
func (e *terminalDeviceFlowError) Unwrap() error { return e.err }

type devicePollAction int

const (
	devicePollContinue devicePollAction = iota // keep polling at the current interval
	devicePollSlowDown                         // grow the interval per RFC 8628 §3.5
	devicePollAbort                            // terminal failure, abort the flow
)

// classifyDevicePollError decides how the token poll loop reacts to a
// checkToken error (#668): terminal OAuth errors abort; authorization_pending
// continues at the same interval; slow_down grows the interval (handled by the
// caller via interval += 5, cumulative 5→10→15…); anything else — wrapped
// network/transport/decode errors — is retried until the device code expires
// so one transient failure cannot waste an already-entered user_code.
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
		return devicePollContinue
	}
}
