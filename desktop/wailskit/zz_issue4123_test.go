package wailskit

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// #4123: LoadSession's restore short-circuit must compare the full
// (vendor, endpoint, model) triple, not the bare model name. Identical model
// names across vendors are common (gateways re-export official models); the
// old name-only check silently kept the global provider while the session's
// Vendor field pointed elsewhere, diverging state and billing the wrong
// endpoint bucket.
func TestSessionSelectionMatches(t *testing.T) {
	official := &config.ResolvedEndpoint{VendorID: "anthropic", EndpointID: "primary", Model: "claude-4"}
	gateway := &config.ResolvedEndpoint{VendorID: "openrouter", EndpointID: "gw", Model: "claude-4"}

	cases := []struct {
		name        string
		resolved    *config.ResolvedEndpoint
		sesVendor   string
		sesEndpoint string
		sesModel    string
		want        bool
	}{
		{"nil resolved never matches", nil, "anthropic", "primary", "claude-4", false},
		{"full triple match", official, "anthropic", "primary", "claude-4", true},
		{"same model different vendor (#4123 core)", official, "openrouter", "gw", "claude-4", false},
		{"same model different endpoint", official, "anthropic", "other", "claude-4", false},
		{"different model same vendor", official, "anthropic", "primary", "claude-5", false},
		{"gateway resolved matches gateway session", gateway, "openrouter", "gw", "claude-4", true},
		{"legacy empty vendor+endpoint falls back to model name", official, "", "", "claude-4", true},
		{"legacy empty pair still requires model match", official, "", "", "claude-5", false},
		{"vendor-only provenance compares vendor and model", official, "anthropic", "", "claude-4", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sessionSelectionMatches(tc.resolved, tc.sesVendor, tc.sesEndpoint, tc.sesModel)
			if got != tc.want {
				t.Fatalf("sessionSelectionMatches(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
