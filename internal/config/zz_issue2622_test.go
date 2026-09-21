package config

import (
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
)

// #2622 pin tests (reviewer-requested on reopen): the fix in 7b466b45 added
// IsExpired() rejections to the two OAuth else branches, but the existing
// TestRefreshClaudeOAuthTokenExpiredNoRefreshToken exercises the refresh
// HELPER - the commit message itself notes the helper guard was unreachable
// dead code behind the caller's condition. These tests pin the CALLER path
// (ResolveEndpointSelection): expired + no refresh token + non-empty access
// token must fail loudly with the re-login hint instead of silently
// resolving to the stale token. Before 7b466b45 both resolved "successfully".

func TestResolveEndpointSelectionClaudeOAuthExpiredNoRefresh(t *testing.T) {
	withTestHome(t)
	t.Setenv("HOME", t.TempDir())
	store := auth.DefaultStore()
	if err := store.Save(&auth.Info{
		ProviderID:  auth.ProviderAnthropic,
		Type:        "oauth",
		AccessToken: "stale-at", // non-empty: the exact state that slipped through
		ExpiresAt:   time.Now().Add(-time.Hour),
		// RefreshToken intentionally empty
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	cfg := DefaultConfig()
	cfg.Vendor = auth.ProviderAnthropic
	cfg.Endpoint = "oauth"
	cfg.Model = "claude-sonnet-4-5"

	_, err := cfg.ResolveEndpointSelection(cfg.Vendor, cfg.Endpoint, cfg.Model)
	if err == nil {
		t.Fatal("expired token with no refresh token must NOT resolve (before #2622 this returned the stale access token)")
	}
	if !strings.Contains(err.Error(), "/login") {
		t.Fatalf("error must carry the re-login hint, got: %v", err)
	}
}

func TestResolveEndpointSelectionOpenCodeOAuthExpiredNoRefresh(t *testing.T) {
	withTestHome(t)
	t.Setenv("HOME", t.TempDir())
	store := auth.DefaultStore()
	if err := store.Save(&auth.Info{
		ProviderID:  "opencode",
		Type:        "oauth",
		AccessToken: "stale-at",
		ExpiresAt:   time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	cfg := DefaultConfig()
	cfg.Vendor = "opencode"
	// The opencode vendor ships zen-anthropic/zen-openai (api-key) endpoints;
	// its OAuth entry arrives as a user-configured endpoint with
	// auth_type: oauth, so inject exactly that shape.
	cfg.Vendors["opencode"].Endpoints["opencode-oauth"] = EndpointConfig{
		DisplayName:  "OpenCode OAuth",
		Protocol:     "anthropic",
		BaseURL:      "https://opencode.ai/zen/v1",
		DefaultModel: "claude-sonnet-4-5",
		AuthType:     "oauth",
	}
	cfg.Endpoint = "opencode-oauth"
	cfg.Model = "claude-sonnet-4-5"

	_, err := cfg.ResolveEndpointSelection(cfg.Vendor, cfg.Endpoint, cfg.Model)
	if err == nil {
		t.Fatal("expired opencode token with no refresh token must NOT resolve (#2622)")
	}
	if !strings.Contains(err.Error(), "ggcode login opencode") {
		t.Fatalf("error must carry the opencode re-login command, got: %v", err)
	}
}
