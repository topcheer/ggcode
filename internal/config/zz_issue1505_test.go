package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
)

func TestRefreshClaudeOAuthTokenReusesFreshTokenOnDisk(t *testing.T) {
	store := auth.NewStore(filepath.Join(t.TempDir(), "provider_auth.json"))
	fresh := &auth.Info{
		ProviderID:   auth.ProviderAnthropic,
		Type:         "oauth",
		AccessToken:  "fresh-at",
		RefreshToken: "rotated-rt",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	if err := store.Save(fresh); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := refreshClaudeOAuthToken(store)
	if err != nil {
		t.Fatalf("refreshClaudeOAuthToken() error = %v", err)
	}
	if got != "fresh-at" {
		t.Fatalf("token = %q, want %q (fresh on-disk token must short-circuit)", got, "fresh-at")
	}
}

func TestRefreshClaudeOAuthTokenMissingCredentials(t *testing.T) {
	store := auth.NewStore(filepath.Join(t.TempDir(), "provider_auth.json"))
	_, err := refreshClaudeOAuthToken(store)
	if err == nil {
		t.Fatal("expected error for missing credentials")
	}
	if !strings.Contains(err.Error(), "/login") {
		t.Fatalf("error should point at /login, got: %v", err)
	}
}

func TestRefreshClaudeOAuthTokenExpiredNoRefreshToken(t *testing.T) {
	store := auth.NewStore(filepath.Join(t.TempDir(), "provider_auth.json"))
	expired := &auth.Info{
		ProviderID:  auth.ProviderAnthropic,
		Type:        "oauth",
		AccessToken: "stale-at",
		ExpiresAt:   time.Now().Add(-time.Hour),
	}
	if err := store.Save(expired); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	_, err := refreshClaudeOAuthToken(store)
	if err == nil {
		t.Fatal("expected error for expired token without refresh token")
	}
	if !strings.Contains(err.Error(), "/login") {
		t.Fatalf("error should point at /login, got: %v", err)
	}
}
