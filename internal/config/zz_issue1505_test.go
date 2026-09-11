package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
)

// #1505 case 2: refreshClaudeOAuthToken must single-flight the Anthropic
// OAuth refresh and, critically, re-read the store after acquiring the
// lock: a concurrent refresh that finished while we waited has already
// persisted a fresh token, and reusing it avoids replaying the (now
// server-side invalidated) refresh token.
//
// The fresh-on-disk short-circuit is what makes these tests network-free:
// if the function wrongly decided to refresh, it would dial the real
// Anthropic token endpoint and these tests would fail or hang instead.

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
