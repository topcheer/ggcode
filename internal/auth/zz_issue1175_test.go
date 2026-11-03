package auth

import (
	"strings"
	"testing"
)

func TestResolveA2AIssuerURLPresetUsesDiscoveryNotTokenURL(t *testing.T) {
	for name, preset := range ProviderPresets {
		if preset.OIDCDiscovery == "" {
			continue
		}
		got := ResolveA2AIssuerURL(name, "")
		if got == "" {
			t.Errorf("provider %q: expected issuer derived from OIDCDiscovery, got empty", name)
			continue
		}
		if got != preset.OIDCDiscovery {
			t.Errorf("provider %q: issuer = %q, want OIDCDiscovery %q", name, got, preset.OIDCDiscovery)
		}
		if strings.Contains(got, preset.TokenURL) {
			t.Errorf("provider %q: issuer %q must not be derived from TokenURL %q", name, got, preset.TokenURL)
		}
		if !strings.Contains(got, "/.well-known/") {
			t.Errorf("provider %q: issuer %q should be an OIDC discovery URL", name, got)
		}
	}
}

func TestResolveA2AIssuerURLExplicitConfigWins(t *testing.T) {
	custom := "https://idp.example.com"
	if got := ResolveA2AIssuerURL("google", custom); got != custom {
		t.Errorf("explicit issuer_url must win, got %q want %q", got, custom)
	}
	if got := ResolveA2AIssuerURL("", custom); got != custom {
		t.Errorf("explicit issuer_url without provider must win, got %q want %q", got, custom)
	}
}

func TestResolveA2AIssuerURLNoOIDCPresetReturnsEmpty(t *testing.T) {
	p := ResolveProviderPreset("github")
	if p == nil {
		t.Fatal("missing github preset")
	}
	if got := ResolveA2AIssuerURL("github", ""); got != "" {
		t.Errorf("github has no OIDC discovery; expected empty issuer, got %q (must not equal TokenURL %q)", got, p.TokenURL)
	}
	if got := ResolveA2AIssuerURL("github", ""); got == p.TokenURL {
		t.Errorf("issuer must never fall back to TokenURL %q", p.TokenURL)
	}
}

func TestResolveA2AIssuerURLUnknownProviderReturnsEmpty(t *testing.T) {
	if got := ResolveA2AIssuerURL("gihub", ""); got != "" {
		t.Errorf("unknown provider should resolve to empty issuer, got %q", got)
	}
}

func TestNewTokenValidatorIssuerSeparationFromPreset(t *testing.T) {
	preset := ResolveProviderPreset("google")
	if preset == nil {
		t.Fatal("missing google preset")
	}
	issuer := ResolveA2AIssuerURL("google", "")
	if issuer == "" || issuer == preset.TokenURL {
		t.Fatalf("issuer %q unusable (TokenURL is %q)", issuer, preset.TokenURL)
	}
	tv, err := NewTokenValidator("test-client", issuer)
	if err != nil {
		t.Fatalf("NewTokenValidator: %v", err)
	}
	if !tv.isIssuerAllowed(preset.OIDCDiscovery) {
		t.Errorf("discovery URL %q should be an allowed issuer", preset.OIDCDiscovery)
	}
	base := strings.Split(preset.OIDCDiscovery, "/.well-known/")[0]
	if !tv.isIssuerAllowed(base) {
		t.Errorf("base issuer %q should be allowed via /.well-known stripping", base)
	}
	if tv.isIssuerAllowed(preset.TokenURL) {
		t.Errorf("TokenURL %q must not be an allowed issuer", preset.TokenURL)
	}
}
