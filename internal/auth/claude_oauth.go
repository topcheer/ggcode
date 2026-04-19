package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	claudeOAuthClientID       = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	claudeOAuthAuthorizeURL   = "https://claude.com/cai/oauth/authorize"
	claudeOAuthTokenURL       = "https://platform.claude.com/v1/oauth/token"
	claudeOAuthAPIKeyURL      = "https://api.anthropic.com/api/oauth/claude_cli/create_api_key"
	claudeOAuthProfileURL     = "https://api.anthropic.com/api/oauth/profile"
	claudeOAuthManualRedirect = "https://platform.claude.com/oauth/code/callback"
	claudeOAuthSuccessURL     = "https://platform.claude.com/oauth/code/success?app=claude-code"
	claudeOAuthScopes         = "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	claudeOAuthCallbackPath   = "/callback"
)

// ClaudeOAuthFlow holds the state for an in-progress OAuth 2.0 + PKCE flow.
type ClaudeOAuthFlow struct {
	AutoURL      string
	ManualURL    string
	CodeVerifier string
	State        string
	Port         int

	callbackCh chan claudeCallbackResult
	server     *http.Server
}

type claudeCallbackResult struct {
	Code        string
	IsAutomatic bool
	Error       error
}

// ClaudeTokenResponse holds the parsed token exchange response.
type ClaudeTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

// ClaudeProfile holds user profile information from the Anthropic API.
type ClaudeProfile struct {
	SubscriptionType string
	DisplayName      string
	RateLimitTier    string
}

// --- PKCE helpers ---

func generateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64urlEncode(b), nil
}

func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64urlEncode(h[:])
}

func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64urlEncode(b), nil
}

func base64urlEncode(data []byte) string {
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(data)
}

// --- Local callback server ---

// startClaudeCallbackListenerNet starts the local HTTP server on a random port.
func startClaudeCallbackListenerNet(expectedState string) (*ClaudeOAuthFlow, error) {
	ch := make(chan claudeCallbackResult, 1)

	mux := http.NewServeMux()
	server := &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	mux.HandleFunc(claudeOAuthCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		code := q.Get("code")
		state := q.Get("state")
		errParam := q.Get("error")

		if errParam != "" {
			desc := q.Get("error_description")
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "OAuth error: %s %s", errParam, desc)
			select {
			case ch <- claudeCallbackResult{Error: fmt.Errorf("OAuth error: %s %s", errParam, desc)}:
			default:
			}
			return
		}

		if code == "" || state != expectedState {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "Invalid callback: missing code or state mismatch")
			select {
			case ch <- claudeCallbackResult{Error: fmt.Errorf("invalid callback: missing code or state mismatch")}:
			default:
			}
			return
		}

		// Redirect browser to success page
		w.Header().Set("Location", claudeOAuthSuccessURL)
		w.WriteHeader(http.StatusFound)
		fmt.Fprint(w, "Authorization successful. You can close this tab.")

		select {
		case ch <- claudeCallbackResult{Code: code, IsAutomatic: true}:
		default:
		}
	})

	// Use net.Listen to get a random port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("starting callback listener: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	go server.Serve(ln)

	flow := &ClaudeOAuthFlow{
		Port:       port,
		callbackCh: ch,
		server:     server,
	}
	return flow, nil
}

// StartClaudeOAuthFlow initiates a new OAuth 2.0 + PKCE flow.
func StartClaudeOAuthFlow(_ context.Context) (*ClaudeOAuthFlow, error) {
	verifier, err := generateCodeVerifier()
	if err != nil {
		return nil, fmt.Errorf("generating code verifier: %w", err)
	}
	challenge := generateCodeChallenge(verifier)

	state, err := generateState()
	if err != nil {
		return nil, fmt.Errorf("generating state: %w", err)
	}

	flow, err := startClaudeCallbackListenerNet(state)
	if err != nil {
		return nil, fmt.Errorf("starting callback listener: %w", err)
	}

	flow.CodeVerifier = verifier
	flow.State = state

	// Build automatic URL (localhost redirect)
	params := url.Values{}
	params.Set("code", "true")
	params.Set("client_id", claudeOAuthClientID)
	params.Set("response_type", "code")
	params.Set("redirect_uri", fmt.Sprintf("http://localhost:%d%s", flow.Port, claudeOAuthCallbackPath))
	params.Set("scope", claudeOAuthScopes)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("state", state)
	flow.AutoURL = claudeOAuthAuthorizeURL + "?" + params.Encode()

	// Build manual URL (platform redirect)
	manualParams := url.Values{}
	manualParams.Set("code", "true")
	manualParams.Set("client_id", claudeOAuthClientID)
	manualParams.Set("response_type", "code")
	manualParams.Set("redirect_uri", claudeOAuthManualRedirect)
	manualParams.Set("scope", claudeOAuthScopes)
	manualParams.Set("code_challenge", challenge)
	manualParams.Set("code_challenge_method", "S256")
	manualParams.Set("state", state)
	flow.ManualURL = claudeOAuthAuthorizeURL + "?" + manualParams.Encode()

	return flow, nil
}

// WaitForClaudeAuthCode waits for the authorization code from the local callback.
func WaitForClaudeAuthCode(ctx context.Context, flow *ClaudeOAuthFlow) (string, bool, error) {
	if flow == nil || flow.callbackCh == nil {
		return "", false, fmt.Errorf("flow is nil or not initialized")
	}
	select {
	case <-ctx.Done():
		flow.Close()
		return "", false, ctx.Err()
	case result := <-flow.callbackCh:
		return result.Code, result.IsAutomatic, result.Error
	}
}

// ExchangeClaudeCodeForTokens exchanges an authorization code for OAuth tokens.
func ExchangeClaudeCodeForTokens(ctx context.Context, code, codeVerifier string, isManual bool, port int) (*ClaudeTokenResponse, error) {
	redirectURI := fmt.Sprintf("http://localhost:%d%s", port, claudeOAuthCallbackPath)
	if isManual {
		redirectURI = claudeOAuthManualRedirect
	}

	body := map[string]interface{}{
		"grant_type":    "authorization_code",
		"code":          code,
		"redirect_uri":  redirectURI,
		"client_id":     claudeOAuthClientID,
		"code_verifier": codeVerifier,
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, claudeOAuthTokenURL, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exchanging code for tokens: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading token response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("token exchange failed [%d]: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var tokenResp ClaudeTokenResponse
	if err := json.Unmarshal(data, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("token response missing access_token")
	}

	return &tokenResp, nil
}

// RefreshClaudeToken refreshes an expired access token using the refresh token.
func RefreshClaudeToken(ctx context.Context, refreshToken string) (*Info, error) {
	body := map[string]interface{}{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     claudeOAuthClientID,
		"scope":         claudeOAuthScopes,
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, claudeOAuthTokenURL, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refreshing token: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading refresh response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("token refresh failed [%d]: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var tokenResp ClaudeTokenResponse
	if err := json.Unmarshal(data, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing refresh response: %w", err)
	}

	expiresIn := tokenResp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}

	info := &Info{
		ProviderID:   ProviderAnthropic,
		Type:         "oauth",
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second),
	}
	// Preserve existing refresh token if not returned
	if info.RefreshToken == "" {
		info.RefreshToken = refreshToken
	}

	return info, nil
}

// CreateClaudeAPIKey creates a long-lived API key from an OAuth access token.
func CreateClaudeAPIKey(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, claudeOAuthAPIKeyURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("creating API key: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading API key response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("API key creation failed [%d]: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("parsing API key response: %w", err)
	}

	rawKey, _ := result["raw_key"].(string)
	// Also check nested data structure
	if rawKey == "" {
		if d, ok := result["data"].(map[string]interface{}); ok {
			rawKey, _ = d["raw_key"].(string)
		}
	}

	return rawKey, nil
}

// FetchClaudeProfile fetches the user's profile information.
func FetchClaudeProfile(ctx context.Context, accessToken string) (*ClaudeProfile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, claudeOAuthProfileURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching profile: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading profile response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("profile fetch failed [%d]: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing profile response: %w", err)
	}

	profile := &ClaudeProfile{}
	if org, ok := raw["organization"].(map[string]interface{}); ok {
		orgType, _ := org["organization_type"].(string)
		switch orgType {
		case "claude_max":
			profile.SubscriptionType = "max"
		case "claude_pro":
			profile.SubscriptionType = "pro"
		case "claude_enterprise":
			profile.SubscriptionType = "enterprise"
		case "claude_team":
			profile.SubscriptionType = "team"
		default:
			profile.SubscriptionType = orgType
		}
	}
	profile.DisplayName, _ = raw["display_name"].(string)
	profile.RateLimitTier, _ = raw["rate_limit_tier"].(string)

	return profile, nil
}

// Close shuts down the callback HTTP server.
func (f *ClaudeOAuthFlow) Close() {
	if f != nil && f.server != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = f.server.Shutdown(shutdownCtx)
	}
}
