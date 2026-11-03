package wailskit

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

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

	if cfg.Model != before {
		t.Fatalf("half-applied update: cfg.Model = %q, want %q", cfg.Model, before)
	}
	if cfg.Impersonation.Preset == "bogus-preset-id" {
		t.Fatalf("bogus preset id leaked into cfg.Impersonation.Preset")
	}
}

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
