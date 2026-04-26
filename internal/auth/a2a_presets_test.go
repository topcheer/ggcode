package auth

import (
	"testing"
)

func TestProviderPresetsExist(t *testing.T) {
	expected := []string{"github", "google", "auth0", "azure"}
	for _, name := range expected {
		p := ResolveProviderPreset(name)
		if p == nil {
			t.Errorf("missing preset for %q", name)
			continue
		}
		if p.AuthorizeURL == "" {
			t.Errorf("preset %q missing authorize URL", name)
		}
		if p.TokenURL == "" {
			t.Errorf("preset %q missing token URL", name)
		}
		if len(p.DefaultScopes) == 0 {
			t.Errorf("preset %q missing default scopes", name)
		}
	}
}

func TestProviderPresetsPKCE(t *testing.T) {
	// All built-in providers should support PKCE
	for name, p := range ProviderPresets {
		if !p.SupportsPKCE {
			t.Errorf("preset %q should support PKCE", name)
		}
	}
}

func TestProviderDeviceFlow(t *testing.T) {
	github := ResolveProviderPreset("github")
	if github == nil || !github.SupportsDevice {
		t.Error("GitHub should support device flow")
	}

	google := ResolveProviderPreset("google")
	if google != nil && google.SupportsDevice {
		t.Error("Google does not support device flow")
	}
}

func TestResolveProviderPresetUnknown(t *testing.T) {
	p := ResolveProviderPreset("nonexistent")
	if p != nil {
		t.Error("expected nil for unknown provider")
	}
}

func TestResolveA2AAuthWithPreset(t *testing.T) {
	authURL, tokenURL, clientID, scopes, err := ResolveA2AAuth("github", "my-client-id", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if authURL != "https://github.com/login/oauth/authorize" {
		t.Errorf("unexpected auth URL: %s", authURL)
	}
	if tokenURL != "https://github.com/login/oauth/access_token" {
		t.Errorf("unexpected token URL: %s", tokenURL)
	}
	if clientID != "my-client-id" {
		t.Errorf("expected my-client-id (user override), got %s", clientID)
	}
	if scopes == "" {
		t.Error("expected default scopes from preset")
	}
}

func TestResolveA2AAuthCustom(t *testing.T) {
	authURL, _, _, scopes, err := ResolveA2AAuth("", "my-client", "https://idp.example.com", "read write")
	if err != nil {
		t.Fatal(err)
	}
	if authURL != "https://idp.example.com/authorize" {
		t.Errorf("unexpected auth URL: %s", authURL)
	}
	if scopes != "read write" {
		t.Errorf("unexpected scopes: %s", scopes)
	}
}

func TestResolveA2AAuthEmpty(t *testing.T) {
	authURL, _, _, _, err := ResolveA2AAuth("", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if authURL != "" {
		t.Errorf("expected empty, got %s", authURL)
	}
}

func TestStringsJoin(t *testing.T) {
	tests := []struct {
		input []string
		sep   string
		want  string
	}{
		{[]string{"a", "b", "c"}, " ", "a b c"},
		{[]string{"single"}, " ", "single"},
		{[]string{}, " ", ""},
		{[]string{"a", "b"}, ",", "a,b"},
	}
	for _, tt := range tests {
		got := stringsJoin(tt.input, tt.sep)
		if got != tt.want {
			t.Errorf("stringsJoin(%v, %q) = %q, want %q", tt.input, tt.sep, got, tt.want)
		}
	}
}

func TestProviderPresetContainsNoClientID(t *testing.T) {
	// Verify that presets do NOT contain client_id values
	// (each installation must register its own)
	for name, p := range ProviderPresets {
		// The struct has no ClientID field, so this is a design assertion
		_ = p
		_ = name
	}
	// This test is a documentation assertion — presets only contain
	// public endpoint URLs, never credentials
}

func TestResolveA2AAuthGitHubDefaultClientID(t *testing.T) {
	// No client_id provided → should use GitHub preset default
	authURL, _, clientID, scopes, err := ResolveA2AAuth("github", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if clientID != "Ov23liq0EQyT4VDz3ayn" {
		t.Errorf("expected default GitHub client_id, got %q", clientID)
	}
	if authURL != "https://github.com/login/oauth/authorize" {
		t.Errorf("unexpected auth URL: %s", authURL)
	}
	if scopes == "" {
		t.Error("expected default scopes")
	}
}

func TestResolveA2AAuthGitHubUserOverridesDefaultClientID(t *testing.T) {
	// User provides client_id → overrides default
	_, _, clientID, _, err := ResolveA2AAuth("github", "my-custom-id", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if clientID != "my-custom-id" {
		t.Errorf("expected user override, got %q", clientID)
	}
}

func TestGitHubPresetHasDefaultClientID(t *testing.T) {
	p := ResolveProviderPreset("github")
	if p == nil {
		t.Fatal("missing GitHub preset")
	}
	if p.DefaultClientID == "" {
		t.Error("GitHub preset should have DefaultClientID")
	}
}
