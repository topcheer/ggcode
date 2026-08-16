package wailskit

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
)

// Feature tests for issue #560 A-1: AnthropicOAuthStatus previously checked
// only info.AccessToken != "" and ignored ExpiresAt — with an expired (e.g.
// revoked) token the probe observed statusShown=true while usable=false and
// isExpired=true, so the desktop Settings page showed "connected" while API
// calls failed with 401. The fix aligns the status with
// auth.Store.HasUsableToken semantics (token present AND not past ExpiresAt;
// zero ExpiresAt means no expiry is known and counts as usable).

// isolateAuthStore560 points HOME at a fresh temp dir so the tests never
// touch the real ~/.ggcode/provider_auth.json. util.HomeDir re-reads $HOME
// on every call, and auth.DefaultStore()/DefaultPath() are computed per
// call, so t.Setenv is sufficient isolation.
func isolateAuthStore560(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func saveAnthropicInfo560(t *testing.T, info *auth.Info) {
	t.Helper()
	info.ProviderID = auth.ProviderAnthropic
	info.Type = "oauth"
	if err := auth.DefaultStore().Save(info); err != nil {
		t.Fatalf("saving auth info: %v", err)
	}
}

// TestIssue560ExpiredTokenNotConnected is the exact probe-verified
// contradiction: non-empty AccessToken with ExpiresAt in the past must NOT
// report connected.
func TestIssue560ExpiredTokenNotConnected(t *testing.T) {
	isolateAuthStore560(t)
	saveAnthropicInfo560(t, &auth.Info{
		AccessToken: "expired-revoked-token",
		ExpiresAt:   time.Now().Add(-1 * time.Hour),
	})
	if AnthropicOAuthStatus() {
		t.Fatal("expired token must not report connected (#560): statusShown=true while usable=false")
	}
}

func TestIssue560ValidTokenConnected(t *testing.T) {
	isolateAuthStore560(t)
	saveAnthropicInfo560(t, &auth.Info{
		AccessToken: "valid-token",
		ExpiresAt:   time.Now().Add(1 * time.Hour),
	})
	if !AnthropicOAuthStatus() {
		t.Fatal("valid, unexpired token must report connected")
	}
}

// TestIssue560NoExpiryKnownConnected matches HasUsableToken semantics: a
// token with zero ExpiresAt (no expiry recorded) counts as usable.
func TestIssue560NoExpiryKnownConnected(t *testing.T) {
	isolateAuthStore560(t)
	saveAnthropicInfo560(t, &auth.Info{
		AccessToken: "no-expiry-token",
	})
	if !AnthropicOAuthStatus() {
		t.Fatal("token with unknown expiry must report connected (HasUsableToken semantics)")
	}
}

func TestIssue560NoTokenNotConnected(t *testing.T) {
	isolateAuthStore560(t)
	if AnthropicOAuthStatus() {
		t.Fatal("no stored token must not report connected")
	}
}

// TestIssue560EmptyAccessTokenNotConnected guards the TrimSpace check from
// HasUsableToken: a whitespace-only access token is not usable.
func TestIssue560EmptyAccessTokenNotConnected(t *testing.T) {
	isolateAuthStore560(t)
	saveAnthropicInfo560(t, &auth.Info{
		AccessToken: "   ",
		ExpiresAt:   time.Now().Add(1 * time.Hour),
	})
	if AnthropicOAuthStatus() {
		t.Fatal("blank access token must not report connected")
	}
}
