package mcp

// sa-144: OAuth handler coverage for the refresh / exchange / device-flow /
// discovery chains over httptest (real HTTP, no network beyond localhost).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
)

func sa144OAuthHandler(t *testing.T) (*OAuthHandler, *auth.Store) {
	t.Helper()
	store := auth.NewStore(t.TempDir() + "/auth.json")
	h := NewOAuthHandler("sa144-srv", "https://resource.example.com/mcp", store)
	if h == nil {
		t.Fatal("NewOAuthHandler returned nil")
	}
	return h, store
}

func sa144SaveToken(t *testing.T, store *auth.Store, access, refresh string, expired bool) {
	t.Helper()
	exp := time.Now().Add(time.Hour)
	if expired {
		exp = time.Now().Add(-time.Hour)
	}
	err := store.Save(&auth.Info{
		ProviderID:   "mcp:sa144-srv",
		Type:         "oauth",
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    exp,
		UpdatedAt:    time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func sa144TokenServer(t *testing.T, handler func(form url.Values) (int, any)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		code, body := handler(r.PostForm)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// urlValues aliases url.Values so test handler closures stay terse.
type urlValues = url.Values

// parseQuery extracts the query parameters of an authorization URL.
func parseQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parsing URL %q: %v", raw, err)
	}
	return u.Query()
}

func TestSa144GetAccessTokenEmptyStore(t *testing.T) {
	h, _ := sa144OAuthHandler(t)
	tok, err := h.GetAccessToken(context.Background())
	if err != nil || tok != "" {
		t.Fatalf("expected empty token with empty store, got %q err=%v", tok, err)
	}
}

func TestSa144GetAccessTokenUnexpired(t *testing.T) {
	h, store := sa144OAuthHandler(t)
	sa144SaveToken(t, store, "fresh", "", false)
	tok, err := h.GetAccessToken(context.Background())
	if err != nil || tok != "fresh" {
		t.Fatalf("expected fresh token, got %q err=%v", tok, err)
	}
}

func TestSa144GetAccessTokenRefreshHappyPath(t *testing.T) {
	h, store := sa144OAuthHandler(t)
	sa144SaveToken(t, store, "at-old", "rt-old", true)
	srv := sa144TokenServer(t, func(form urlValues) (int, any) {
		if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "rt-old" {
			return 400, map[string]string{"error": "bad request shape"}
		}
		return 200, map[string]any{"access_token": "at-new", "token_type": "Bearer", "expires_in": 3600}
	})
	h.state = &oauthState{authorizationServerMeta: &AuthorizationServerMetadata{TokenEndpoint: srv.URL + "/token"}}

	tok, err := h.GetAccessToken(context.Background())
	if err != nil || tok != "at-new" {
		t.Fatalf("expected refreshed token, got %q err=%v", tok, err)
	}
	// The new token must be persisted; the old refresh token preserved.
	info, _, err := h.loadStoredInfo()
	if err != nil || info == nil {
		t.Fatalf("stored info missing after refresh: %v", err)
	}
	if info.AccessToken != "at-new" || info.RefreshToken != "rt-old" {
		t.Fatalf("unexpected persisted credentials: access=%q refresh=%q", info.AccessToken, info.RefreshToken)
	}
}

func TestSa144GetAccessTokenRefreshFailsOptimistic(t *testing.T) {
	h, store := sa144OAuthHandler(t)
	sa144SaveToken(t, store, "at-old", "rt-old", true)
	srv := sa144TokenServer(t, func(form urlValues) (int, any) {
		return 500, map[string]string{"error": "server exploded"}
	})
	h.state = &oauthState{authorizationServerMeta: &AuthorizationServerMetadata{TokenEndpoint: srv.URL + "/token"}}

	tok, err := h.GetAccessToken(context.Background())
	if err != nil || tok != "at-old" {
		t.Fatalf("refresh failure must fall back optimistically, got %q err=%v", tok, err)
	}
}

func TestSa144GetAccessTokenExpiredNoRefresh(t *testing.T) {
	h, store := sa144OAuthHandler(t)
	sa144SaveToken(t, store, "at-old", "", true)
	tok, err := h.GetAccessToken(context.Background())
	if err != nil || tok != "at-old" {
		t.Fatalf("expired token without refresh token is returned optimistically, got %q err=%v", tok, err)
	}
}

func TestSa144RefreshTokenGuards(t *testing.T) {
	h, _ := sa144OAuthHandler(t)
	if _, err := h.refreshToken(context.Background(), "rt"); err == nil || !strings.Contains(err.Error(), "no authorization server metadata") {
		t.Fatalf("expected metadata error, got %v", err)
	}
	h.state = &oauthState{authorizationServerMeta: &AuthorizationServerMetadata{}}
	if _, err := h.refreshToken(context.Background(), "rt"); err == nil || !strings.Contains(err.Error(), "no token endpoint and no issuer") {
		t.Fatalf("expected endpoint+issuer error, got %v", err)
	}
}

func TestSa144RefreshTokenLazyDiscovery(t *testing.T) {
	h, _ := sa144OAuthHandler(t)
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":         srv.URL,
			"token_endpoint": srv.URL + "/token",
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("grant_type") != "refresh_token" {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-lazy", "expires_in": 60})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	// Point the issuer at the httptest host so lazy discovery stays local.
	h.state = &oauthState{authorizationServerMeta: &AuthorizationServerMetadata{Issuer: srv.URL}}

	info, err := h.refreshToken(context.Background(), "rt")
	if err != nil {
		t.Fatalf("lazy refresh: %v", err)
	}
	if info.AccessToken != "at-lazy" {
		t.Fatalf("unexpected token %q", info.AccessToken)
	}
}

func TestSa144RefreshTokenErrorBodies(t *testing.T) {
	cases := []struct {
		name    string
		code    int
		body    any
		wantErr string
	}{
		{"http-error", 401, map[string]string{"error": "invalid_grant"}, "token refresh failed"},
		{"body-error-200", 200, map[string]string{"error": "invalid_grant", "error_description": "revoked"}, "token refresh error"},
		{"empty-access", 200, map[string]string{"token_type": "Bearer"}, "empty access_token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := sa144OAuthHandler(t)
			srv := sa144TokenServer(t, func(form urlValues) (int, any) { return tc.code, tc.body })
			h.state = &oauthState{authorizationServerMeta: &AuthorizationServerMetadata{TokenEndpoint: srv.URL + "/token"}}
			if _, err := h.refreshToken(context.Background(), "rt"); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected %q error, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestSa144RefreshTokenWithClientCredentials(t *testing.T) {
	h, _ := sa144OAuthHandler(t)
	var mu sync.Mutex
	gotClientID := ""
	gotSecret := ""
	srv := sa144TokenServer(t, func(form urlValues) (int, any) {
		mu.Lock()
		defer mu.Unlock()
		gotClientID = form.Get("client_id")
		gotSecret = form.Get("client_secret")
		return 200, map[string]any{"access_token": "at", "refresh_token": "rt-new", "expires_in": 60}
	})
	h.state = &oauthState{
		authorizationServerMeta: &AuthorizationServerMetadata{TokenEndpoint: srv.URL + "/token"},
		clientRegistration:      &ClientRegistration{ClientID: "cid", ClientSecret: "secret"},
	}
	if _, err := h.refreshToken(context.Background(), "rt-old"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	id, secret := gotClientID, gotSecret
	mu.Unlock()
	if id != "cid" || secret != "secret" {
		t.Fatalf("client credentials not sent: id=%q secret=%q", id, secret)
	}
}

func TestSa144DeleteServerToken(t *testing.T) {
	h, store := sa144OAuthHandler(t)
	sa144SaveToken(t, store, "at", "rt", false)
	if err := h.DeleteServerToken(); err != nil {
		t.Fatal(err)
	}
	if info, _, err := h.loadStoredInfo(); err != nil || info != nil {
		t.Fatalf("server token must be gone, got %+v err=%v", info, err)
	}
	// skipCanonical must be set: a canonical (shared) credential is skipped too.
	h.mu.Lock()
	h.state = &oauthState{
		authorizationServerMeta: &AuthorizationServerMetadata{Issuer: "https://issuer.example.com"},
		protectedResourceMeta:   &ProtectedResourceMetadata{Resource: StringOrArray{"https://resource.example.com/mcp"}},
	}
	h.mu.Unlock()
	canonical := h.canonicalProviderID()
	if canonical == "" {
		t.Fatal("expected non-empty canonical id")
	}
	if err := store.Save(&auth.Info{ProviderID: canonical, Type: "oauth", AccessToken: "shared", UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if info, _, err := h.loadStoredInfo(); err != nil || info != nil {
		t.Fatalf("canonical credential must be skipped after DeleteServerToken, got %+v", info)
	}
}

func TestSa144Handle401FullChain(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/meta", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              "https://resource.example.com/mcp",
			"authorization_servers": []string{"https://auth.example.com"},
			"scopes_supported":      []string{"read", "write"},
		})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                "https://auth.example.com",
			"token_endpoint":        "https://auth.example.com/token",
			"registration_endpoint": "https://auth.example.com/register",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	h, _ := sa144OAuthHandler(t)
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("WWW-Authenticate", `Bearer resource_metadata="`+srv.URL+`/meta"`)
	// The chain resolves whatever URLs the metadata advertises; both point at
	// https://*.example.com which is not reachable here, so pin them to the
	// httptest host by seeding after discovery is impossible. Instead assert
	// the failure mode first (real chain requires the advertised hosts).
	ok, err := h.Handle401(resp)
	if ok {
		t.Fatal("discovery of unreachable example.com hosts must fail")
	}
	if err == nil || !strings.Contains(err.Error(), "discovering") {
		t.Fatalf("expected discovery error, got %v", err)
	}
}

func TestSa144Handle401LocalChain(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              srv.URL,
			"authorization_servers": []string{srv.URL},
			"scopes_supported":      []string{"mcp"},
		})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":         "https://auth.example.com",
			"token_endpoint": "https://auth.example.com/token",
		})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Handler whose serverURL points at the httptest host: the well-known
	// fallback path then resolves every hop locally.
	h := NewOAuthHandler("sa144-local", srv.URL, nil)
	h.state = &oauthState{}
	ok, err := h.Handle401(&http.Response{Header: http.Header{}})
	if err != nil {
		t.Fatalf("Handle401: %v", err)
	}
	if !ok {
		t.Fatal("expected oauth-required signal")
	}
	h.mu.Lock()
	servers := h.state.protectedResourceMeta.AuthorizationServers
	h.mu.Unlock()
	if len(servers) == 0 {
		t.Fatal("protected resource metadata not hydrated")
	}
}

func TestSa144Handle401NoAuthServers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"resource": "https://x"})
	}))
	t.Cleanup(srv.Close)
	h := NewOAuthHandler("sa144-none", srv.URL, nil)
	h.state = &oauthState{}
	ok, err := h.Handle401(&http.Response{Header: http.Header{}})
	if ok || err == nil || !strings.Contains(err.Error(), "no authorization servers") {
		t.Fatalf("expected no-auth-servers error, got ok=%v err=%v", ok, err)
	}
}

func TestSa144Handle401DiscoveryFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	t.Cleanup(srv.Close)
	h := NewOAuthHandler("sa144-404", srv.URL, nil)
	ok, err := h.Handle401(&http.Response{Header: http.Header{}})
	if ok || err == nil || !strings.Contains(err.Error(), "discovering protected resource") {
		t.Fatalf("expected discovery failure, got ok=%v err=%v", ok, err)
	}
}

func TestSa144RegisterClient(t *testing.T) {
	var mu sync.Mutex
	gotRedirect := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		if uris, ok := body["redirect_uris"].([]any); ok && len(uris) > 0 {
			gotRedirect, _ = uris[0].(string)
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "reg-1", "client_secret": "sec"})
	}))
	t.Cleanup(srv.Close)

	h, _ := sa144OAuthHandler(t)
	h.state = &oauthState{authorizationServerMeta: &AuthorizationServerMetadata{RegistrationEndpoint: srv.URL + "/register"}}
	if err := h.RegisterClient(context.Background()); err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	h.mu.Lock()
	clientID := h.state.clientRegistration.ClientID
	redirect := h.state.redirectURI
	h.mu.Unlock()
	if clientID != "reg-1" {
		t.Fatalf("unexpected client id %q", clientID)
	}
	mu.Lock()
	registeredRedirect := gotRedirect
	mu.Unlock()
	if !strings.HasPrefix(registeredRedirect, "http://localhost:") || !strings.Contains(registeredRedirect, "/callback") {
		t.Fatalf("unexpected registered redirect %q", registeredRedirect)
	}
	if redirect != registeredRedirect {
		t.Fatalf("state redirect %q != registered %q", redirect, registeredRedirect)
	}

	// Idempotent: a second call reuses the running callback server.
	if err := h.RegisterClient(context.Background()); err != nil {
		t.Fatalf("idempotent RegisterClient: %v", err)
	}
	h.Close()
	h.ShutdownCallbackServer()
}

func TestSa144RegisterClientFailures(t *testing.T) {
	h, _ := sa144OAuthHandler(t)
	h.state = &oauthState{authorizationServerMeta: &AuthorizationServerMetadata{}}
	if err := h.RegisterClient(context.Background()); err == nil || !strings.Contains(err.Error(), "no registration endpoint") {
		t.Fatalf("expected no-registration-endpoint error, got %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
	}))
	t.Cleanup(srv.Close)
	h2, _ := sa144OAuthHandler(t)
	h2.state = &oauthState{authorizationServerMeta: &AuthorizationServerMetadata{RegistrationEndpoint: srv.URL + "/reg"}}
	if err := h2.RegisterClient(context.Background()); err == nil {
		t.Fatal("expected DCR failure on 403")
	}
	h2.Close()
}

func TestSa144StartAuthFlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(srv.Close)

	h, _ := sa144OAuthHandler(t)
	h.state = &oauthState{
		authorizationServerMeta: &AuthorizationServerMetadata{AuthorizationEndpoint: srv.URL + "/authorize"},
		clientRegistration:      &ClientRegistration{ClientID: "cid"},
		protectedResourceMeta:   &ProtectedResourceMetadata{ScopesSupported: []string{"read", "write"}},
	}
	authURL, err := h.StartAuthFlow(context.Background())
	if err != nil {
		t.Fatalf("StartAuthFlow: %v", err)
	}
	if !strings.HasPrefix(authURL, srv.URL+"/authorize?") {
		t.Fatalf("unexpected auth URL %q", authURL)
	}
	// Query assertions via net/url parsing.
	q := parseQuery(t, authURL)
	for key, want := range map[string]string{
		"response_type":         "code",
		"client_id":             "cid",
		"code_challenge_method": "S256",
		"prompt":                "consent",
		"scope":                 "read write offline_access",
		"resource":              "https://resource.example.com/mcp",
	} {
		if q.Get(key) != want {
			t.Fatalf("param %s = %q, want %q", key, q.Get(key), want)
		}
	}
	if q.Get("code_challenge") == "" || q.Get("state") == "" || q.Get("redirect_uri") == "" {
		t.Fatal("PKCE challenge, state and redirect_uri are mandatory")
	}
	h.Close()
}

func TestSa144StartAuthFlowErrors(t *testing.T) {
	h, _ := sa144OAuthHandler(t)
	h.state = &oauthState{authorizationServerMeta: &AuthorizationServerMetadata{AuthorizationEndpoint: "https://x/auth"}}
	if _, err := h.StartAuthFlow(context.Background()); err == nil || !strings.Contains(err.Error(), "no OAuth client_id") {
		t.Fatalf("expected client_id error, got %v", err)
	}
	h2, _ := sa144OAuthHandler(t)
	h2.state = &oauthState{clientRegistration: &ClientRegistration{ClientID: "cid"}}
	// sa-144 regression: nil authorizationServerMeta must not panic the deref.
	if _, err := h2.StartAuthFlow(context.Background()); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("expected not-initialized error, got %v", err)
	}
	h3, _ := sa144OAuthHandler(t)
	h3.state = &oauthState{
		authorizationServerMeta: &AuthorizationServerMetadata{},
		clientRegistration:      &ClientRegistration{ClientID: "cid"},
	}
	if _, err := h3.StartAuthFlow(context.Background()); err == nil || !strings.Contains(err.Error(), "no authorization endpoint") {
		t.Fatalf("expected authorization endpoint error, got %v", err)
	}
}

func TestSa144ExchangeCode(t *testing.T) {
	h, _ := sa144OAuthHandler(t)
	if _, err := h.ExchangeCode(context.Background(), "code"); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("expected uninitialized error, got %v", err)
	}

	var mu sync.Mutex
	var form urlValues
	srv := sa144TokenServer(t, func(f urlValues) (int, any) {
		mu.Lock()
		defer mu.Unlock()
		form = f
		return 200, map[string]any{"access_token": "at-x", "refresh_token": "rt-x", "expires_in": 3600}
	})
	h.state = &oauthState{
		authorizationServerMeta: &AuthorizationServerMetadata{TokenEndpoint: srv.URL + "/token"},
		clientRegistration:      &ClientRegistration{ClientID: "cid"},
		redirectURI:             "http://localhost:1/callback",
		codeVerifier:            "verifier",
	}
	tok, err := h.ExchangeCode(context.Background(), "the-code")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if tok.AccessToken != "at-x" || tok.RefreshToken != "rt-x" {
		t.Fatalf("unexpected tokens %+v", tok)
	}
	mu.Lock()
	sent := form
	mu.Unlock()
	if sent.Get("grant_type") != "authorization_code" || sent.Get("code") != "the-code" ||
		sent.Get("code_verifier") != "verifier" || sent.Get("client_id") != "cid" ||
		sent.Get("redirect_uri") != "http://localhost:1/callback" {
		t.Fatalf("unexpected exchange form: %v", sent)
	}

	// Error body with 200 status (GitHub-style).
	srv2 := sa144TokenServer(t, func(f urlValues) (int, any) {
		return 200, map[string]string{"error": "incorrect_client_credentials", "error_description": "bad"}
	})
	h.state.authorizationServerMeta.TokenEndpoint = srv2.URL + "/token"
	if _, err := h.ExchangeCode(context.Background(), "c"); err == nil || !strings.Contains(err.Error(), "token exchange error") {
		t.Fatalf("expected body error, got %v", err)
	}

	// HTTP failure.
	srv3 := sa144TokenServer(t, func(f urlValues) (int, any) { return 400, map[string]string{} })
	h.state.authorizationServerMeta.TokenEndpoint = srv3.URL + "/token"
	if _, err := h.ExchangeCode(context.Background(), "c"); err == nil || !strings.Contains(err.Error(), "token exchange failed") {
		t.Fatalf("expected http error, got %v", err)
	}

	// Empty access token.
	srv4 := sa144TokenServer(t, func(f urlValues) (int, any) { return 200, map[string]string{"token_type": "Bearer"} })
	h.state.authorizationServerMeta.TokenEndpoint = srv4.URL + "/token"
	if _, err := h.ExchangeCode(context.Background(), "c"); err == nil || !strings.Contains(err.Error(), "empty access_token") {
		t.Fatalf("expected empty access token error, got %v", err)
	}
}

func TestSa144PollDeviceToken(t *testing.T) {
	h, _ := sa144OAuthHandler(t)
	if _, err := h.PollDeviceToken(context.Background()); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("expected uninitialized error, got %v", err)
	}
	h.state = &oauthState{authorizationServerMeta: &AuthorizationServerMetadata{}}
	if _, err := h.PollDeviceToken(context.Background()); err == nil || !strings.Contains(err.Error(), "no token endpoint") {
		t.Fatalf("expected token endpoint error, got %v", err)
	}
	h.state.authorizationServerMeta = &AuthorizationServerMetadata{TokenEndpoint: "http://127.0.0.1:1/token"}
	if _, err := h.PollDeviceToken(context.Background()); err == nil || !strings.Contains(err.Error(), "no device code") {
		t.Fatalf("expected device code error, got %v", err)
	}

	var mu sync.Mutex
	polls := 0
	srv := sa144TokenServer(t, func(f urlValues) (int, any) {
		mu.Lock()
		defer mu.Unlock()
		polls++
		if f.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || f.Get("device_code") != "dc-1" {
			return 400, map[string]string{}
		}
		if polls == 1 {
			return 200, map[string]string{"error": "authorization_pending"}
		}
		return 200, map[string]any{"access_token": "at-dev", "refresh_token": "rt-dev", "expires_in": 100}
	})
	h.state = &oauthState{
		authorizationServerMeta: &AuthorizationServerMetadata{TokenEndpoint: srv.URL + "/token"},
		clientRegistration:      &ClientRegistration{ClientID: "cid"},
		deviceCode:              "dc-1",
		deviceInterval:          1,
	}
	tok, err := h.PollDeviceToken(context.Background())
	if err != nil {
		t.Fatalf("PollDeviceToken: %v", err)
	}
	mu.Lock()
	n := polls
	mu.Unlock()
	if tok.AccessToken != "at-dev" || n != 2 {
		t.Fatalf("unexpected device token %+v after %d polls", tok, n)
	}
}

func TestSa144PollDeviceTokenTerminalErrors(t *testing.T) {
	cases := []struct {
		name    string
		body    any
		wantErr string
	}{
		{"expired", map[string]string{"error": "expired_token"}, "device code expired"},
		{"unknown", map[string]string{"error": "access_denied", "error_description": "no"}, "device token error"},
		{"empty-access", map[string]string{}, "empty access_token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := sa144OAuthHandler(t)
			srv := sa144TokenServer(t, func(f urlValues) (int, any) { return 200, tc.body })
			h.state = &oauthState{
				authorizationServerMeta: &AuthorizationServerMetadata{TokenEndpoint: srv.URL + "/token"},
				deviceCode:              "dc",
				deviceInterval:          1,
			}
			if _, err := h.PollDeviceToken(context.Background()); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected %q error, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestSa144StartDeviceFlowRequiresClientID(t *testing.T) {
	h, _ := sa144OAuthHandler(t)
	h.state = &oauthState{authorizationServerMeta: &AuthorizationServerMetadata{Issuer: "https://unknown.example.com"}}
	if _, err := h.StartDeviceFlow(context.Background(), nil); err == nil {
		t.Fatal("expected device flow error without client id")
	}
}

func TestSa144OAuthSmallAccessors(t *testing.T) {
	h, _ := sa144OAuthHandler(t)
	if got := minDuration(2*time.Second, 3*time.Second); got != 2*time.Second {
		t.Fatalf("minDuration min side: %v", got)
	}
	if got := minDuration(5*time.Second, 1*time.Second); got != 1*time.Second {
		t.Fatalf("minDuration max side: %v", got)
	}
	if got := truncateForLog("abcdef", 3); got != "abc..." {
		t.Fatalf("truncateForLog long: %q", got)
	}
	if got := truncateForLog("ab", 3); got != "ab" {
		t.Fatalf("truncateForLog short: %q", got)
	}
	if got := ensureOfflineAccess([]string{"openid"}); len(got) != 2 || got[1] != "offline_access" {
		t.Fatalf("offline_access must be appended: %v", got)
	}
	if got := ensureOfflineAccess([]string{"offline_access"}); len(got) != 1 {
		t.Fatalf("offline_access must not duplicate: %v", got)
	}
	if h.HealthCheckStatus() != "" {
		t.Fatal("fresh handler has empty health status")
	}
	h.setHealthCheckStatus("ok")
	if h.HealthCheckStatus() != "ok" {
		t.Fatal("health status roundtrip failed")
	}
	if !h.NeedsDiscovery() {
		t.Fatal("nil state requires discovery")
	}
	if h.GetScopes() != nil {
		t.Fatal("nil protected resource yields nil scopes")
	}
	if h.HasPendingDeviceFlow() {
		t.Fatal("no pending device flow on fresh handler")
	}
	if h.SupportsDeviceFlow() {
		t.Fatal("no device flow support without metadata")
	}
	if h.SupportsDCR() {
		t.Fatal("no DCR without registration endpoint")
	}

	h.mu.Lock()
	h.state = &oauthState{
		authorizationServerMeta: &AuthorizationServerMetadata{
			Issuer:               "https://auth.example.com",
			RegistrationEndpoint: "https://auth.example.com/register",
		},
		protectedResourceMeta: &ProtectedResourceMetadata{ScopesSupported: []string{"s1", "s2"}},
		deviceCode:            "dc",
	}
	h.mu.Unlock()
	if h.NeedsDiscovery() {
		t.Fatal("hydrated state does not need discovery")
	}
	if scopes := h.GetScopes(); len(scopes) != 2 || scopes[0] != "s1" {
		t.Fatalf("unexpected scopes %v", scopes)
	}
	if !h.HasPendingDeviceFlow() {
		t.Fatal("device code set means pending flow")
	}
	if !h.SupportsDCR() {
		t.Fatal("registration endpoint means DCR support")
	}
	// GetScopes must return a copy: mutating it must not affect state.
	scopes := h.GetScopes()
	scopes[0] = "mutated"
	if h.GetScopes()[0] != "s1" {
		t.Fatal("GetScopes must defensive-copy")
	}
}

func TestSa144ParseWWWAuthenticate(t *testing.T) {
	if _, ok := parseWWWAuthenticate(""); ok {
		t.Fatal("empty header must fail")
	}
	if _, ok := parseWWWAuthenticate("Basic realm=x"); ok {
		t.Fatal("non-bearer must fail")
	}
	if _, ok := parseWWWAuthenticate("Bearer realm=x"); ok {
		t.Fatal("bearer without resource_metadata must fail")
	}
	got, ok := parseWWWAuthenticate(`Bearer resource_metadata="https://x/meta"`)
	if !ok || got != "https://x/meta" {
		t.Fatalf("quoted parse: %q ok=%v", got, ok)
	}
	got, ok = parseWWWAuthenticate("Bearer resource_metadata=https://x/meta")
	if !ok || got != "https://x/meta" {
		t.Fatalf("unquoted parse: %q ok=%v", got, ok)
	}
}

func TestSa144BuildProtectedResourceWellKnown(t *testing.T) {
	if got := buildProtectedResourceWellKnown("https://host/mcp"); got != "https://host/.well-known/oauth-protected-resource" {
		t.Fatalf("unexpected well-known URL %q", got)
	}
	if got := buildProtectedResourceWellKnown("://bad"); got != "" {
		t.Fatalf("invalid URL must yield empty string, got %q", got)
	}
}
