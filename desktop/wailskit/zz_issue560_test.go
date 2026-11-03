package wailskit

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
)

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
