//go:build goolm

package wailskit

// #3852 companions: (A) ApplyImpersonation must write instance-sourced
// impersonation fields back to the instance file - cfg.Save() strips them
// from the global file, so without the write-back the next restart's
// instance merge silently reverts the user's preset switch (#282 family).
// (B) UpdateConfig must apply impersonateCustomVersion BEFORE activating
// the runtime impersonation, or the running provider pins the stale
// version while disk holds the new one.

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

// A: preset switch via ApplyImpersonation on an instance-bound workspace
// must survive a cold re-load from disk.
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

	// Cold reload from disk: the instance merge must now yield the NEW
	// preset. Before the fix, Save() stripped impersonation from the
	// global file and the instance file still held gemini-cli, so the
	// user's switch silently reverted on restart.
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

// B: submitting preset + customVersion in one UpdateConfig batch must
// activate the runtime with the NEW version.
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
