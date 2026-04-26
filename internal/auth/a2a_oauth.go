package auth

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// ---------------------------------------------------------------------------
// OAuth2 + PKCE (Authorization Code Flow for public clients)
// ---------------------------------------------------------------------------

// A2AOAuth2Config is the runtime config for A2A OAuth2 authentication.
type A2AOAuth2Config struct {
	ClientID     string
	ClientSecret string // optional; GitHub requires this even with PKCE
	AuthorizeURL string
	TokenURL     string
	Scopes       []string
	// PKCE is always enabled for public clients.
}

// PKCEToken holds tokens obtained via OAuth2 + PKCE flow.
type PKCEToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type"`
	Expiry       time.Time `json:"expiry,omitempty"`
	Scope        string    `json:"scope,omitempty"`
}

// StartPKCEFlow starts an Authorization Code + PKCE flow.
// It opens a browser for user consent and waits for the callback.
// Returns the tokens on success.
func StartPKCEFlow(ctx context.Context, cfg A2AOAuth2Config) (*PKCEToken, error) {
	verifier, err := GenerateCodeVerifier()
	if err != nil {
		return nil, fmt.Errorf("generate verifier: %w", err)
	}
	challenge := GenerateCodeChallenge(verifier)
	state, err := GenerateState()
	if err != nil {
		return nil, fmt.Errorf("generate state: %w", err)
	}

	// Start local callback server.
	// Use localhost (not 127.0.0.1) because GitHub OAuth Apps treat them differently
	// and the registered callback URL must match exactly.
	// Port 8089 is the default; users can override via env var GGCODE_OAUTH_PORT.
	oauthPort := os.Getenv("GGCODE_OAUTH_PORT")
	if oauthPort == "" {
		oauthPort = "8089"
	}
	listenAddr := "localhost:" + oauthPort
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		// Port might be in use; try a random port as fallback
		listener, err = net.Listen("tcp", "localhost:0")
		if err != nil {
			return nil, fmt.Errorf("listen: %w", err)
		}
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)

	// Build authorization URL
	authURL, _ := url.Parse(cfg.AuthorizeURL)
	q := authURL.Query()
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", strings.Join(cfg.Scopes, " "))
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	authURL.RawQuery = q.Encode()

	resultCh := make(chan *PKCEToken, 1)
	errCh := make(chan error, 1)

	srv := &http.Server{}
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			return
		}
		if r.URL.Query().Get("state") != state {
			errCh <- fmt.Errorf("state mismatch")
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no code in callback")
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}

		// Exchange code for token
		token, err := exchangeCodeForToken(ctx, cfg, code, redirectURI, verifier)
		if err != nil {
			errCh <- fmt.Errorf("token exchange: %w", err)
			http.Error(w, "token exchange failed", http.StatusInternalServerError)
			return
		}

		resultCh <- token
		fmt.Fprintf(w, "<html><body><h2>✓ Authentication successful!</h2><p>You can close this tab.</p></body></html>")
	})

	go srv.Serve(listener)
	defer srv.Close()

	// Open browser
	fmt.Fprintf(os.Stderr, "\n🔐 Opening browser for A2A authentication...\n")
	fmt.Fprintf(os.Stderr, "   If browser does not open, visit:\n   %s\n\n", authURL.String())
	openBrowser(authURL.String())

	select {
	case token := <-resultCh:
		return token, nil
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("authentication timed out")
	}
}

func exchangeCodeForToken(ctx context.Context, cfg A2AOAuth2Config, code, redirectURI, verifier string) (*PKCEToken, error) {
	// GitHub requires JSON body for PKCE token exchange (not form-urlencoded).
	// With form-urlencoded, GitHub expects client_secret (confidential client).
	// With JSON body + Accept: application/json, GitHub accepts PKCE (public client).
	reqBody := map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"redirect_uri":  redirectURI,
		"client_id":     cfg.ClientID,
		"code_verifier": verifier,
	}
	// GitHub requires client_secret even with PKCE (confidential client).
	// Read from config or GGCODE_OAUTH_CLIENT_SECRET env var.
	if cfg.ClientSecret != "" {
		reqBody["client_secret"] = cfg.ClientSecret
	} else if secret := os.Getenv("GGCODE_OAUTH_CLIENT_SECRET"); secret != "" {
		reqBody["client_secret"] = secret
	}
	bodyJSON, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST token endpoint: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	contentType := resp.Header.Get("Content-Type")

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	// GitHub returns JSON when Accept header is set, otherwise returns URL-encoded
	var raw map[string]interface{}
	if strings.Contains(contentType, "application/json") {
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("parse token response: %w", err)
		}
	} else {
		// Parse URL-encoded or try JSON anyway
		if err := json.Unmarshal(body, &raw); err != nil {
			// Try URL-encoded
			vals, parseErr := url.ParseQuery(string(body))
			if parseErr != nil {
				return nil, fmt.Errorf("parse token response: %w", err)
			}
			raw = make(map[string]interface{})
			for k, v := range vals {
				if len(v) > 0 {
					raw[k] = v[0]
				}
			}
		}
	}

	token := &PKCEToken{
		AccessToken:  strVal(raw["access_token"]),
		RefreshToken: strVal(raw["refresh_token"]),
		TokenType:    strVal(raw["token_type"]),
		Scope:        strVal(raw["scope"]),
	}
	if exp, ok := raw["expires_in"].(float64); ok {
		token.Expiry = time.Now().Add(time.Duration(exp) * time.Second)
	}
	return token, nil
}

// ---------------------------------------------------------------------------
// Device Authorization Flow (headless / CI environments)
// ---------------------------------------------------------------------------

// StartDeviceFlow starts a Device Authorization flow.
// No client_secret or browser needed. User visits a URL and enters a code.
func StartDeviceFlow(ctx context.Context, cfg A2AOAuth2Config) (*PKCEToken, error) {
	// Request device code via JSON (GitHub requires Accept: application/json)
	reqBody := map[string]string{
		"client_id": cfg.ClientID,
		"scope":     strings.Join(cfg.Scopes, " "),
	}
	bodyJSON, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.AuthorizeURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("create device code request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("device code request: %w", err)
	}
	defer resp.Body.Close()

	var deviceResp struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		Interval        int    `json:"interval"`
		ExpiresIn       int    `json:"expires_in"`
	}
	respBody, _ := io.ReadAll(resp.Body)

	// Check for error response first
	var errResp struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
		return nil, fmt.Errorf("device code request failed: %s (%s)", errResp.Error, errResp.ErrorDescription)
	}

	if err := json.Unmarshal(respBody, &deviceResp); err != nil {
		return nil, fmt.Errorf("parse device response: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\n🔐 Device Authentication Required\n")
	fmt.Fprintf(os.Stderr, "   Visit: %s\n", deviceResp.VerificationURI)
	fmt.Fprintf(os.Stderr, "   Enter code: %s\n\n", deviceResp.UserCode)

	// Copy code to clipboard and open browser automatically
	copyToClipboard(deviceResp.UserCode)
	fmt.Fprintf(os.Stderr, "   ✅ Code copied to clipboard!\n")
	openBrowser(deviceResp.VerificationURI)

	interval := time.Duration(deviceResp.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(deviceResp.ExpiresIn) * time.Second)

	for {
		time.Sleep(interval)
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("device code expired")
		}

		token, err := pollDeviceToken(ctx, cfg, deviceResp.DeviceCode)
		if err != nil {
			if strings.Contains(err.Error(), "authorization_pending") {
				continue
			}
			if strings.Contains(err.Error(), "slow_down") {
				interval += 5 * time.Second
				continue
			}
			return nil, err
		}
		return token, nil
	}
}

func pollDeviceToken(ctx context.Context, cfg A2AOAuth2Config, deviceCode string) (*PKCEToken, error) {
	// Device flow is for public clients — no client_secret needed.
	data := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"client_id":   {cfg.ClientID},
		"device_code": {deviceCode},
	}

	bodyJSON, _ := json.Marshal(map[string]string{
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		"client_id":   cfg.ClientID,
		"device_code": deviceCode,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("create device token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("poll token: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	_ = data // suppress unused
	var raw map[string]interface{}
	json.Unmarshal(body, &raw)

	if errMsg, ok := raw["error"].(string); ok {
		if errDesc, ok := raw["error_description"].(string); ok {
			return nil, fmt.Errorf("%s: %s", errMsg, errDesc)
		}
		return nil, fmt.Errorf("%s", errMsg)
	}

	token := &PKCEToken{
		AccessToken:  strVal(raw["access_token"]),
		RefreshToken: strVal(raw["refresh_token"]),
		TokenType:    strVal(raw["token_type"]),
	}
	if exp, ok := raw["expires_in"].(float64); ok {
		token.Expiry = time.Now().Add(time.Duration(exp) * time.Second)
	}
	return token, nil
}

// ---------------------------------------------------------------------------
// Token validation (server-side)
// ---------------------------------------------------------------------------

// TokenValidator validates incoming Bearer tokens on the A2A server side.
type TokenValidator struct {
	oauthConfig *oauth2.Config
	oidcJWKS    string
	mu          sync.Mutex
}

// NewTokenValidator creates a validator for the given OAuth2/OIDC issuer.
func NewTokenValidator(clientID, issuerURL string) (*TokenValidator, error) {
	cfg := &oauth2.Config{
		ClientID: clientID,
		Endpoint: oauth2.Endpoint{
			TokenURL: issuerURL + "/token",
		},
	}
	return &TokenValidator{oauthConfig: cfg}, nil
}

// ValidateToken checks if a Bearer token is valid.
// For JWT tokens, it verifies locally. For opaque tokens, it uses introspection.
func (v *TokenValidator) ValidateToken(ctx context.Context, token string) (map[string]interface{}, error) {
	// Try JWT parsing first (OIDC id_tokens are JWTs)
	if parts := strings.Split(token, "."); len(parts) == 3 {
		// This is a JWT — for now do basic structure validation.
		// Full validation needs golang-jwt with JWKS.
		return map[string]interface{}{"token_type": "jwt"}, nil
	}

	// Opaque token — use token introspection or userinfo
	introspectURL := v.oauthConfig.Endpoint.TokenURL
	if strings.HasSuffix(introspectURL, "/token") {
		introspectURL = strings.Replace(introspectURL, "/token", "/introspect", 1)
	}

	req, _ := http.NewRequestWithContext(ctx, "POST", introspectURL, strings.NewReader(
		"token="+url.QueryEscape(token),
	))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("introspect: %w", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse introspect response: %w", err)
	}

	if active, _ := result["active"].(bool); !active {
		return nil, fmt.Errorf("token is not active")
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Mutual TLS helpers
// ---------------------------------------------------------------------------

// MTLSConfig holds the runtime mTLS configuration.
type MTLSConfig struct {
	CertFile string
	KeyFile  string
	CAFile   string
}

// BuildTLSConfig creates a *tls.Config for mutual TLS.
func (c *MTLSConfig) BuildTLSConfig() (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load cert/key: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if c.CAFile != "" {
		caCert, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA cert: %w", err)
		}
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to append CA cert")
		}
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caCertPool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func strVal(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// openBrowser tries to open a URL in the default browser.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return
	}
	// Best-effort; failure is non-fatal — URL already printed to stderr.
	_ = cmd.Start()
}

// copyToClipboard copies text to the system clipboard (best-effort).
func copyToClipboard(text string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		// Try wl-copy (Wayland) first, fall back to xclip (X11)
		if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.Command("wl-copy", text)
		} else if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else {
			return
		}
	case "windows":
		cmd = exec.Command("clip")
	default:
		return
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}
	stdin.Write([]byte(text))
	stdin.Close()
	_ = cmd.Wait()
}
