package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/hooks"
)

// CHARACTERIZATION TEST - documents a real, verified bug (do not "fix" the
// test without fixing the bug; when the bug is fixed, invert the assertions
// marked BUG).
//
// Bug: clearing instance-scope hooks (non-empty -> empty) is silently lost.
//
// Evidence chain (verified against the failing repro, see git history):
//  1. WRITE-SIDE SUPPRESSION: diffHooks (instance_delta.go) only writes
//     pre_tool_use/post_tool_use into the delta when len(current) > 0, so a
//     cleared list never enters the delta.
//  2. NO RESET FALLBACK: applyInstanceResets (instance_reset.go) deletes
//     stale on-disk keys only for instanceResetManagedKeys (scalars, #2106)
//     and instanceResetNestedKeys (knight/subagents/swarm, #2936). "hooks"
//     is in neither table, so no nil deletion marker is emitted for it.
//  3. MERGE KEEPS STALE KEYS: deepMergeYAMLMaps (config_save.go) only
//     deletes a dst key when the delta carries an explicit nil marker;
//     unmentioned keys survive, so the stale hooks stay on disk.
//  4. READ-SIDE SUPPRESSION: mergeHookConfig (instance.go) replaces the
//     global list only when len(instance) > 0, so the stale non-empty
//     on-disk list resurrects on the next LoadWithInstance.
//
// Scalar fields and knight/subagents/swarm all closed the identical
// resurrection hole (#2106/#2936); hooks is the only family left with
// write-side zero-suppression AND no reset fallback.
func TestInstanceHooksClearToEmptySilentlyLost(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	// Global has NO hooks; hooks live only at instance scope.
	if err := os.WriteFile(globalPath, []byte("language: en\n"), 0644); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	instPath := filepath.Join(InstanceDir(workspace), "ggcode.yaml")

	// Phase 1: set an instance-scope hook [A] and save.
	cfg, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}
	cfg.Hooks.PreToolUse = []hooks.Hook{{Match: "*", Command: "echo A"}}
	if err := cfg.SaveInstance(workspace); err != nil {
		t.Fatalf("SaveInstance(set): %v", err)
	}
	instData, err := os.ReadFile(instPath)
	if err != nil || !strings.Contains(string(instData), "echo A") {
		t.Fatalf("precondition: instance file should carry hook A; err=%v data=%s", err, instData)
	}

	// Phase 2: reload so [A] comes from the instance file, then CLEAR hooks.
	cfg2, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(cfg2.Hooks.PreToolUse) == 0 {
		t.Fatal("precondition: reloaded merged config should carry hook A from instance file")
	}
	cfg2.Hooks.PreToolUse = nil
	cfg2.Hooks.PostToolUse = nil
	if err := cfg2.SaveInstance(workspace); err != nil {
		t.Fatalf("SaveInstance(clear): %v", err)
	}

	// Write side: FIXED (#4315) - the cleared hooks must NOT remain on disk.
	// With hooks as the only instance key, the post-reset prune may remove
	// the instance file entirely (existing family behavior for fully-reset
	// instances) - absence is the strongest pass.
	instData, err = os.ReadFile(instPath)
	if err == nil {
		if strings.Contains(string(instData), "echo A") {
			t.Errorf("#4315 regression (write side): cleared hooks still on disk after SaveInstance")
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("read instance file after clear: %v", err)
	}

	// Read side: FIXED (#4315) - no resurrection on reload (global has no
	// hooks in this fixture, so the merged config must be empty).
	cfg3, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("reload after clear: %v", err)
	}
	if len(cfg3.Hooks.PreToolUse) != 0 || len(cfg3.Hooks.PostToolUse) != 0 {
		t.Errorf("#4315 regression (read side): cleared hooks resurrected on reload: pre=%d post=%d", len(cfg3.Hooks.PreToolUse), len(cfg3.Hooks.PostToolUse))
	}
}
