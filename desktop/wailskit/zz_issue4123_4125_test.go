package wailskit

// zz_issue4123_4125_test.go pins the desktop fixes from PR #4134:
//
// #4123: LoadSession's restore short-circuit compared only the model name,
// so a same-name model served by a different vendor skipped
// ActivateCurrentSelection and silently kept the global vendor while
// currentSes.Vendor kept the saved one (usage bucketing billed the wrong
// endpoint). sessionSelectionMatches now requires vendor/endpoint match
// too, when the session recorded them.
//
// #4125: RuntimePermissionMode fell back to a hardcoded "supervised" that
// desynced enforcement from the auto default InitAgent actually armed.

import (
	"testing"

	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/session"
)

// #4123: table-driven pin of the restore short-circuit predicate.
func TestIssue4123_RestoreSelectionMatchesRequiresVendorMatch(t *testing.T) {
	openrouter := &config.ResolvedEndpoint{Model: "claude-4", VendorID: "openrouter", EndpointID: "gw"}
	cases := []struct {
		name           string
		resolved       *config.ResolvedEndpoint
		model, vendor  string
		endpoint       string
		wantActivation bool // want sessionSelectionMatches == false
	}{
		{"nil resolved always activates", nil, "claude-4", "openrouter", "gw", true},
		{"exact match skips activation", openrouter, "claude-4", "openrouter", "gw", false},
		{"model name alone is NOT enough (cross vendor, #4123 regression)", openrouter, "claude-4", "anthropic", "", true},
		{"cross endpoint with same model activates", openrouter, "claude-4", "openrouter", "official", true},
		{"different model activates", openrouter, "gpt-6", "openrouter", "gw", true},
		{"legacy session without vendor/endpoint tolerates name match", openrouter, "claude-4", "", "", false},
	}
	for _, tc := range cases {
		matches := sessionSelectionMatches(tc.resolved, tc.vendor, tc.endpoint, tc.model)
		if matches == tc.wantActivation {
			t.Errorf("%s: sessionSelectionMatches = %v, want %v", tc.name, matches, !tc.wantActivation)
		}
	}
}

// #4125: with no session-pinned mode, the fallback must recompute the same
// default InitAgent arms - never a hardcoded "supervised".
func TestIssue4125_RuntimePermissionModeFallbackRecomputesDefault(t *testing.T) {
	b := &ChatBridge{cfg: &config.Config{}}
	p := &desktopRuntimeProvider{bridge: b}

	want := agentruntime.InteractivePermissionModeWithDefault(b.cfg, false, "auto").String()
	if got := p.RuntimePermissionMode(); got != want {
		t.Fatalf("fallback = %q, want recomputed default %q", got, want)
	}

	// Session-pinned mode keeps precedence over the fallback.
	b.currentSes = &session.Session{PermissionMode: "bypass"}
	if got := p.RuntimePermissionMode(); got != "bypass" {
		t.Fatalf("session-pinned mode lost precedence: got %q, want bypass", got)
	}
}
