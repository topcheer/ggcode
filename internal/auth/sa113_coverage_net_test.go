package auth

// sa-113 coverage net, part 2: functions that the (now untagged)
// auth_coverage_ext_test.go could not reach.
//
// Everything here exercises real behavior: HTTP goes through the actual
// transport (hardcoded-URL functions are routed via an http.DefaultClient
// transport swap, never by mocking internals), token stores use t.TempDir.
// Tests that would spawn a browser/clipboard helper (openBrowser, pbcopy)
// are skipped on darwin to avoid hijacking the developer's desktop; on
// linux CI those exec calls fail silently and the code paths still run.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// sa113RoundTripper adapts a function to http.RoundTripper.
type sa113RoundTripper func(*http.Request) (*http.Response, error)

func (f sa113RoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// swapDefaultClient installs a temporary http.DefaultClient whose transport
// forwards requests whose URL host matches routeHost to the given handler
// (plain HTTP, no TLS: the rewrite happens before dialing). The original
// client is restored via t.Cleanup.
func swapDefaultClient(t *testing.T, routeHost string, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	orig := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = orig })
	http.DefaultClient = &http.Client{
		Timeout: 10 * time.Second,
		Transport: sa113RoundTripper(func(req *http.Request) (*http.Response, error) {
			if req.URL.Host == routeHost {
				u := *req.URL
				u.Scheme = "http"
				u.Host = strings.TrimPrefix(srv.URL, "http://")
				req.URL = &u
				req.Host = u.Host
			}
			return http.DefaultTransport.RoundTrip(req)
		}),
	}
	return srv
}

// ---------------------------------------------------------------------------
// claude_oauth.go: ExchangeClaudeCodeForTokens / RefreshClaudeToken
// (hardcoded platform.claude.com URL -> DefaultClient transport swap)
// ---------------------------------------------------------------------------

func TestExchangeClaudeCodeForTokens_SuccessAutomatic(t *testing.T) {
	srv := swapDefaultClient(t, "platform.claude.com", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/oauth/token" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["grant_type"] != "authorization_code" {
			t.Errorf("grant_type = %q", body["grant_type"])
		}
		wantRedirect := fmt.Sprintf("http://localhost:%d%s", 8765, claudeOAuthCallbackPath)
		if body["redirect_uri"] != wantRedirect {
			t.Errorf("redirect_uri = %q, want %q", body["redirect_uri"], wantRedirect)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "claude-at",
			"refresh_token": "claude-rt",
			"expires_in":    3600,
			"scope":         "user:profile",
		})
	})

	resp, err := ExchangeClaudeCodeForTokens(context.Background(), "code-x", "verifier-x", false, 8765)
	if err != nil {
		t.Fatalf("ExchangeClaudeCodeForTokens: %v", err)
	}
	_ = srv
	if resp.AccessToken != "claude-at" || resp.RefreshToken != "claude-rt" || resp.ExpiresIn != 3600 {
		t.Errorf("unexpected token response: %+v", resp)
	}
}

func TestExchangeClaudeCodeForTokens_ManualRedirect(t *testing.T) {
	swapDefaultClient(t, "platform.claude.com", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["redirect_uri"] != claudeOAuthManualRedirect {
			t.Errorf("manual redirect_uri = %q, want %q", body["redirect_uri"], claudeOAuthManualRedirect)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at"})
	})
	if _, err := ExchangeClaudeCodeForTokens(context.Background(), "c", "v", true, 1); err != nil {
		t.Fatalf("manual exchange: %v", err)
	}
}

func TestExchangeClaudeCodeForTokens_ErrorStatus(t *testing.T) {
	swapDefaultClient(t, "platform.claude.com", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant"}`)
	})
	_, err := ExchangeClaudeCodeForTokens(context.Background(), "bad", "v", false, 0)
	if err == nil || !strings.Contains(err.Error(), "token exchange failed [400]") {
		t.Errorf("expected 400 exchange error, got %v", err)
	}
}

func TestExchangeClaudeCodeForTokens_MissingAccessToken(t *testing.T) {
	swapDefaultClient(t, "platform.claude.com", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"expires_in":10}`)
	})
	_, err := ExchangeClaudeCodeForTokens(context.Background(), "c", "v", false, 0)
	if err == nil || !strings.Contains(err.Error(), "missing access_token") {
		t.Errorf("expected missing access_token error, got %v", err)
	}
}

func TestRefreshClaudeToken_Success(t *testing.T) {
	swapDefaultClient(t, "platform.claude.com", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["grant_type"] != "refresh_token" {
			t.Errorf("grant_type = %q", body["grant_type"])
		}
		if body["refresh_token"] != "old-rt" {
			t.Errorf("refresh_token = %q", body["refresh_token"])
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-at",
			"refresh_token": "new-rt",
			"expires_in":    7200,
		})
	})
	info, err := RefreshClaudeToken(context.Background(), "old-rt")
	if err != nil {
		t.Fatalf("RefreshClaudeToken: %v", err)
	}
	if info.ProviderID != ProviderAnthropic || info.Type != "oauth" {
		t.Errorf("unexpected info header: %+v", info)
	}
	if info.AccessToken != "new-at" || info.RefreshToken != "new-rt" {
		t.Errorf("unexpected tokens: %+v", info)
	}
	if info.ExpiresAt.IsZero() || time.Until(info.ExpiresAt) > 2*time.Hour+time.Minute {
		t.Errorf("ExpiresAt not derived from expires_in: %v", info.ExpiresAt)
	}
}

func TestRefreshClaudeToken_Fallbacks(t *testing.T) {
	// Missing expires_in -> 3600s default; missing refresh_token -> preserved.
	swapDefaultClient(t, "platform.claude.com", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"only-at"}`)
	})
	info, err := RefreshClaudeToken(context.Background(), "keep-me")
	if err != nil {
		t.Fatalf("RefreshClaudeToken fallback: %v", err)
	}
	if info.RefreshToken != "keep-me" {
		t.Errorf("refresh token not preserved: %q", info.RefreshToken)
	}
	until := time.Until(info.ExpiresAt)
	if until < 55*time.Minute || until > 65*time.Minute {
		t.Errorf("expected ~1h default expiry, got %v", until)
	}
}

func TestRefreshClaudeToken_ErrorStatus(t *testing.T) {
	swapDefaultClient(t, "platform.claude.com", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":"expired_token"}`)
	})
	_, err := RefreshClaudeToken(context.Background(), "rt")
	if err == nil || !strings.Contains(err.Error(), "token refresh failed [403]") {
		t.Errorf("expected 403 refresh error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// a2a_oauth.go: WithValidIssuers
// ---------------------------------------------------------------------------

func TestWithValidIssuers_ExtraIssuersAllowed(t *testing.T) {
	tv, err := NewTokenValidator("client", "https://primary.example.com",
		WithValidIssuers([]string{"https://tenant-a.example.com", "https://tenant-b.example.com"}))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		iss  string
		want bool
	}{
		{"https://primary.example.com", true},
		{"https://primary.example.com/", true}, // trailing slash normalized
		{"https://tenant-a.example.com", true},
		{"https://tenant-b.example.com/", true},
		{"https://evil.example.com", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := tv.isIssuerAllowed(tc.iss); got != tc.want {
			t.Errorf("isIssuerAllowed(%q) = %v, want %v", tc.iss, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// a2a_oauth.go: StartDeviceFlow
// (PKCETokenProvider.GetToken cache-hit path is covered in
// auth_coverage_ext_test.go; the GetToken cache-miss path needs a browser
// and is intentionally not exercised here.)
// ---------------------------------------------------------------------------

func sa113SkipBrowserSideEffects(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("would exec `open`/`pbcopy` on the developer desktop; covered on linux CI")
	}
}

func TestStartDeviceFlow_DeviceCodeRequestError(t *testing.T) {
	// No browser side effects reached: the flow aborts before openBrowser.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"error":             "unauthorized_client",
			"error_description": "client not registered",
		})
	}))
	defer srv.Close()

	cfg := A2AOAuth2Config{AuthorizeURL: srv.URL, TokenURL: srv.URL, ClientID: "c"}
	_, err := StartDeviceFlow(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "unauthorized_client") {
		t.Errorf("expected device code request error, got %v", err)
	}
}

func TestStartDeviceFlow_SuccessFirstPoll(t *testing.T) {
	sa113SkipBrowserSideEffects(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/device/code":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["client_id"] != "cid" {
				t.Errorf("client_id = %q", body["client_id"])
			}
			json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "dc-1",
				"user_code":        "ABCD-EFGH",
				"verification_uri": "http://127.0.0.1:1/device",
				"interval":         0,
			})
		case "/device/token":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["device_code"] != "dc-1" {
				t.Errorf("device_code = %q", body["device_code"])
			}
			if body["grant_type"] != "urn:ietf:params:oauth:grant-type:device_code" {
				t.Errorf("grant_type = %q", body["grant_type"])
			}
			json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "device-at",
				"refresh_token": "device-rt",
				"token_type":    "bearer",
				"expires_in":    900,
			})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	cfg := A2AOAuth2Config{
		AuthorizeURL: srv.URL + "/device/code",
		TokenURL:     srv.URL + "/device/token",
		ClientID:     "cid",
		Scopes:       []string{"read:user"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	token, err := StartDeviceFlow(ctx, cfg)
	if err != nil {
		t.Fatalf("StartDeviceFlow: %v", err)
	}
	if token.AccessToken != "device-at" || token.RefreshToken != "device-rt" {
		t.Errorf("unexpected token: %+v", token)
	}
	if token.Expiry.IsZero() {
		t.Error("expected expiry from expires_in")
	}
}

func TestStartDeviceFlow_CtxCancelledDuringWait(t *testing.T) {
	sa113SkipBrowserSideEffects(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"device_code":      "dc-2",
			"user_code":        "XXXX-YYYY",
			"verification_uri": "http://127.0.0.1:1/device",
			"interval":         60, // long wait; ctx should fire first
		})
	}))
	defer srv.Close()

	cfg := A2AOAuth2Config{AuthorizeURL: srv.URL, TokenURL: srv.URL, ClientID: "c"}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	done := make(chan error, 1)
	go func() {
		_, err := StartDeviceFlow(ctx, cfg)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("StartDeviceFlow did not observe ctx cancellation")
	}
}

// ---------------------------------------------------------------------------
// copilot.go: StartCopilotDeviceFlow public wrapper (DefaultClient routing)
// ---------------------------------------------------------------------------

func TestStartCopilotDeviceFlow_InvalidEnterpriseURL(t *testing.T) {
	_, err := StartCopilotDeviceFlow(context.Background(), "https:///path")
	if err == nil {
		t.Error("expected error for enterprise URL without host")
	}
}

func TestStartCopilotDeviceFlow_DefaultClientRouting(t *testing.T) {
	srv := swapDefaultClient(t, "github.com", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/device/code" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if ua := r.Header.Get("User-Agent"); ua != "ggcode" {
			t.Errorf("User-Agent = %q", ua)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"device_code":      "dev-pub",
			"user_code":        "PUBB-0001",
			"verification_uri": "https://github.com/login/device",
			"interval":         0,
		})
	})
	flow, err := StartCopilotDeviceFlow(context.Background(), "")
	if err != nil {
		t.Fatalf("StartCopilotDeviceFlow: %v", err)
	}
	if flow.Domain != "github.com" {
		t.Errorf("Domain = %q", flow.Domain)
	}
	if flow.DeviceCode != "dev-pub" || flow.UserCode != "PUBB-0001" {
		t.Errorf("unexpected flow: %+v", flow)
	}
	if flow.Interval != 5*time.Second {
		t.Errorf("interval should clamp to 5s, got %v", flow.Interval)
	}
	_ = srv
}

// ---------------------------------------------------------------------------
// opencode_oauth.go: StartOpenCodeDeviceFlow + toErr mapping
// ---------------------------------------------------------------------------

func TestStartOpenCodeDeviceFlow_SuccessAndIncomplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/device/code" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("User-Agent") != "opencode/1.16.2" {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"device_code": "oc-dc",
			"user_code":   "OC-1234",
		})
	}))
	defer srv.Close()

	auth, err := StartOpenCodeDeviceFlow(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("StartOpenCodeDeviceFlow: %v", err)
	}
	if auth.DeviceCode != "oc-dc" || auth.UserCode != "OC-1234" {
		t.Errorf("unexpected auth: %+v", auth)
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	}))
	defer empty.Close()
	if _, err := StartOpenCodeDeviceFlow(context.Background(), empty.URL); err == nil ||
		!strings.Contains(err.Error(), "incomplete") {
		t.Errorf("expected incomplete response error, got %v", err)
	}
}

func TestOpenCodeTokenErrorToErr_Mapping(t *testing.T) {
	cases := []struct {
		errStr string
		want   error
	}{
		{"authorization_pending", ErrOpenCodePending},
		{"slow_down", ErrOpenCodeSlowDown},
		{"expired_token", ErrOpenCodeExpired},
		{"access_denied", ErrOpenCodeDenied},
	}
	for _, tc := range cases {
		got := (&openCodeTokenError{Error: tc.errStr}).toErr()
		if !errors.Is(got, tc.want) {
			t.Errorf("toErr(%q) = %v, want %v", tc.errStr, got, tc.want)
		}
	}
	if got := (&openCodeTokenError{Error: "weird", ErrorDescription: "details"}).toErr(); got == nil ||
		!strings.Contains(got.Error(), "weird: details") {
		t.Errorf("unexpected unmapped error: %v", got)
	}
	if got := (&openCodeTokenError{Error: "weird"}).toErr(); got == nil ||
		!strings.Contains(got.Error(), "weird") {
		t.Errorf("unexpected bare error: %v", got)
	}
}

// ---------------------------------------------------------------------------
// store.go: saveAll / loadAll error branches via real filesystem failures
// ---------------------------------------------------------------------------

func TestStoreSave_MkdirAllFailsWhenParentIsFile(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Parent path component is a regular file. Save() fails in loadAll() first
	// (reading the store path returns ENOTDIR); on platforms where the read
	// succeeds the write phase fails at MkdirAll. Both are real error paths.
	store := NewStore(filepath.Join(blocker, "sub", "store.json"))
	err := store.Save(&Info{ProviderID: "p", Type: "oauth", AccessToken: "t"})
	if err == nil {
		t.Fatal("expected error when a parent path component is a file")
	}
	if !strings.Contains(err.Error(), "reading auth store") &&
		!strings.Contains(err.Error(), "creating auth store directory") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStoreSave_CorruptedExistingFileFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	if err := os.WriteFile(path, []byte("}{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewStore(path)
	err := store.Save(&Info{ProviderID: "p", Type: "oauth", AccessToken: "t"})
	if err == nil || !strings.Contains(err.Error(), "parsing auth store") {
		t.Errorf("expected parse error, got %v", err)
	}
}

func TestStoreLoad_ReadErrorWhenPathIsDirectory(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir) // a directory, ReadFile fails with non-IsNotExist error
	if _, err := store.Load("any"); err == nil || !strings.Contains(err.Error(), "reading auth store") {
		t.Errorf("expected read error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// a2a_oauth.go: GetToken cache-miss paths
// ---------------------------------------------------------------------------

func TestDeviceFlowTokenProvider_GetToken_CacheMissFailsFast(t *testing.T) {
	// Cache key deliberately mismatches the saved entry so LoadValid misses;
	// StartDeviceFlow then fails on the (unroutable) device-code request,
	// before any browser/clipboard side effect.
	cache := NewTokenCache(t.TempDir())
	_ = cache.Save(CacheKey("github", "other-client"), &PKCEToken{
		AccessToken: "other-at",
		Expiry:      time.Now().Add(time.Hour),
	}, "other-client")

	p := &DeviceFlowTokenProvider{
		Config:   A2AOAuth2Config{ClientID: "cid", AuthorizeURL: "http://127.0.0.1:1/device/code"},
		Provider: "github",
		Cache:    cache,
	}
	_, _, _, err := p.GetToken(context.Background())
	if err == nil {
		t.Fatal("expected error from unreachable device-code endpoint")
	}
}

func TestPKCETokenProvider_GetToken_CacheMissCtxCancelled(t *testing.T) {
	sa113SkipBrowserSideEffects(t)
	cache := NewTokenCache(t.TempDir())
	p := &PKCETokenProvider{
		Config:   A2AOAuth2Config{ClientID: "cid", AuthorizeURL: "http://127.0.0.1:1/noop"},
		Provider: "github",
		Cache:    cache, // empty cache -> guaranteed miss
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err := p.GetToken(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// a2a_oauth.go: openBrowser (empty URL fails to launch, no browser opens)
// ---------------------------------------------------------------------------

func TestOpenBrowser_EmptyURLFailsSilently(t *testing.T) {
	openBrowser("") // best-effort: must not panic and must not open anything
}

func TestStartPKCEFlow_CtxCancelledImmediately(t *testing.T) {
	sa113SkipBrowserSideEffects(t)
	cfg := A2AOAuth2Config{
		AuthorizeURL: "http://127.0.0.1:1/noop",
		TokenURL:     "http://127.0.0.1:1/token",
		ClientID:     "pkce-client",
		Scopes:       []string{"read:user"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() {
		_, err := StartPKCEFlow(ctx, cfg)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("StartPKCEFlow did not observe ctx cancellation")
	}
}
