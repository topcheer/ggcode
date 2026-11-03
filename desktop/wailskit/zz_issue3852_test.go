//go:build goolm

package wailskit

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

const issue3852Instance = `
impersonation:
  preset: gemini-cli
  custom_version: "0.5.0"
`

func TestIssue3852_ApplyImpersonationInstanceWriteback(t *testing.T) {
	globalPath, workspace := setupConfigTestEnv(t, issue3852Instance)

	cfg := GetGlobalConfig()
	if !cfg.HasInstanceConfigAttached() {
		t.Skip("instance not attached in this env")
	}
	if cfg.Impersonation.Preset != "gemini-cli" {
		t.Fatalf("instance preset should merge into view, got %q", cfg.Impersonation.Preset)
	}

	if err := ApplyImpersonation("claude-cli", "2.1.209", nil); err != nil {
		t.Fatalf("ApplyImpersonation: %v", err)
	}

	reloaded, err := config.LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}
	if reloaded.Impersonation.Preset != "claude-cli" {
		t.Fatalf("instance write-back missing: reloaded preset = %q, want claude-cli (#3852 A)", reloaded.Impersonation.Preset)
	}
	if reloaded.Impersonation.CustomVersion != "2.1.209" {
		t.Fatalf("reloaded custom_version = %q, want 2.1.209", reloaded.Impersonation.CustomVersion)
	}
}

func TestIssue3852_UpdateConfigVersionBeforeActivate(t *testing.T) {
	_, _ = setupConfigTestEnv(t, "")
	cfg := GetGlobalConfig()
	cfg.Impersonation = config.ImpersonationConfig{Preset: "gemini-cli", CustomVersion: "0.5.0"}

	if err := UpdateConfig(map[string]interface{}{
		"impersonatePreset":        "claude-cli",
		"impersonateCustomVersion": "2.1.209",
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	_, version, _ := provider.GetActiveImpersonation()
	if version != "2.1.209" {
		t.Fatalf("runtime active version = %q, want 2.1.209 - CustomVersion must be written before SetActiveImpersonation (#3852 B)", version)
	}
	if got := GetGlobalConfig().Impersonation.CustomVersion; got != "2.1.209" {
		t.Fatalf("persisted custom_version = %q, want 2.1.209", got)
	}
}
