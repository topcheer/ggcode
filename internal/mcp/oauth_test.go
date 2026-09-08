package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
)

func TestOAuthHandler_providerID(t *testing.T) {
	h := NewOAuthHandler("test-server", "https://example.com", nil)
	if h.providerID() != "mcp:test-server" {
		t.Errorf("providerID = %q, want %q", h.providerID(), "mcp:test-server")
	}
}

func TestOAuthHandler_canonicalProviderID(t *testing.T) {
	h := NewOAuthHandler("test-server", "https://example.com/mcp", nil)
	h.state = &oauthState{
		protectedResourceMeta: &ProtectedResourceMetadata{
			Resource: StringOrArray{"https://api.example.com/mcp/"},
		},
		authorizationServerMeta: &AuthorizationServerMetadata{
			Issuer: "HTTPS://AUTH.EXAMPLE.COM/",
		},
	}
	if got := h.canonicalProviderID(); got != "mcp-shared:https://auth.example.com|https://api.example.com/mcp" {
		t.Fatalf("canonicalProviderID = %q", got)
	}
}

func TestOAuthHandler_ServerName(t *testing.T) {
	h := NewOAuthHandler("my-server", "https://example.com", nil)
	if h.ServerName() != "my-server" {
		t.Errorf("ServerName = %q", h.ServerName())
	}
}

func TestOAuthHandler_Close(t *testing.T) {
	h := NewOAuthHandler("test", "https://example.com", nil)
	h.Close()
}

func TestParseWWWAuthenticate(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		wantURL string
		wantOK  bool
	}{
		{"with resource_metadata", `Bearer resource_metadata="https://example.com/.well-known/oauth-protected-resource"`, "https://example.com/.well-known/oauth-protected-resource", true},
		{"realm only", `Bearer realm="test"`, "", false},
		{"empty", "", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotURL, gotOK := parseWWWAuthenticate(tc.header)
			if gotURL != tc.wantURL || gotOK != tc.wantOK {
				t.Errorf("parseWWWAuthenticate() = (%q, %v), want (%q, %v)", gotURL, gotOK, tc.wantURL, tc.wantOK)
			}
		})
	}
}

func TestBuildProtectedResourceWellKnown(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://example.com", "https://example.com/.well-known/oauth-protected-resource"},
		{"https://example.com/", "https://example.com/.well-known/oauth-protected-resource"},
		{"https://example.com/api", "https://example.com/.well-known/oauth-protected-resource"},
	}
	for _, tc := range tests {
		got := buildProtectedResourceWellKnown(tc.input)
		if got != tc.want {
			t.Errorf("buildProtectedResourceWellKnown(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestOAuthHandler_discoverProtectedResource(t *testing.T) {
	metadata := ProtectedResourceMetadata{
		Resource:             StringOrArray{"https://example.com"},
		AuthorizationServers: []string{"https://auth.example.com"},
	}
	body, _ := json.Marshal(metadata)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer server.Close()

	h := NewOAuthHandler("test", server.URL, nil)
	err := h.discoverProtectedResource(context.Background(), server.URL+"/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
}

func TestOAuthHandler_fetchAuthorizationServerMeta(t *testing.T) {
	meta := AuthorizationServerMetadata{
		Issuer:                "https://auth.example.com",
		AuthorizationEndpoint: "https://auth.example.com/authorize",
		TokenEndpoint:         "https://auth.example.com/token",
		RegistrationEndpoint:  "https://auth.example.com/register",
		ScopesSupported:       []string{"read", "write"},
	}
	body, _ := json.Marshal(meta)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer server.Close()

	h := NewOAuthHandler("test", "https://example.com", nil)
	got, err := h.fetchAuthorizationServerMeta(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got.Issuer != "https://auth.example.com" {
		t.Errorf("issuer = %q", got.Issuer)
	}
	if len(got.ScopesSupported) != 2 {
		t.Errorf("scopes = %v", got.ScopesSupported)
	}
}

func TestOAuthHandler_FullDiscovery(t *testing.T) {
	// End-to-end: protected resource → auth server discovery
	authMeta := AuthorizationServerMetadata{
		Issuer:                "https://auth.example.com",
		AuthorizationEndpoint: "https://auth.example.com/authorize",
		TokenEndpoint:         "https://auth.example.com/token",
		ScopesSupported:       []string{"read", "write"},
	}
	authBody, _ := json.Marshal(authMeta)

	protectedMeta := ProtectedResourceMetadata{
		Resource:             StringOrArray{"https://example.com"},
		AuthorizationServers: []string{"https://auth.example.com"},
	}
	protectedBody, _ := json.Marshal(protectedMeta)

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(protectedBody)
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server/auth.example.com", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(authBody)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	// Update protected meta to point to our test server
	protectedMeta.AuthorizationServers = []string{server.URL}
	protectedBody, _ = json.Marshal(protectedMeta)

	h := NewOAuthHandler("test", server.URL, nil)
	err := h.discoverProtectedResource(context.Background(), server.URL+"/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatalf("discoverProtectedResource error: %v", err)
	}

	if !h.NeedsDiscovery() {
		// After protected resource discovery, still need auth server discovery
		t.Log("discovery state changed after protected resource meta fetch")
	}
}

func TestOAuthHandler_Handle401_NoHeader(t *testing.T) {
	h := NewOAuthHandler("test", "https://example.com", nil)
	resp := &http.Response{StatusCode: 401, Header: http.Header{}}
	retried, err := h.Handle401(resp)
	if err != nil {
		t.Logf("Handle401 error (expected): %v", err)
	}
	if retried {
		t.Error("should not retry without WWW-Authenticate")
	}
}

func TestTokenExpiry(t *testing.T) {
	before := time.Now()
	expiry := tokenExpiry(3600)
	if expiry.Before(before.Add(3599*time.Second)) || expiry.After(before.Add(3601*time.Second)) {
		t.Errorf("tokenExpiry(3600) = %v, unexpected", expiry)
	}
}

func TestOAuthRequiredError(t *testing.T) {
	h := NewOAuthHandler("test-server", "https://example.com", nil)
	err := &OAuthRequiredError{Handler: h}
	if err.Error() != "oauth authentication required" {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestOAuthHandler_SaveToken(t *testing.T) {
	dir := t.TempDir()
	store := auth.NewStore(dir + "/auth.json")
	h := NewOAuthHandler("test", "https://example.com", store)

	err := h.SaveToken(&TokenResponse{
		AccessToken:  "my-token",
		TokenType:    "Bearer",
		RefreshToken: "my-refresh",
		ExpiresIn:    3600,
	})
	if err != nil {
		t.Fatalf("SaveToken error: %v", err)
	}

	info, err := store.Load(h.providerID())
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if info.AccessToken != "my-token" {
		t.Errorf("token = %q", info.AccessToken)
	}
}

func TestOAuthHandler_GetAccessToken_NoToken(t *testing.T) {
	dir := t.TempDir()
	store := auth.NewStore(dir + "/auth.json")
	h := NewOAuthHandler("test", "https://example.com", store)
	token, err := h.GetAccessToken(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "" {
		t.Errorf("expected empty token, got %q", token)
	}
}

func TestOAuthHandler_GetAccessToken_ValidToken(t *testing.T) {
	dir := t.TempDir()
	store := auth.NewStore(dir + "/auth.json")
	h := NewOAuthHandler("test", "https://example.com", store)
	// Save token through the handler so provider ID is set correctly
	h.SaveToken(&TokenResponse{
		AccessToken: "valid-token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
	})

	token, err := h.GetAccessToken(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if token != "valid-token" {
		t.Errorf("token = %q, want %q", token, "valid-token")
	}
}

func TestOAuthHandler_GetAccessToken_UsesCanonicalSharedTokenAfterDiscovery(t *testing.T) {
	dir := t.TempDir()
	store := auth.NewStore(dir + "/auth.json")
	if err := store.Save(&auth.Info{
		ProviderID:    "mcp-shared:https://auth.example.com|https://api.example.com/mcp",
		Type:          "oauth",
		AccessToken:   "shared-token",
		RefreshToken:  "shared-refresh",
		OAuthIssuer:   "https://auth.example.com",
		OAuthResource: "https://api.example.com/mcp",
		ExpiresAt:     time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	h := NewOAuthHandler("different-server-name", "https://api.example.com/mcp", store)
	h.state = &oauthState{
		protectedResourceMeta: &ProtectedResourceMetadata{
			Resource: StringOrArray{"https://api.example.com/mcp"},
		},
		authorizationServerMeta: &AuthorizationServerMetadata{
			Issuer: "https://auth.example.com",
		},
	}
	token, err := h.GetAccessToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token != "shared-token" {
		t.Fatalf("token = %q, want shared-token", token)
	}
}

func TestAdapter_NewAdapter(t *testing.T) {
	tools := []ToolDefinition{
		{Name: "tool1", Description: "First", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "tool2", Description: "Second", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}
	a := NewAdapter("test-server", nil, tools)
	if a.ServerName() != "test-server" {
		t.Errorf("ServerName = %q", a.ServerName())
	}
	if a.ToolCount() != 2 {
		t.Errorf("ToolCount = %d", a.ToolCount())
	}
	if names := a.ToolNames(); len(names) != 2 {
		t.Errorf("ToolNames = %v", names)
	}
}

func TestExchangeCode_NilStateGuard(t *testing.T) {
	h := NewOAuthHandler("test-server", "https://example.com", nil)
	// h.state is nil — should return error, not panic.
	_, err := h.ExchangeCode(context.Background(), "some-code")
	if err == nil {
		t.Fatal("ExchangeCode should fail with nil state")
	}
}

func TestPollDeviceToken_NilStateGuard(t *testing.T) {
	h := NewOAuthHandler("test-server", "https://example.com", nil)
	// h.state is nil — should return error, not panic.
	_, err := h.PollDeviceToken(context.Background())
	if err == nil {
		t.Fatal("PollDeviceToken should fail with nil state")
	}
}

func TestExchangeCode_EmptyMetadataGuard(t *testing.T) {
	h := NewOAuthHandler("test-server", "https://example.com", nil)
	// State exists but discovery never completed — no token endpoint.
	h.state = &oauthState{}
	_, err := h.ExchangeCode(context.Background(), "some-code")
	if err == nil {
		t.Fatal("ExchangeCode should fail with empty authorizationServerMeta")
	}
}

// Regression for the user-guidance flow: an unauthenticated server that
// declares OAuth via well-known must be detected by the pre-flight probe,
// and an anonymous server must miss (404) without side effects.
func TestProbeWellKnownHitAndMiss(t *testing.T) {
	// HIT: well-known returns one authorization server, and that server's
	// metadata is fetchable.
	var asServed bool
	mux := http.NewServeMux()
	asURL := "" // filled after srv starts; discovery needs ABSOLUTE issuer URLs
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"authorization_servers": []string{asURL},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	asURL = srv.URL + "/as-meta"

	h := NewOAuthHandler("probe-test", srv.URL, auth.DefaultStore())
	// Point the AS metadata fetch at the same test server so the chain
	// completes without an external issuer.
	mux.HandleFunc("/as-meta", func(w http.ResponseWriter, r *http.Request) {
		asServed = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"issuer":                 srv.URL,
			"authorization_endpoint": srv.URL + "/authorize",
			"token_endpoint":         srv.URL + "/token",
		})
	})
	if !h.ProbeWellKnown(context.Background()) {
		t.Fatal("well-known hit must return true and complete discovery")
	}
	if !asServed {
		t.Fatal("authorization-server metadata must have been fetched")
	}

	// MISS: anonymous server (404 on well-known).
	anon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer anon.Close()
	h2 := NewOAuthHandler("anon-test", anon.URL, auth.DefaultStore())
	if h2.ProbeWellKnown(context.Background()) {
		t.Fatal("anonymous server (404) must miss the probe")
	}
}

// waitForClientID must respect an already-cancelled context immediately (the
// unbounded-retry regression once parked the panel for the full caller ctx).
func TestWaitForClientIDRespectsContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	h := NewOAuthHandler("cancel-test", srv.URL, auth.DefaultStore())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := h.waitForClientID(ctx, srv.URL, "client-x", "http://localhost:1/callback"); err == nil {
		t.Fatal("cancelled context must return an error")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("cancelled context must return immediately, took %v", time.Since(start))
	}
}

// v1.3.234 owner report: "DCR 无延迟" must mean ZERO added delay on a
// consistent server. The old loop slept 2s BEFORE the first probe, so every
// OAuth start paid the Railway-style eventual-consistency tax even when the
// client_id was already live. Pin probe-first: first probe immediate, waits
// only between retries.
func TestWaitForClientIDProbesImmediately(t *testing.T) {
	var probes int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&probes, 1)
		w.Header().Set("Location", "https://example.com/login")
		w.WriteHeader(http.StatusFound) // 302 = client_id live
	}))
	defer srv.Close()

	h := NewOAuthHandler("probe-first", srv.URL, auth.DefaultStore())
	start := time.Now()
	if err := h.waitForClientID(context.Background(), srv.URL, "client-live", "http://localhost:1/callback"); err != nil {
		t.Fatalf("live client_id must pass: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 500*time.Millisecond {
		t.Fatalf("first-probe success must return without any sleep, took %v", elapsed)
	}
	if n := atomic.LoadInt32(&probes); n != 1 {
		t.Fatalf("live client_id must succeed on the first probe, got %d probes", n)
	}
}
