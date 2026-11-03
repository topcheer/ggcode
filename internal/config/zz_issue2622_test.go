package config

import (
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
)

func TestResolveEndpointSelectionClaudeOAuthExpiredNoRefresh(t *testing.T) {
	withTestHome(t)
	t.Setenv("HOME", t.TempDir())
	store := auth.DefaultStore()
	if err := store.Save(&auth.Info{
		ProviderID:  auth.ProviderAnthropic,
		Type:        "oauth",
		AccessToken: "stale-at",
		ExpiresAt:   time.Now().Add(-time.Hour),
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
