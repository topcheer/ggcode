package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/a2a"
	"github.com/topcheer/ggcode/internal/config"
)

// #3677: every configuration-origin failure in the A2A auth wiring must be
// classifiable via errors.Is(err, a2a.ErrConfig) so the TUI and daemon
// callers can abort startup instead of downgrading the failure to an
// invisible debug.Log. Covers the fail-fast families from #1174 (unknown
// provider / no client_id), #1175 (no issuer) and #1503/#2781 (unfilled
// preset placeholders).
func TestBuildA2ATokenValidator_WrapsErrConfig(t *testing.T) {
	authCfg := &config.A2AAuthConfig{}
	cases := []struct {
		name     string
		kind     string
		provider string
		clientID string
		issuer   string
		wantMsg  string
	}{
		{"unknown provider typo", "oauth2", "gihub", "abc", "https://ex.com", "unknown provider"},
		{"unknown provider kind oidc", "oidc", "nope", "abc", "", "unknown provider"},
		{"empty block resolves to nothing", "oauth2", "", "", "", "no client_id resolved"},
		{"github preset has no OIDC discovery", "oidc", "github", "abc", "", "no issuer available"},
		{"auth0 preset unfilled tenant", "oauth2", "auth0", "abc", "", "preset placeholder"},
		{"azure preset unfilled tenant", "oidc", "azure", "abc", "", "preset placeholder"},
	}
	for _, tc := range cases {
		_, err := buildA2ATokenValidator(tc.kind, tc.provider, tc.clientID, tc.issuer, "", authCfg)
		if err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
			continue
		}
		if !errors.Is(err, a2a.ErrConfig) {
			t.Errorf("%s: error does not wrap a2a.ErrConfig: %v", tc.name, err)
		}
		if tc.kind == "oidc" && !strings.Contains(err.Error(), "a2a oidc") {
			t.Errorf("%s: error text missing kind prefix: %v", tc.name, err)
		}
		if tc.wantMsg != "" && !strings.Contains(err.Error(), tc.wantMsg) {
			t.Errorf("%s: error %q missing %q", tc.name, err, tc.wantMsg)
		}
	}
}

// A fully specified manual issuer builds a validator offline (no network
// access at construction) and must NOT wrap ErrConfig.
func TestBuildA2ATokenValidator_ValidConfigSucceeds(t *testing.T) {
	authCfg := &config.A2AAuthConfig{}
	tv, err := buildA2ATokenValidator("oauth2", "", "my-client", "https://auth.example.com", "read:a2a", authCfg)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if tv == nil {
		t.Fatal("expected non-nil validator")
	}
}
