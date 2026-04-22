package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
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
	deviceResp, err := ah.requestDeviceCode()
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
func (ah *AuthHandler) requestDeviceCode() (*DeviceCodeResponse, error) {
	data := url.Values{
		"client_id": {githubDeviceClientID},
		"scope":     {githubDeviceScope},
	}

	resp, err := http.PostForm(githubDeviceCodeURL, data)
	if err != nil {
		return nil, fmt.Errorf("POST device/code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
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
			SessionUpdateType: "auth_required",
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
			token, err := ah.checkToken(deviceResp.DeviceCode)
			if err != nil {
				// "authorization_pending" means user hasn't entered code yet
				if err.Error() == "authorization_pending" {
					continue
				}
				// "slow_down" means increase interval
				if err.Error() == "slow_down" {
					ticker.Reset(time.Duration(interval+5) * time.Second)
					continue
				}
				return "", err
			}
			return token, nil
		}
	}
}

// checkToken checks if the user has completed the device flow.
func (ah *AuthHandler) checkToken(deviceCode string) (string, error) {
	data := url.Values{
		"client_id":   {githubDeviceClientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}

	req, err := http.NewRequest("POST", githubAccessTokenURL, bytes.NewBufferString(data.Encode()))
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
		return "", fmt.Errorf("%s", result.Error)
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
