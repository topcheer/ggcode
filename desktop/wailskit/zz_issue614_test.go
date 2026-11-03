//go:build goolm

package wailskit

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

func TestIssue614_NoneAndEmptyStillValid(t *testing.T) {
	setupIssue614Config(t, config.ImpersonationConfig{
		Preset:        "claude-cli",
		CustomHeaders: map[string]string{"X-Custom": "keep"},
	})
	if err := ApplyImpersonation("none", "", nil); err != nil {
		t.Fatalf("ApplyImpersonation(none): %v", err)
	}
}
