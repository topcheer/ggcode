package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

type StringOrArray []string

func (s *StringOrArray) UnmarshalJSON(data []byte) error {

	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = []string{single}
		return nil
	}

	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	*s = arr
	return nil
}

func (s StringOrArray) First() string {
	if len(s) > 0 {
		return s[0]
	}
	return ""
}

type ProtectedResourceMetadata struct {
	Resource               StringOrArray `json:"resource"`
	AuthorizationServers   []string      `json:"authorization_servers"`
	ScopesSupported        []string      `json:"scopes_supported,omitempty"`
	BearerMethodsSupported []string      `json:"bearer_methods_supported,omitempty"`
}

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

	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint,omitempty"`
}

type ClientRegistration struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`

	Error            string `json:"error,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
}

type oauthState struct {
	protectedResourceMeta   *ProtectedResourceMetadata
	authorizationServerMeta *AuthorizationServerMetadata
	clientRegistration      *ClientRegistration
	codeVerifier            string
	state                   string
	callbackPort            int
	redirectURI             string

	authResource string

	deviceCode     string
	deviceInterval int
}

type oauthCallbackResult struct {
	code string
	err  error
}

type OAuthHandler struct {
	serverName string
	serverURL  string
	httpClient *http.Client
	store      *auth.Store

	mu sync.Mutex

	refreshMu         sync.Mutex
	state             *oauthState
	callbackCh        chan oauthCallbackResult
	callbackSrv       *http.Server
	skipCanonical     bool
	healthCheckStatus string
}

func NewOAuthHandler(serverName, serverURL string, store *auth.Store) *OAuthHandler {
	return &OAuthHandler{
		serverName: serverName,
		serverURL:  serverURL,
		httpClient: newMCPHTTPClient(10 * time.Second),
		store:      store,
	}
}

func (h *OAuthHandler) legacyProviderID() string {
	return "mcp:" + h.serverName
}

func (h *OAuthHandler) providerID() string {
	if canonical := h.canonicalProviderID(); canonical != "" {
		return canonical
	}
	return h.legacyProviderID()
}

func (h *OAuthHandler) canonicalProviderID() string {
	issuer := h.canonicalIssuer()
	resource := h.canonicalResource()
	if issuer == "" || resource == "" {
		return ""
	}
	return "mcp-shared:" + issuer + "|" + resource
}

func (h *OAuthHandler) canonicalIssuer() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state == nil || h.state.authorizationServerMeta == nil {
		return ""
	}
	return normalizeOAuthIdentity(h.state.authorizationServerMeta.Issuer)
}

func (h *OAuthHandler) canonicalResource() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state != nil && h.state.protectedResourceMeta != nil {
		if resource := normalizeOAuthIdentity(h.state.protectedResourceMeta.Resource.First()); resource != "" {
			return resource
		}
	}
	return normalizeOAuthIdentity(h.serverURL)
}

func (h *OAuthHandler) providerIDCandidates() []string {
	out := make([]string, 0, 2)
	out = append(out, h.legacyProviderID())
	if canonical := h.canonicalProviderID(); canonical != "" {
		out = append(out, canonical)
	}
	return out
}

func normalizeOAuthIdentity(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return strings.ToLower(raw)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	u.RawQuery = ""
	if u.Path == "/" {
		u.Path = ""
	}
	return strings.TrimSuffix(u.String(), "/")
}

func (h *OAuthHandler) loadStoredInfo() (*auth.Info, string, error) {
	h.mu.Lock()
	skip := h.skipCanonical
	h.mu.Unlock()
	for _, key := range h.providerIDCandidates() {
		if skip && strings.HasPrefix(key, "mcp-shared:") {
			continue
		}
		info, err := h.store.Load(key)
		if err != nil || info == nil {
			if err != nil {
				return nil, "", err
			}
			continue
		}
		return info, key, nil
	}
	return nil, "", nil
}

func (h *OAuthHandler) hydrateStateFromInfo(info *auth.Info) {
	if info == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state == nil {
		h.state = &oauthState{}
	}
	if h.state.clientRegistration == nil && strings.TrimSpace(info.OAuthClientID) != "" {
		h.state.clientRegistration = &ClientRegistration{
			ClientID:     strings.TrimSpace(info.OAuthClientID),
			ClientSecret: strings.TrimSpace(info.OAuthSecret),
		}
	}
	if h.state.protectedResourceMeta == nil && strings.TrimSpace(info.OAuthResource) != "" {
		h.state.protectedResourceMeta = &ProtectedResourceMetadata{
			Resource: StringOrArray{strings.TrimSpace(info.OAuthResource)},
		}
	}
	if h.state.authorizationServerMeta == nil && strings.TrimSpace(info.OAuthIssuer) != "" {
		h.state.authorizationServerMeta = &AuthorizationServerMetadata{
			Issuer: strings.TrimSpace(info.OAuthIssuer),
		}
	}
}

func (h *OAuthHandler) GetAccessToken(ctx context.Context) (string, error) {
	info, _, err := h.loadStoredInfo()
	if err != nil || info == nil {
		return "", nil
	}
	h.hydrateStateFromInfo(info)
	if !info.IsExpired() {
		return info.AccessToken, nil
	}

	if info.RefreshToken != "" {

		h.refreshMu.Lock()
		defer h.refreshMu.Unlock()
		if latest, _, err := h.loadStoredInfo(); err == nil && latest != nil {
			if !latest.IsExpired() {
				return latest.AccessToken, nil
			}
			info = latest
		}
		newInfo, err := h.refreshToken(ctx, info.RefreshToken)
		if err == nil && newInfo != nil {
			return newInfo.AccessToken, nil
		}

		debug.Log("mcp-oauth", "refresh_failed server=%s error=%v, using existing token", h.serverName, err)
	}

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

		issuer := st.authorizationServerMeta.Issuer
		if issuer == "" {
			return nil, fmt.Errorf("no token endpoint and no issuer for discovery")
		}
		discoverCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := h.discoverAuthorizationServer(discoverCtx, issuer); err != nil {
			return nil, fmt.Errorf("lazy discovery for token refresh: %w", err)
		}
		h.mu.Lock()
		tokenEndpoint = h.state.authorizationServerMeta.TokenEndpoint
		h.mu.Unlock()
		if tokenEndpoint == "" {
			return nil, fmt.Errorf("no token endpoint after discovery")
		}
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
	body, err := util.ReadAll(resp.Body, util.ReadLimitAuth)
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

	info := h.buildAuthInfo(&tokenResp)
	if info.RefreshToken == "" {

		old, _, _ := h.loadStoredInfo()
		if old != nil && old.RefreshToken != "" {
			info.RefreshToken = old.RefreshToken
		}
	}

	info.ProviderID = h.legacyProviderID()
	if err := h.store.Save(info); err != nil {
		return nil, err
	}

	return info, nil
}

func (h *OAuthHandler) DeleteServerToken() error {
	h.mu.Lock()
	h.skipCanonical = true
	h.mu.Unlock()
	return h.store.Delete(h.legacyProviderID())
}

func (h *OAuthHandler) Handle401(resp *http.Response) (bool, error) {
	wwwAuth := resp.Header.Get("WWW-Authenticate")
	metadataURL, ok := parseWWWAuthenticate(wwwAuth)
	if !ok {

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

func (h *OAuthHandler) ProbeWellKnown(ctx context.Context) bool {
	metadataURL := buildProtectedResourceWellKnown(h.serverURL)
	debug.Log("mcp-oauth", "probe well-known server=%s url=%s", h.serverName, metadataURL)
	if err := h.discoverProtectedResource(ctx, metadataURL); err != nil {
		debug.Log("mcp-oauth", "probe well-known miss server=%s error=%v", h.serverName, err)
		return false
	}
	h.mu.Lock()
	servers := h.state.protectedResourceMeta.AuthorizationServers
	h.mu.Unlock()
	if len(servers) == 0 {
		return false
	}
	if err := h.discoverAuthorizationServer(ctx, servers[0]); err != nil {
		debug.Log("mcp-oauth", "probe well-known auth-server discovery failed server=%s error=%v", h.serverName, err)
		return false
	}
	debug.Log("mcp-oauth", "probe well-known hit server=%s - starting OAuth immediately (no 401 roundtrip)", h.serverName)
	return true
}

func parseWWWAuthenticate(header string) (string, bool) {
	if header == "" {
		return "", false
	}

	lower := strings.ToLower(header)
	idx := strings.Index(lower, "bearer")
	if idx == -1 {
		return "", false
	}
	rest := header[idx+6:]

	rmIdx := strings.Index(strings.ToLower(rest), "resource_metadata=")
	if rmIdx == -1 {
		return "", false
	}
	val := rest[rmIdx+18:]
	val = strings.TrimSpace(val)

	if strings.HasPrefix(val, `"`) {
		val = val[1:]
		end := strings.Index(val, `"`)
		if end == -1 {
			return "", false
		}
		val = val[:end]
	} else {

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
	body, err := util.ReadAll(resp.Body, util.ReadLimitAuth)
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

	wellKnown := strings.TrimRight(authServerURL, "/") + "/.well-known/oauth-authorization-server"
	if u, err := url.Parse(authServerURL); err == nil {
		wellKnown = fmt.Sprintf("%s://%s/.well-known/oauth-authorization-server%s", u.Scheme, u.Host, u.Path)
	}

	meta, err := h.fetchAuthorizationServerMeta(ctx, wellKnown)
	if err != nil {

		meta, err = h.fetchAuthorizationServerMeta(ctx, authServerURL)
		if err != nil {
			return err
		}
	}

	h.mu.Lock()
	h.state.authorizationServerMeta = meta

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
	body, err := util.ReadAll(resp.Body, util.ReadLimitAuth)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		bodyPreview := string(body)
		if len(bodyPreview) > 200 {
			bodyPreview = bodyPreview[:200] + "..."
		}
		return nil, fmt.Errorf("authorization server metadata at %s: status %d, body: %s", url, resp.StatusCode, bodyPreview)
	}

	var meta AuthorizationServerMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, fmt.Errorf("parsing authorization server metadata: %w", err)
	}
	return &meta, nil
}

func (h *OAuthHandler) SupportsDCR() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state != nil && h.state.authorizationServerMeta != nil &&
		h.state.authorizationServerMeta.RegistrationEndpoint != ""
}

func (h *OAuthHandler) prepareCallbackServer() error {
	h.mu.Lock()
	alreadyRunning := h.callbackSrv != nil
	h.mu.Unlock()
	if alreadyRunning {
		return nil
	}

	state, err := auth.GenerateState()
	if err != nil {
		return fmt.Errorf("generating state: %w", err)
	}

	port, callbackCh, srv, err := h.startCallbackServer(state)
	if err != nil {
		return fmt.Errorf("starting callback server: %w", err)
	}

	h.mu.Lock()
	if h.state == nil {

		h.state = &oauthState{}
	}
	h.state.state = state
	h.state.callbackPort = port
	h.state.redirectURI = fmt.Sprintf("http://localhost:%d/callback", port)
	h.callbackCh = callbackCh
	h.callbackSrv = srv
	h.mu.Unlock()
	return nil
}

func (h *OAuthHandler) RegisterClient(ctx context.Context) error {

	if err := h.prepareCallbackServer(); err != nil {
		return err
	}

	h.mu.Lock()
	regEndpoint := h.state.authorizationServerMeta.RegistrationEndpoint
	redirectURI := h.state.redirectURI
	h.mu.Unlock()

	if regEndpoint == "" {
		return fmt.Errorf("no registration endpoint")
	}
	regBody := map[string]interface{}{
		"client_name":                "ggcode-mcp-" + h.serverName,
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
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
	body, err := util.ReadAll(resp.Body, util.ReadLimitAuth)
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
	authEndpoint := h.state.authorizationServerMeta.AuthorizationEndpoint
	h.mu.Unlock()

	if reg.ClientID != "" && authEndpoint != "" {
		if err := h.waitForClientID(ctx, authEndpoint, reg.ClientID, redirectURI); err != nil {
			debug.Log("mcp-oauth", "client_id health check failed server=%s client_id=%s: %v (continuing anyway)", h.serverName, reg.ClientID, err)
		}
	}
	return nil
}

func (h *OAuthHandler) waitForClientID(ctx context.Context, authEndpoint, clientID, redirectURI string) error {

	probeVerifier, err := auth.GenerateCodeVerifier()
	if err != nil {
		return nil
	}
	probeChallenge := auth.GenerateCodeChallenge(probeVerifier)

	probeURL, err := url.Parse(authEndpoint)
	if err != nil {
		return nil
	}
	q := probeURL.Query()
	q.Set("client_id", clientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirectURI)
	q.Set("code_challenge", probeChallenge)
	q.Set("code_challenge_method", "S256")
	probeURL.RawQuery = q.Encode()

	const maxDelay = 10 * time.Second

	const maxAttempts = 6
	delay := 2 * time.Second
	attempt := 0

	for {
		attempt++
		if attempt > maxAttempts {
			h.setHealthCheckStatus("")
			debug.Log("mcp-oauth", "client_id health check gave up after %d attempts, continuing anyway", maxAttempts)
			return nil
		}

		if attempt > 1 {
			select {
			case <-ctx.Done():
				h.setHealthCheckStatus("")
				return ctx.Err()
			case <-time.After(delay):
			}
			delay = minDuration(delay*3/2, maxDelay)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL.String(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "text/html,application/json")

		probeClient := *h.httpClient
		probeClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
		resp, err := probeClient.Do(req)
		if err != nil {
			h.setHealthCheckStatus(fmt.Sprintf("Verifying OAuth client (attempt %d, retrying)...", attempt))
			debug.Log("mcp-oauth", "client_id health check attempt=%d error=%v", attempt, err)
			continue
		}
		body, _ := util.ReadAll(resp.Body, util.ReadLimitAuth)
		resp.Body.Close()

		bodyStr := string(body)
		switch {
		case resp.StatusCode >= 300 && resp.StatusCode < 400:
			h.setHealthCheckStatus("")
			debug.Log("mcp-oauth", "client_id health check OK (302) attempt=%d", attempt)
			return nil
		case resp.StatusCode == 200 && !strings.Contains(bodyStr, "invalid_client") && !strings.Contains(bodyStr, "invalid_request"):
			h.setHealthCheckStatus("")
			debug.Log("mcp-oauth", "client_id health check OK (200) attempt=%d", attempt)
			return nil
		case strings.Contains(bodyStr, "invalid_client"):
			h.setHealthCheckStatus(fmt.Sprintf("Waiting for OAuth client to sync (attempt %d)...", attempt))
			debug.Log("mcp-oauth", "client_id health check invalid_client attempt=%d", attempt)
			continue
		case resp.StatusCode >= 400 && resp.StatusCode < 500:

			h.setHealthCheckStatus(fmt.Sprintf("Waiting for OAuth client to sync (attempt %d)...", attempt))
			debug.Log("mcp-oauth", "client_id health check 4xx status=%d body=%s attempt=%d", resp.StatusCode, truncateForLog(bodyStr, 200), attempt)
			continue
		default:
			h.setHealthCheckStatus("")
			debug.Log("mcp-oauth", "client_id health check UNKNOWN status=%d, assuming OK", resp.StatusCode)
			return nil
		}
	}
}

func (h *OAuthHandler) setHealthCheckStatus(s string) {
	h.mu.Lock()
	h.healthCheckStatus = s
	h.mu.Unlock()
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func (h *OAuthHandler) StartAuthFlow(ctx context.Context) (string, error) {
	verifier, err := auth.GenerateCodeVerifier()
	if err != nil {
		return "", fmt.Errorf("generating code verifier: %w", err)
	}
	challenge := auth.GenerateCodeChallenge(verifier)

	if err := h.prepareCallbackServer(); err != nil {
		return "", err
	}

	h.mu.Lock()
	h.state.codeVerifier = verifier
	state := h.state.state
	if h.state.authorizationServerMeta == nil {

		h.mu.Unlock()
		return "", fmt.Errorf("oauth state not initialized (discovery incomplete)")
	}
	authEndpoint := h.state.authorizationServerMeta.AuthorizationEndpoint
	clientID := ""
	if h.state.clientRegistration != nil {
		clientID = h.state.clientRegistration.ClientID
	}
	redirectURI := h.state.redirectURI

	var scopes []string
	if h.state.protectedResourceMeta != nil {
		scopes = h.state.protectedResourceMeta.ScopesSupported
	}
	h.mu.Unlock()

	if authEndpoint == "" {
		return "", fmt.Errorf("no authorization endpoint")
	}
	if clientID == "" {
		return "", fmt.Errorf("no OAuth client_id: server does not support dynamic client registration and no client_id was configured; set oauth_client_id in your MCP server config")
	}

	scopes = ensureOfflineAccess(scopes)

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

	params.Set("prompt", "consent")

	parsed, err := url.Parse(authEndpoint)
	if err != nil {
		return "", fmt.Errorf("parsing authorization endpoint: %w", err)
	}
	baseQuery := parsed.Query()

	endpointHasResource := baseQuery.Get("resource") != ""
	if h.serverURL != "" && !endpointHasResource && !params.Has("resource") {
		params.Set("resource", h.serverURL)
	}

	for key, values := range baseQuery {
		if !params.Has(key) {
			for _, v := range values {
				params.Add(key, v)
			}
		}
	}
	parsed.RawQuery = params.Encode()

	return parsed.String(), nil
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

			select {
			case ch <- oauthCallbackResult{err: fmt.Errorf("OAuth error: %s %s", errParam, desc)}:
			default:
			}
			return
		}

		if code == "" || state != expectedState {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "Invalid callback: missing code or state mismatch")
			select {
			case ch <- oauthCallbackResult{err: fmt.Errorf("invalid callback: missing code or state mismatch")}:
			default:
			}
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "Authorization successful. You can close this tab.")
		select {
		case ch <- oauthCallbackResult{code: code}:
		default:
		}
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, nil, nil, fmt.Errorf("starting callback listener: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	safego.Go("mcp.oauth.serve", func() { _ = srv.Serve(ln) })
	return port, ch, srv, nil
}

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

func (h *OAuthHandler) ExchangeCode(ctx context.Context, code string) (*TokenResponse, error) {
	h.mu.Lock()
	if h.state == nil || h.state.authorizationServerMeta == nil {
		h.mu.Unlock()
		return nil, fmt.Errorf("oauth state not initialized (discovery incomplete)")
	}
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
	body, err := util.ReadAll(resp.Body, util.ReadLimitAuth)
	if err != nil {
		return nil, err
	}
	debug.Log("mcp-oauth", "exchange_response server=%s status=%d body_len=%d", h.serverName, resp.StatusCode, len(body))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed: %d %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}

	if tokenResp.Error != "" {
		return nil, fmt.Errorf("token exchange error: %s %s", tokenResp.Error, tokenResp.ErrorDescription)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("token exchange returned empty access_token")
	}
	debug.Log("mcp-oauth", "exchange_parsed server=%s has_access_token=%v expires_in=%d has_refresh=%v", h.serverName, tokenResp.AccessToken != "", tokenResp.ExpiresIn, tokenResp.RefreshToken != "")
	return &tokenResp, nil
}

func (h *OAuthHandler) SaveToken(tokenResp *TokenResponse) error {
	info := h.buildAuthInfo(tokenResp)

	serverKey := h.legacyProviderID()
	info.ProviderID = serverKey
	if err := h.store.Save(info); err != nil {
		return err
	}

	h.mu.Lock()
	h.skipCanonical = false
	h.mu.Unlock()

	if canonical := h.canonicalProviderID(); canonical != "" && canonical != serverKey {
		existing, _ := h.store.Load(canonical)
		if existing == nil {
			infoCopy := *info
			infoCopy.ProviderID = canonical
			_ = h.store.Save(&infoCopy)
		}
	}
	return nil
}

func (h *OAuthHandler) buildAuthInfo(tokenResp *TokenResponse) *auth.Info {
	expiresIn := tokenResp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	h.mu.Lock()
	clientID := ""
	clientSecret := ""
	if h.state != nil && h.state.clientRegistration != nil {
		clientID = h.state.clientRegistration.ClientID
		clientSecret = h.state.clientRegistration.ClientSecret
	}
	h.mu.Unlock()
	return &auth.Info{
		ProviderID:    h.providerID(),
		Type:          "oauth",
		AccessToken:   tokenResp.AccessToken,
		RefreshToken:  tokenResp.RefreshToken,
		OAuthIssuer:   h.canonicalIssuer(),
		OAuthResource: h.canonicalResource(),
		OAuthClientID: strings.TrimSpace(clientID),
		OAuthSecret:   strings.TrimSpace(clientSecret),
		ExpiresAt:     tokenExpiry(expiresIn),
	}
}

type OAuthRequiredError struct {
	Handler *OAuthHandler
}

func (e *OAuthRequiredError) Error() string {
	return "oauth authentication required"
}

func (h *OAuthHandler) ServerName() string {
	return h.serverName
}

func (h *OAuthHandler) HealthCheckStatus() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.healthCheckStatus
}

func (h *OAuthHandler) Close() {
	h.mu.Lock()
	srv := h.callbackSrv
	h.callbackSrv = nil
	h.callbackCh = nil
	if h.state != nil {

		h.state.state = ""
	}
	h.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

func (h *OAuthHandler) ShutdownCallbackServer() {
	h.Close()
}

func (h *OAuthHandler) HasPendingDeviceFlow() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state != nil && h.state.deviceCode != ""
}

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

	return h.state.authorizationServerMeta.DeviceAuthorizationEndpoint != ""
}

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

	deviceEndpoint, ok := wellKnownDeviceEndpoints[issuer]
	if !ok && h.state != nil && h.state.authorizationServerMeta != nil {
		deviceEndpoint = h.state.authorizationServerMeta.DeviceAuthorizationEndpoint
		ok = deviceEndpoint != ""
	}
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
	body, err := util.ReadAll(resp.Body, util.ReadLimitAuth)
	if err != nil {
		return nil, err
	}
	debug.Log("mcp-oauth", "device_flow_response server=%s status=%d body_len=%d", h.serverName, resp.StatusCode, len(body))

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

func (h *OAuthHandler) PollDeviceToken(ctx context.Context) (*TokenResponse, error) {
	h.mu.Lock()
	if h.state == nil || h.state.authorizationServerMeta == nil {
		h.mu.Unlock()
		return nil, fmt.Errorf("oauth state not initialized (discovery incomplete)")
	}
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
			body, _ := util.ReadAll(resp.Body, util.ReadLimitAuth)
			resp.Body.Close()

			debug.Log("mcp-oauth", "device_poll server=%s status=%d body_len=%d", h.serverName, resp.StatusCode, len(body))

			var tokenResp TokenResponse
			if err := json.Unmarshal(body, &tokenResp); err != nil {
				return nil, fmt.Errorf("parsing device token response: %w", err)
			}

			debug.Log("mcp-oauth", "device_token_parsed server=%s has_access=%v has_refresh=%v expires_in=%d error=%s",
				h.serverName, tokenResp.AccessToken != "", tokenResp.RefreshToken != "", tokenResp.ExpiresIn, tokenResp.Error)

			switch tokenResp.Error {
			case "":

				if tokenResp.AccessToken == "" {
					return nil, fmt.Errorf("device token returned empty access_token")
				}
				return &tokenResp, nil
			case "authorization_pending":

				continue
			case "slow_down":

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

type DeviceFlowResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

var wellKnownClientIDs = map[string]string{
	"https://github.com/login/oauth": "Iv23liJhRuCX7QU1DfgT",
}

var wellKnownClientSecrets = map[string]string{}

var wellKnownDeviceEndpoints = map[string]string{
	"https://github.com/login/oauth": "https://github.com/login/device/code",
}

func tokenExpiry(expiresIn int) time.Time {
	return time.Now().Add(time.Duration(expiresIn) * time.Second)
}

func ensureOfflineAccess(scopes []string) []string {
	for _, s := range scopes {
		if s == "offline_access" {
			return scopes
		}
	}
	return append(scopes, "offline_access")
}

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

func (h *OAuthHandler) NeedsDiscovery() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state == nil || h.state.authorizationServerMeta == nil
}

func (h *OAuthHandler) GetScopes() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state == nil || h.state.protectedResourceMeta == nil {
		return nil
	}
	return append([]string(nil), h.state.protectedResourceMeta.ScopesSupported...)
}
