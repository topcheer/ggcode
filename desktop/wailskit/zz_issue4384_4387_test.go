//go:build goolm

package wailskit

// #4384/#4385/#4387 regression tests.
//
// #4384: SaveHooks must write instance-sourced hooks back to the instance
// file - cfg.Save() strips tracked instance keys from the global write, so
// without the write-back a hooks edit on an instance workspace silently
// reverted on restart while SaveHooks returned nil.
//
// #4385: UpdateConfig validated defaultMode/a2aPort nowhere (while the
// dedicated SaveDefaultModePreference path whitelists) - a typo'd value
// saved "successfully", silently downgraded this process, and bricked the
// next config.Load.
//
// #4387: runtime impersonation must activate only AFTER cfg.Save()
// succeeds - the old order left the runtime impersonated when Save failed
// (disk full/permissions), a session-long runtime/disk fork (#740).

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

// #4384: hooks edit on an instance workspace must survive a cold re-load.
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

	// Cold reload from disk: the instance merge must yield the NEW hooks.
	// Before the fix, Save() stripped hooks from the global file and the
	// instance file still held the old pre_tool_use list, so the user's
	// edit silently reverted on restart.
	reloaded, err := config.LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}
	if len(reloaded.Hooks.PostToolUse) != 1 || reloaded.Hooks.PostToolUse[0].Command != "echo new" {
		t.Fatalf("instance write-back missing: reloaded post_tool_use = %+v, want [echo new] (#4384)", reloaded.Hooks.PostToolUse)
	}
}

// #4385: defaultMode is whitelisted + normalized exactly like the dedicated
// SaveDefaultModePreference path; a2aPort rejects negatives.
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
	// The rejected updates must not have mutated the resident config nor
	// bricked the file for the next Load (#740/#4385).
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

// makeSaveFail replaces the on-disk global config path with a directory so
// cfg.Save() fails deterministically (opening a directory for write errors
// on every platform).
func makeSaveFail(t *testing.T, globalPath string) {
	t.Helper()
	if err := os.Remove(globalPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(globalPath, 0755); err != nil {
		t.Fatal(err)
	}
}

// #4387 (UpdateConfig path): a failed Save must not change the runtime
// activation. (Snapshot-compare: the package-global provider state is
// shared with other tests in this package, so assert "unchanged", not
// "empty".)
func TestIssue4387_UpdateConfigImpersonationOnlyAfterSave(t *testing.T) {
	globalPath, _ := setupConfigTestEnv(t, "")
	makeSaveFail(t, globalPath)

	// Seed a distinct baseline: the package-global activation is shared
	// across this package's tests (e.g. the #3852 suite activates
	// claude-cli/2.1.209), so reset to a known state and assert THAT is
	// preserved - under the old code this Save failure still flipped the
	// runtime to the values below.
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

// #4387 (ApplyImpersonation path): same contract for the dedicated entry
// point.
func TestIssue4387_ApplyImpersonationOnlyAfterSave(t *testing.T) {
	globalPath, _ := setupConfigTestEnv(t, "")
	makeSaveFail(t, globalPath)

	// Seed a distinct baseline - see the UpdateConfig twin above.
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
