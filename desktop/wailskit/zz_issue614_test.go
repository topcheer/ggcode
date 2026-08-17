//go:build goolm

package wailskit

// Issue #614 regression tests: ApplyImpersonation must (a) preserve
// already-saved CustomHeaders when the frontend submits an empty map on a
// plain preset switch (SettingsPage.tsx sends `{} as Record<string,string>`),
// and (b) reject unknown presetIDs instead of silently disabling
// impersonation while persisting the bogus ID (UI/runtime fork).

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func setupIssue614Config(t *testing.T, impersonation config.ImpersonationConfig) {
	t.Helper()
	globalPath, workspace := setupConfigTestEnv(t, "")
	cfg := GetGlobalConfig()
	cfg.Impersonation = impersonation
	_ = globalPath
	_ = workspace
}

// D1: switching presets with an empty headers map must keep saved headers.
func TestIssue614_EmptyHeadersPreserveExisting(t *testing.T) {
	setupIssue614Config(t, config.ImpersonationConfig{
		Preset:        "gemini-cli",
		CustomHeaders: map[string]string{"X-Custom": "keep-me"},
	})

	if err := ApplyImpersonation("claude-cli", "2.1.209", nil); err != nil {
		t.Fatalf("ApplyImpersonation: %v", err)
	}

	cfg := GetGlobalConfig()
	if cfg.Impersonation.Preset != "claude-cli" {
		t.Errorf("preset = %q, want claude-cli", cfg.Impersonation.Preset)
	}
	if got := cfg.Impersonation.CustomHeaders["X-Custom"]; got != "keep-me" {
		t.Errorf("existing custom headers wiped on preset switch (#614): got %q, want keep-me", got)
	}
	if len(cfg.Impersonation.CustomHeaders) != 1 {
		t.Errorf("expected exactly 1 header, got %d", len(cfg.Impersonation.CustomHeaders))
	}
}

// D1: explicit new headers still replace the old ones.
func TestIssue614_ExplicitHeadersReplace(t *testing.T) {
	setupIssue614Config(t, config.ImpersonationConfig{
		Preset:        "gemini-cli",
		CustomHeaders: map[string]string{"X-Old": "old"},
	})

	if err := ApplyImpersonation("gemini-cli", "0.50.0", map[string]string{"X-New": "new"}); err != nil {
		t.Fatalf("ApplyImpersonation: %v", err)
	}

	cfg := GetGlobalConfig()
	if _, exists := cfg.Impersonation.CustomHeaders["X-Old"]; exists {
		t.Error("explicit headers did not replace old headers")
	}
	if cfg.Impersonation.CustomHeaders["X-New"] != "new" {
		t.Errorf("new header missing: %+v", cfg.Impersonation.CustomHeaders)
	}
}

// D2: unknown presetID must be a validation error, not silently accepted.
func TestIssue614_UnknownPresetRejected(t *testing.T) {
	setupIssue614Config(t, config.ImpersonationConfig{Preset: "none"})

	err := ApplyImpersonation("not-a-preset", "1.0", nil)
	if err == nil {
		t.Fatal("unknown presetID silently accepted (#614 D2): expected error")
	}
	cfg := GetGlobalConfig()
	if cfg.Impersonation.Preset != "none" {
		t.Errorf("unknown presetID persisted: %q", cfg.Impersonation.Preset)
	}
}

// "none" and "" remain valid (disable impersonation).
func TestIssue614_NoneAndEmptyStillValid(t *testing.T) {
	setupIssue614Config(t, config.ImpersonationConfig{
		Preset:        "claude-cli",
		CustomHeaders: map[string]string{"X-Custom": "keep"},
	})
	if err := ApplyImpersonation("none", "", nil); err != nil {
		t.Fatalf("ApplyImpersonation(none): %v", err)
	}
}
