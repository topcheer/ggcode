package config

import "testing"

// #4259: an api_keys-only config must NOT fall back to the PUBLIC default
// key - any LAN host knowing the well-known key could authenticate.
func TestIssue4259_APIKeysOnlyNoDefaultFallback(t *testing.T) {
	c := A2AConfig{}
	c.Auth.APIKeys = []string{"my-strong-secret-9f3a"}
	if got := c.EffectiveAPIKey(); got != "" {
		t.Fatalf("api_keys-only config must not surface the default key, got %q", got)
	}
	// Single key still wins; empty config keeps LAN convenience default.
	c2 := A2AConfig{}
	c2.Auth.APIKey = "explicit"
	if got := c2.EffectiveAPIKey(); got != "explicit" {
		t.Fatalf("explicit single key must win, got %q", got)
	}
	if got := (A2AConfig{}).EffectiveAPIKey(); got != DefaultA2AAPIKey {
		t.Fatalf("empty config keeps LAN default, got %q", got)
	}
}
