package wailskit

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// #3770: impersonatePreset validation must run in the #740 pre-validation
// block -- BEFORE any field write. The pre-fix shape validated the preset
// after nine fields were already written in place, so
// UpdateConfig({"model":"x","impersonatePreset":"bogus"}) returned an error
// while cfg.Model had silently changed (and the next successful save would
// persist it). Contract: a failed UpdateConfig leaves the config exactly as
// it was.
func TestIssue3770_BogusPresetLeavesConfigUntouched(t *testing.T) {
	_, _ = setupConfigTestEnv(t, "")

	cfg := GetGlobalConfig()
	cfg.Model = "original-model"
	before := cfg.Model

	err := UpdateConfig(map[string]interface{}{
		"model":             "new-model",
		"impersonatePreset": "bogus-preset-id",
	})
	if err == nil {
		t.Fatal("expected unknown-preset error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown impersonation preset") {
		t.Fatalf("unexpected error: %v", err)
	}
	// The whole request must be atomic: the legal model write must NOT have
	// landed either (pre-fix it did -- the reported half-applied bug).
	if cfg.Model != before {
		t.Fatalf("half-applied update: cfg.Model = %q, want %q", cfg.Model, before)
	}
	if cfg.Impersonation.Preset == "bogus-preset-id" {
		t.Fatalf("bogus preset id leaked into cfg.Impersonation.Preset")
	}
}

// Positive control: a valid preset id still applies, and the same map's
// model write lands -- proving the moved validation gates only the bogus
// case, not the whole feature.
func TestIssue3770_ValidPresetStillApplies(t *testing.T) {
	_, _ = setupConfigTestEnv(t, "")

	presets := provider.DefaultImpersonationPresets()
	if len(presets) == 0 {
		t.Skip("no default impersonation presets in this build")
	}
	validID := presets[0].ID

	cfg := GetGlobalConfig()
	if err := UpdateConfig(map[string]interface{}{
		"model":             "new-model",
		"impersonatePreset": validID,
	}); err != nil {
		t.Fatalf("valid preset rejected: %v", err)
	}
	if cfg.Model != "new-model" {
		t.Fatalf("model not applied: %q", cfg.Model)
	}
	if cfg.Impersonation.Preset != validID {
		t.Fatalf("preset not applied: %q", cfg.Impersonation.Preset)
	}
}
