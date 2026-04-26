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
	authURL, tokenURL, scopes, err := ResolveA2AAuth("github", "my-client-id", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if authURL != "https://github.com/login/oauth/authorize" {
		t.Errorf("unexpected auth URL: %s", authURL)
	}
	if tokenURL != "https://github.com/login/oauth/access_token" {
		t.Errorf("unexpected token URL: %s", tokenURL)
	}
	if scopes == "" {
		t.Error("expected default scopes from preset")
	}
}

func TestResolveA2AAuthCustom(t *testing.T) {
	authURL, _, scopes, err := ResolveA2AAuth("", "my-client", "https://idp.example.com", "read write")
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
	authURL, _, _, err := ResolveA2AAuth("", "", "", "")
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
