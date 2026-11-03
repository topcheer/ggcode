//go:build goolm

package wailskit

import (
	"os"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/provider"
)

const issue4384Instance = `
hooks:
  pre_tool_use:
    - command: echo old
`

func TestIssue4384_SaveHooksInstanceWriteback(t *testing.T) {
	globalPath, workspace := setupConfigTestEnv(t, issue4384Instance)

	cfg := GetGlobalConfig()
	if !cfg.HasInstanceConfigAttached() {
		t.Skip("instance not attached in this env")
	}
	if len(cfg.Hooks.PreToolUse) == 0 {
		t.Fatalf("instance hooks should merge into view")
	}

	b := &ChatBridge{cfg: cfg}
	newHooks := hooks.HookConfig{
		PostToolUse: []hooks.Hook{{Match: "*", Command: "echo new"}},
	}
	if err := b.SaveHooks(newHooks); err != nil {
		t.Fatalf("SaveHooks: %v", err)
	}

	reloaded, err := config.LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}
	if len(reloaded.Hooks.PostToolUse) != 1 || reloaded.Hooks.PostToolUse[0].Command != "echo new" {
		t.Fatalf("instance write-back missing: reloaded post_tool_use = %+v, want [echo new] (#4384)", reloaded.Hooks.PostToolUse)
	}
}

func TestIssue4385_UpdateConfigDefaultModeValidation(t *testing.T) {
	globalPath, _ := setupConfigTestEnv(t, "")

	if err := UpdateConfig(map[string]interface{}{"defaultMode": " bypass "}); err != nil {
		t.Fatalf("normalized bypass should be accepted: %v", err)
	}
	if got := GetGlobalConfig().DefaultMode; got != "bypass" {
		t.Fatalf("default_mode = %q, want normalized \"bypass\" (#4385)", got)
	}

	for _, bad := range []string{"bogus", "SUPERVISEDx", "plan"} {
		if err := UpdateConfig(map[string]interface{}{"defaultMode": bad}); err == nil {
			t.Fatalf("defaultMode %q must be rejected (#4385)", bad)
		}
	}

	if got := GetGlobalConfig().DefaultMode; got != "bypass" {
		t.Fatalf("rejected update leaked into resident config: %q", got)
	}
	if _, err := config.Load(globalPath); err != nil {
		t.Fatalf("config file must still load after rejected updates: %v", err)
	}

	if err := UpdateConfig(map[string]interface{}{"a2aPort": -1.0}); err == nil {
		t.Fatalf("negative a2aPort must be rejected (#4385)")
	}
	if p := GetGlobalConfig().A2A.Port; p < 0 {
		t.Fatalf("rejected negative port leaked: %d", p)
	}
}

func makeSaveFail(t *testing.T, globalPath string) {
	t.Helper()
	if err := os.Remove(globalPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(globalPath, 0755); err != nil {
		t.Fatal(err)
	}
}

func TestIssue4387_UpdateConfigImpersonationOnlyAfterSave(t *testing.T) {
	globalPath, _ := setupConfigTestEnv(t, "")
	makeSaveFail(t, globalPath)

	provider.SetActiveImpersonation(nil, "", nil)
	wantPreset, wantVersion, _ := provider.GetActiveImpersonation()
	if wantPreset != nil || wantVersion != "" {
		t.Fatalf("baseline reset failed")
	}

	err := UpdateConfig(map[string]interface{}{"impersonatePreset": "claude-cli"})
	if err == nil {
		t.Fatalf("Save should have failed against a directory config path")
	}
	preset, version, _ := provider.GetActiveImpersonation()
	if preset != wantPreset || version != wantVersion {
		t.Fatalf("runtime impersonation must stay untouched when Save fails: got preset=%v version=%q, want preset=%v version=%q (#4387)", preset, version, wantPreset, wantVersion)
	}
}

func TestIssue4387_ApplyImpersonationOnlyAfterSave(t *testing.T) {
	globalPath, _ := setupConfigTestEnv(t, "")
	makeSaveFail(t, globalPath)

	provider.SetActiveImpersonation(nil, "", nil)
	wantPreset, wantVersion, _ := provider.GetActiveImpersonation()
	if wantPreset != nil || wantVersion != "" {
		t.Fatalf("baseline reset failed")
	}

	if err := ApplyImpersonation("claude-cli", "2.1.209", nil); err == nil {
		t.Fatalf("Save should have failed against a directory config path")
	}
	preset, version, _ := provider.GetActiveImpersonation()
	if preset != wantPreset || version != wantVersion {
		t.Fatalf("runtime impersonation must stay untouched when Save fails: got preset=%v version=%q, want preset=%v version=%q (#4387)", preset, version, wantPreset, wantVersion)
	}
}
