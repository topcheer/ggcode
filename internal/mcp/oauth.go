package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/debug"
)

// ProtectedResourceMetadata represents RFC 9728 protected resource metadata.
type ProtectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported,omitempty"`
	BearerMethodsSupported []string `json:"bearer_methods_supported,omitempty"`
}

// AuthorizationServerMetadata represents RFC 8414 authorization server metadata.
type AuthorizationServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint,omitempty"`
	ScopesSupported                   []string `json:"scopes_supported,omitempty"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported,omitempty"`
	GrantTypesSupported               []string `json:"grant_types_supported,omitempty"`
	ResponseTypesSupported            []string `json:"response_types_supported,omitempty"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitempty"`
	RevocationEndpoint                string   `json:"revocation_endpoint,omitempty"`
}

// ClientRegistration represents RFC 7591 dynamic client registration response.
type ClientRegistration struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
}

// TokenResponse represents an OAuth 2.1 token endpoint response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
	// Error fields (some providers return 200 with error body)
	Error            string `json:"error,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// oauthState holds discovered OAuth configuration for an MCP server.
type oauthState struct {
	protectedResourceMeta   *ProtectedResourceMetadata
	authorizationServerMeta *AuthorizationServerMetadata
	clientRegistration      *ClientRegistration
	codeVerifier            string
	state                   string
	callbackPort            int
	redirectURI             string
	// Device flow state
	deviceCode     string
	deviceInterval int
}

type oauthCallbackResult struct {
	code string
	err  error
}

// OAuthHandler manages OAuth 2.1 authentication for a single MCP server.
type OAuthHandler struct {
	serverName string
	serverURL  string
	httpClient *http.Client
	store      *auth.Store

	mu          sync.Mutex
	state       *oauthState
	callbackCh  chan oauthCallbackResult
	callbackSrv *http.Server
}

// NewOAuthHandler creates a new OAuth handler for an MCP server.
func NewOAuthHandler(serverName, serverURL string, store *auth.Store) *OAuthHandler {
	return &OAuthHandler{
		serverName: serverName,
		serverURL:  serverURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		store:      store,
	}
}

// providerID returns the storage key for this server's tokens.
func (h *OAuthHandler) providerID() string {
	return "mcp:" + h.serverName
}

// GetAccessToken returns a valid access token, refreshing if needed.
// Returns empty string if no token exists.
// If the token is expired but there is a refresh token, it attempts to refresh.
// If the token is expired and there is no refresh token, it still returns the
// access token optimistically — the server will return 401 if truly expired,
// which triggers the OAuth flow. This avoids premature re-authentication when
// the token has a few minutes left (the 5-minute IsExpired() buffer caused
// tokens to be discarded too early, requiring re-auth every time).
func (h *OAuthHandler) GetAccessToken(ctx context.Context) (string, error) {
	info, err := h.store.Load(h.providerID())
	if err != nil || info == nil {
		return "", nil
	}
	if !info.IsExpired() {
		return info.AccessToken, nil
	}
	// Token is near-expiry or expired. Try refresh if we have a refresh token.
	if info.RefreshToken != "" {
		newInfo, err := h.refreshToken(ctx, info.RefreshToken)
		if err == nil && newInfo != nil {
			return newInfo.AccessToken, nil
		}
		// Refresh failed — fall through to return existing token optimistically.
		debug.Log("mcp-oauth", "refresh_failed server=%s error=%v, using existing token", h.serverName, err)
	}
	// Return the existing access token optimistically. If it's truly expired,
	// the server will return 401 and trigger re-auth.
	if info.AccessToken != "" {
		return info.AccessToken, nil
	}
	return "", nil
}

func (h *OAuthHandler) refreshToken(ctx context.Context, refreshToken string) (*auth.Info, error) {
	h.mu.Lock()
	st := h.state
	h.mu.Unlock()
	if st == nil || st.authorizationServerMeta == nil {
		return nil, fmt.Errorf("no authorization server metadata")
	}
	tokenEndpoint := st.authorizationServerMeta.TokenEndpoint
	if tokenEndpoint == "" {
		return nil, fmt.Errorf("no token endpoint")
	}

	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)
	if st.clientRegistration != nil {
		data.Set("client_id", st.clientRegistration.ClientID)
		if st.clientRegistration.ClientSecret != "" {
			data.Set("client_secret", st.clientRegistration.ClientSecret)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed: %d %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}
	if tokenResp.Error != "" {
		return nil, fmt.Errorf("token refresh error: %s %s", tokenResp.Error, tokenResp.ErrorDescription)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("token refresh returned empty access_token")
	}

	info := &auth.Info{
		ProviderID:   h.providerID(),
		Type:         "oauth",
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    tokenExpiry(tokenResp.ExpiresIn),
	}
	if info.RefreshToken == "" {
		// Preserve existing refresh token if server didn't return a new one
		old, _ := h.store.Load(h.providerID())
		if old != nil && old.RefreshToken != "" {
			info.RefreshToken = old.RefreshToken
		}
	}
	if err := h.store.Save(info); err != nil {
		return nil, err
	}
	return info, nil
}

// Handle401 processes a 401 response. Returns true if OAuth is needed.
func (h *OAuthHandler) Handle401(resp *http.Response) (bool, error) {
	wwwAuth := resp.Header.Get("WWW-Authenticate")
	metadataURL, ok := parseWWWAuthenticate(wwwAuth)
	if !ok {
		// Fallback: try well-known URL
		metadataURL = buildProtectedResourceWellKnown(h.serverURL)
	}
	debug.Log("mcp-oauth", "handle401 server=%s www_auth=%s metadata_url=%s", h.serverName, wwwAuth, metadataURL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := h.discoverProtectedResource(ctx, metadataURL); err != nil {
		debug.Log("mcp-oauth", "discover_protected_resource_failed server=%s error=%v", h.serverName, err)
		return false, fmt.Errorf("discovering protected resource: %w", err)
	}

	h.mu.Lock()
	servers := h.state.protectedResourceMeta.AuthorizationServers
	clientID := ""
	if h.state.clientRegistration != nil {
		clientID = h.state.clientRegistration.ClientID
	}
	h.mu.Unlock()
	debug.Log("mcp-oauth", "auth_servers server=%s servers=%v client_id=%s", h.serverName, servers, clientID)
	if len(servers) == 0 {
		return false, fmt.Errorf("no authorization servers found")
	}

	if err := h.discoverAuthorizationServer(ctx, servers[0]); err != nil {
		debug.Log("mcp-oauth", "discover_auth_server_failed server=%s error=%v", h.serverName, err)
		return false, fmt.Errorf("discovering authorization server: %w", err)
	}

	return true, nil
}

// parseWWWAuthenticate extracts resource_metadata URL from WWW-Authenticate header.
// Format: Bearer resource_metadata="<url>"
func parseWWWAuthenticate(header string) (string, bool) {
	if header == "" {
		return "", false
	}
	// Look for Bearer scheme
	lower := strings.ToLower(header)
	idx := strings.Index(lower, "bearer")
	if idx == -1 {
		return "", false
	}
	rest := header[idx+6:] // skip "bearer"

	// Look for resource_metadata=
	rmIdx := strings.Index(strings.ToLower(rest), "resource_metadata=")
	if rmIdx == -1 {
		return "", false
	}
	val := rest[rmIdx+18:] // skip "resource_metadata="
	val = strings.TrimSpace(val)

	// Extract quoted or unquoted value
	if strings.HasPrefix(val, `"`) {
		val = val[1:]
		end := strings.Index(val, `"`)
		if end == -1 {
			return "", false
		}
		val = val[:end]
	} else {
		// Unquoted: take until space or comma
		if end := strings.IndexAny(val, " ,"); end != -1 {
			val = val[:end]
		}
	}
	if val == "" {
		return "", false
	}
	return val, true
}

func buildProtectedResourceWellKnown(serverURL string) string {
	u, err := url.Parse(serverURL)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s://%s/.well-known/oauth-protected-resource", u.Scheme, u.Host)
}

func (h *OAuthHandler) discoverProtectedResource(ctx context.Context, metadataURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("protected resource metadata: status %d", resp.StatusCode)
	}

	var meta ProtectedResourceMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return fmt.Errorf("parsing protected resource metadata: %w", err)
	}

	h.mu.Lock()
	if h.state == nil {
		h.state = &oauthState{}
	}
	h.state.protectedResourceMeta = &meta
	h.mu.Unlock()
	return nil
}

func (h *OAuthHandler) discoverAuthorizationServer(ctx context.Context, authServerURL string) error {
	// Try RFC 8414 well-known first, then direct URL
	wellKnown := strings.TrimRight(authServerURL, "/") + "/.well-known/oauth-authorization-server"
	if u, err := url.Parse(authServerURL); err == nil {
		wellKnown = fmt.Sprintf("%s://%s/.well-known/oauth-authorization-server%s", u.Scheme, u.Host, u.Path)
	}

	meta, err := h.fetchAuthorizationServerMeta(ctx, wellKnown)
	if err != nil {
		// Fallback: try direct URL
		meta, err = h.fetchAuthorizationServerMeta(ctx, authServerURL)
		if err != nil {
			return err
		}
	}

	h.mu.Lock()
	h.state.authorizationServerMeta = meta
	// Auto-fill built-in client_id for servers with known OAuth apps, even if they advertise
	// a registration endpoint (DCR may fail with 403 for third-party clients).
	if h.state.clientRegistration == nil {
		if clientID, ok := wellKnownClientIDs[meta.Issuer]; ok {
			h.state.clientRegistration = &ClientRegistration{ClientID: clientID, ClientSecret: wellKnownClientSecrets[meta.Issuer]}
		}
	}
	h.mu.Unlock()
	return nil
}

func (h *OAuthHandler) fetchAuthorizationServerMeta(ctx context.Context, url string) (*AuthorizationServerMetadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("authorization server metadata: status %d", resp.StatusCode)
	}

	var meta AuthorizationServerMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, fmt.Errorf("parsing authorization server metadata: %w", err)
	}
	return &meta, nil
}

// SupportsDCR returns true if the authorization server supports dynamic client registration.
func (h *OAuthHandler) SupportsDCR() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state != nil && h.state.authorizationServerMeta != nil &&
		h.state.authorizationServerMeta.RegistrationEndpoint != ""
}

// RegisterClient performs RFC 7591 dynamic client registration.
func (h *OAuthHandler) RegisterClient(ctx context.Context) error {
	h.mu.Lock()
	regEndpoint := h.state.authorizationServerMeta.RegistrationEndpoint
	callbackPort := h.state.callbackPort
	h.mu.Unlock()

	if regEndpoint == "" {
		return fmt.Errorf("no registration endpoint")
	}

	redirectURI := fmt.Sprintf("http://localhost:%d/callback", callbackPort)
	regBody := map[string]interface{}{
		"client_name":                "ggcode-mcp-" + h.serverName,
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none", // public client
	}
	data, err := json.Marshal(regBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, regEndpoint, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("client registration failed: %d %s", resp.StatusCode, string(body))
	}

	var reg ClientRegistration
	if err := json.Unmarshal(body, &reg); err != nil {
		return fmt.Errorf("parsing registration response: %w", err)
	}

	h.mu.Lock()
	h.state.clientRegistration = &reg
	h.mu.Unlock()
	return nil
}

// StartAuthFlow initiates the authorization code + PKCE flow.
// Returns the authorize URL for the user's browser and starts a local callback listener.
func (h *OAuthHandler) StartAuthFlow(ctx context.Context) (string, error) {
	verifier, err := auth.GenerateCodeVerifier()
	if err != nil {
		return "", fmt.Errorf("generating code verifier: %w", err)
	}
	challenge := auth.GenerateCodeChallenge(verifier)

	state, err := auth.GenerateState()
	if err != nil {
		return "", fmt.Errorf("generating state: %w", err)
	}

	// Start local callback server
	port, callbackCh, srv, err := h.startCallbackServer(state)
	if err != nil {
		return "", fmt.Errorf("starting callback server: %w", err)
	}

	h.mu.Lock()
	h.state.codeVerifier = verifier
	h.state.state = state
	h.state.callbackPort = port
	h.state.redirectURI = fmt.Sprintf("http://localhost:%d/callback", port)
	h.callbackCh = callbackCh
	h.callbackSrv = srv
	h.mu.Unlock()

	// Build authorize URL
	h.mu.Lock()
	authEndpoint := h.state.authorizationServerMeta.AuthorizationEndpoint
	clientID := ""
	if h.state.clientRegistration != nil {
		clientID = h.state.clientRegistration.ClientID
	}
	redirectURI := h.state.redirectURI
	scopes := h.state.protectedResourceMeta.ScopesSupported
	h.mu.Unlock()

	if authEndpoint == "" {
		return "", fmt.Errorf("no authorization endpoint")
	}
	if clientID == "" {
		return "", fmt.Errorf("no OAuth client_id: server does not support dynamic client registration and no client_id was configured; set oauth_client_id in your MCP server config")
	}

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("client_id", clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("state", state)
	if len(scopes) > 0 {
		params.Set("scope", strings.Join(scopes, " "))
	}
	if h.serverURL != "" {
		params.Set("resource", h.serverURL)
	}

	return authEndpoint + "?" + params.Encode(), nil
}

func (h *OAuthHandler) startCallbackServer(expectedState string) (int, chan oauthCallbackResult, *http.Server, error) {
	ch := make(chan oauthCallbackResult, 1)
	mux := http.NewServeMux()
	srv := &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		code := q.Get("code")
		state := q.Get("state")
		errParam := q.Get("error")

		if errParam != "" {
			desc := q.Get("error_description")
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "OAuth error: %s %s", errParam, desc)
			ch <- oauthCallbackResult{err: fmt.Errorf("OAuth error: %s %s", errParam, desc)}
			return
		}

		if code == "" || state != expectedState {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "Invalid callback: missing code or state mismatch")
			ch <- oauthCallbackResult{err: fmt.Errorf("invalid callback: missing code or state mismatch")}
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "Authorization successful. You can close this tab.")
		ch <- oauthCallbackResult{code: code}
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, nil, nil, fmt.Errorf("starting callback listener: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	go srv.Serve(ln)
	return port, ch, srv, nil
}

// WaitForCallback waits for the OAuth callback on the local server.
func (h *OAuthHandler) WaitForCallback(ctx context.Context) (string, error) {
	h.mu.Lock()
	ch := h.callbackCh
	h.mu.Unlock()
	if ch == nil {
		return "", fmt.Errorf("no callback channel")
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-ch:
		return result.code, result.err
	}
}

// ExchangeCode exchanges an authorization code for tokens.
func (h *OAuthHandler) ExchangeCode(ctx context.Context, code string) (*TokenResponse, error) {
	h.mu.Lock()
	tokenEndpoint := h.state.authorizationServerMeta.TokenEndpoint
	clientID := ""
	clientSecret := ""
	if h.state.clientRegistration != nil {
		clientID = h.state.clientRegistration.ClientID
		clientSecret = h.state.clientRegistration.ClientSecret
	}
	redirectURI := h.state.redirectURI
	codeVerifier := h.state.codeVerifier
	h.mu.Unlock()

	debug.Log("mcp-oauth", "exchange_code server=%s endpoint=%s client_id=%s redirect_uri=%s", h.serverName, tokenEndpoint, clientID, redirectURI)

	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)
	data.Set("code_verifier", codeVerifier)
	data.Set("client_id", clientID)
	if clientSecret != "" {
		data.Set("client_secret", clientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	debug.Log("mcp-oauth", "exchange_response server=%s status=%d body=%s", h.serverName, resp.StatusCode, string(body))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed: %d %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}
	// Some providers return 200 with error body (e.g., GitHub returns incorrect_client_credentials as 200)
	if tokenResp.Error != "" {
		return nil, fmt.Errorf("token exchange error: %s %s", tokenResp.Error, tokenResp.ErrorDescription)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("token exchange returned empty access_token")
	}
	debug.Log("mcp-oauth", "exchange_parsed server=%s has_access_token=%v expires_in=%d has_refresh=%v", h.serverName, tokenResp.AccessToken != "", tokenResp.ExpiresIn, tokenResp.RefreshToken != "")
	return &tokenResp, nil
}

// SaveToken persists the token response to the auth store.
func (h *OAuthHandler) SaveToken(tokenResp *TokenResponse) error {
	expiresIn := tokenResp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	info := &auth.Info{
		ProviderID:   h.providerID(),
		Type:         "oauth",
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    tokenExpiry(expiresIn),
	}
	return h.store.Save(info)
}

// OAuthRequiredError signals that OAuth authentication is needed.
type OAuthRequiredError struct {
	Handler *OAuthHandler
}

func (e *OAuthRequiredError) Error() string {
	return "oauth authentication required"
}

// ServerName returns the name of the MCP server this handler is for.
func (h *OAuthHandler) ServerName() string {
	return h.serverName
}

// Close cleans up the callback server.
func (h *OAuthHandler) Close() {
	h.mu.Lock()
	srv := h.callbackSrv
	h.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

// SupportsDeviceFlow returns true if we can attempt device flow for this server.
// Checks for a known device code endpoint or a device_authorization_endpoint in metadata.
func (h *OAuthHandler) SupportsDeviceFlow() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state == nil || h.state.authorizationServerMeta == nil {
		return false
	}
	issuer := h.state.authorizationServerMeta.Issuer
	if _, ok := wellKnownDeviceEndpoints[issuer]; ok {
		return true
	}
	// RFC 8628: check device_authorization_endpoint in server metadata
	return false
}

// StartDeviceFlow initiates a device authorization flow (RFC 8628).
// Returns the device flow response containing user_code and verification URI.
func (h *OAuthHandler) StartDeviceFlow(ctx context.Context, scopes []string) (*DeviceFlowResponse, error) {
	h.mu.Lock()
	clientID := ""
	if h.state != nil && h.state.clientRegistration != nil {
		clientID = h.state.clientRegistration.ClientID
	}
	issuer := ""
	if h.state != nil && h.state.authorizationServerMeta != nil {
		issuer = h.state.authorizationServerMeta.Issuer
	}
	h.mu.Unlock()

	if clientID == "" {
		return nil, fmt.Errorf("no client_id for device flow")
	}

	// Find device code endpoint
	deviceEndpoint, ok := wellKnownDeviceEndpoints[issuer]
	if !ok {
		return nil, fmt.Errorf("no device code endpoint for issuer %s", issuer)
	}

	data := url.Values{}
	data.Set("client_id", clientID)
	if len(scopes) > 0 {
		data.Set("scope", strings.Join(scopes, " "))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deviceEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("device flow request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	debug.Log("mcp-oauth", "device_flow_response server=%s status=%d body=%s", h.serverName, resp.StatusCode, string(body))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device flow failed: %d %s", resp.StatusCode, string(body))
	}

	var devResp DeviceFlowResponse
	if err := json.Unmarshal(body, &devResp); err != nil {
		return nil, fmt.Errorf("parsing device flow response: %w", err)
	}
	if devResp.DeviceCode == "" || devResp.UserCode == "" || devResp.VerificationURI == "" {
		return nil, fmt.Errorf("device flow response missing required fields")
	}

	h.mu.Lock()
	if h.state == nil {
		h.state = &oauthState{}
	}
	h.state.deviceCode = devResp.DeviceCode
	h.state.deviceInterval = devResp.Interval
	if h.state.deviceInterval <= 0 {
		h.state.deviceInterval = 5
	}
	h.mu.Unlock()

	return &devResp, nil
}

// PollDeviceToken polls the token endpoint until the user authorizes the device code.
// Should be called after StartDeviceFlow and the user visits the verification URI.
func (h *OAuthHandler) PollDeviceToken(ctx context.Context) (*TokenResponse, error) {
	h.mu.Lock()
	tokenEndpoint := h.state.authorizationServerMeta.TokenEndpoint
	clientID := ""
	if h.state.clientRegistration != nil {
		clientID = h.state.clientRegistration.ClientID
	}
	deviceCode := h.state.deviceCode
	interval := h.state.deviceInterval
	h.mu.Unlock()

	if tokenEndpoint == "" {
		return nil, fmt.Errorf("no token endpoint")
	}
	if deviceCode == "" {
		return nil, fmt.Errorf("no device code; call StartDeviceFlow first")
	}

	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			data := url.Values{}
			data.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
			data.Set("client_id", clientID)
			data.Set("device_code", deviceCode)

			req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(data.Encode()))
			if err != nil {
				return nil, err
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Accept", "application/json")

			resp, err := h.httpClient.Do(req)
			if err != nil {
				return nil, fmt.Errorf("device token poll failed: %w", err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			debug.Log("mcp-oauth", "device_poll server=%s status=%d body=%s", h.serverName, resp.StatusCode, string(body))

			var tokenResp TokenResponse
			if err := json.Unmarshal(body, &tokenResp); err != nil {
				return nil, fmt.Errorf("parsing device token response: %w", err)
			}

			debug.Log("mcp-oauth", "device_token_parsed server=%s has_access=%v has_refresh=%v expires_in=%d error=%s",
				h.serverName, tokenResp.AccessToken != "", tokenResp.RefreshToken != "", tokenResp.ExpiresIn, tokenResp.Error)

			switch tokenResp.Error {
			case "":
				// Success
				if tokenResp.AccessToken == "" {
					return nil, fmt.Errorf("device token returned empty access_token")
				}
				return &tokenResp, nil
			case "authorization_pending":
				// User hasn't authorized yet, keep polling
				continue
			case "slow_down":
				// Increase interval by 5 seconds
				ticker.Reset(time.Duration(interval+5) * time.Second)
				interval += 5
				continue
			case "expired_token":
				return nil, fmt.Errorf("device code expired, please try again")
			default:
				return nil, fmt.Errorf("device token error: %s %s", tokenResp.Error, tokenResp.ErrorDescription)
			}
		}
	}
}

// DeviceFlowResponse holds the response from the device code endpoint.
type DeviceFlowResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// wellKnownClientIDs maps authorization server issuers to their built-in OAuth client IDs.
// Used when the server does not support dynamic client registration (DCR) and the user
// has not configured oauth_client_id in their MCP server config.
var wellKnownClientIDs = map[string]string{
	"https://github.com/login/oauth": "Iv23liJhRuCX7QU1DfgT",
}

// wellKnownClientSecrets maps authorization server issuers to their client secrets.
// Only needed for servers that require client_secret_basic or client_secret_post auth.
var wellKnownClientSecrets = map[string]string{}

// wellKnownDeviceEndpoints maps authorization server issuers to device code endpoints.
// Used when the server metadata doesn't include a device_authorization_endpoint.
var wellKnownDeviceEndpoints = map[string]string{
	"https://github.com/login/oauth": "https://github.com/login/device/code",
}

func tokenExpiry(expiresIn int) time.Time {
	return time.Now().Add(time.Duration(expiresIn) * time.Second)
}

// SetClientCredentials sets pre-configured client credentials for servers without DCR.
func (h *OAuthHandler) SetClientCredentials(clientID, clientSecret string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state == nil {
		h.state = &oauthState{}
	}
	h.state.clientRegistration = &ClientRegistration{
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}
}

// NeedsDiscovery returns true if OAuth metadata hasn't been discovered yet.
func (h *OAuthHandler) NeedsDiscovery() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state == nil || h.state.authorizationServerMeta == nil
}

// GetScopes returns the scopes supported by the protected resource.
func (h *OAuthHandler) GetScopes() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state == nil || h.state.protectedResourceMeta == nil {
		return nil
	}
	return append([]string(nil), h.state.protectedResourceMeta.ScopesSupported...)
}
