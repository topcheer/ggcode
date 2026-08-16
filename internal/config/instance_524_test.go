package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/hooks"
)

// TestInstanceToolPermValueChangeSurvivesReload (#524 Bug A, both sides):
// global configures `bash: allow`; the user changes it to deny at instance
// scope (/config tool_permissions → SaveInstance). The change must land in
// the instance file (write side: diffToolPerms records VALUE differences,
// not only new keys) and survive a reload (read side: MergeInstance adopts
// instance-wins for tool_permissions). Previously the delta was empty,
// SaveInstance even deleted the instance file and returned nil, and the
// deny silently reverted to the global allow on restart.
func TestInstanceToolPermValueChangeSurvivesReload(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	globalYAML := "language: en\ntool_permissions:\n  bash: allow\n"
	if err := os.WriteFile(globalPath, []byte(globalYAML), 0644); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()

	cfg, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}
	if cfg.ToolPerms["bash"] != ToolPermAllow {
		t.Fatalf("precondition: global bash perm = %q, want allow", cfg.ToolPerms["bash"])
	}

	// Instance-scope value change: allow → deny.
	cfg.ToolPerms["bash"] = ToolPermDeny
	if err := cfg.SaveInstance(workspace); err != nil {
		t.Fatalf("SaveInstance: %v", err)
	}

	// Write side: the value difference must be persisted to the instance file.
	instData, err := os.ReadFile(filepath.Join(InstanceDir(workspace), "ggcode.yaml"))
	if err != nil {
		t.Fatalf("instance config should exist after SaveInstance: %v", err)
	}
	if !strings.Contains(string(instData), "bash: deny") {
		t.Errorf("instance config missing bash: deny override; got:\n%s", instData)
	}

	// Read side: after reload the instance-wins deny must still be in effect.
	cfg2, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("reload LoadWithInstance: %v", err)
	}
	if got := cfg2.ToolPerms["bash"]; got != ToolPermDeny {
		t.Errorf("after reload bash perm = %q, want deny (instance override lost)", got)
	}

	// The global file must keep its own allow — instance overrides never
	// leak into the global config.
	globData, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(globData), "bash: allow") {
		t.Errorf("global file lost its bash: allow; got:\n%s", globData)
	}
}

// TestInstanceToolPermMergeMatrix guards the MergeInstance read-side
// semantics: instance keys absent from global are still added (pre-existing
// behavior), instance values conflict with global (instance wins), and
// global-only keys are untouched.
func TestInstanceToolPermMergeMatrix(t *testing.T) {
	global := &Config{ToolPerms: map[string]ToolPermission{
		"bash": ToolPermAllow,
	}}
	instance := &Config{ToolPerms: map[string]ToolPermission{
		"bash": ToolPermDeny, // value conflict → instance wins
		"grep": ToolPermAsk,  // new key → added
	}}
	MergeInstance(global, instance)

	if got := global.ToolPerms["bash"]; got != ToolPermDeny {
		t.Errorf("bash = %q, want deny (instance-wins on conflict)", got)
	}
	if got := global.ToolPerms["grep"]; got != ToolPermAsk {
		t.Errorf("grep = %q, want ask (new instance key)", got)
	}
	if len(global.ToolPerms) != 2 {
		t.Errorf("unexpected extra keys: %v", global.ToolPerms)
	}
	if !global.instanceFields["tool_permissions"] {
		t.Errorf("tool_permissions not tracked in instanceFields; Save() would leak instance overrides to the global file")
	}
}

// TestInstanceHooksOverrideSurvivesReload (#524 Bug B): with the global
// config already carrying hooks, an instance-level hook change must persist
// through SaveInstance and reload. The old `len(global.PreToolUse)==0` guard
// silently dropped every such change; append merge semantics would instead
// duplicate hooks across save/reload cycles. Replace semantics keep the
// cycle stable: exactly one instance hook after any number of cycles.
func TestInstanceHooksOverrideSurvivesReload(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	globalYAML := "language: en\nhooks:\n  pre_tool_use:\n    - match: \"*\"\n      command: echo global-hook\n"
	if err := os.WriteFile(globalPath, []byte(globalYAML), 0644); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()

	cfg, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}
	if len(cfg.Hooks.PreToolUse) != 1 || cfg.Hooks.PreToolUse[0].Command != "echo global-hook" {
		t.Fatalf("precondition: global pre_tool_use hooks = %+v", cfg.Hooks.PreToolUse)
	}

	// Instance-level hook change: replace the global hook with another.
	cfg.Hooks.PreToolUse = []hooks.Hook{{Match: "edit_file", Command: "echo instance-hook"}}
	if err := cfg.SaveInstance(workspace); err != nil {
		t.Fatalf("SaveInstance: %v", err)
	}

	instData, err := os.ReadFile(filepath.Join(InstanceDir(workspace), "ggcode.yaml"))
	if err != nil {
		t.Fatalf("instance config should exist after SaveInstance: %v", err)
	}
	if !strings.Contains(string(instData), "echo instance-hook") {
		t.Errorf("instance config missing hook override; got:\n%s", instData)
	}

	// Reload: the instance hook must replace (not be dropped, not appended
	// after) the global hook.
	cfg2, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("reload LoadWithInstance: %v", err)
	}
	if len(cfg2.Hooks.PreToolUse) != 1 || cfg2.Hooks.PreToolUse[0].Command != "echo instance-hook" {
		t.Errorf("after reload pre_tool_use = %+v, want exactly one instance-hook", cfg2.Hooks.PreToolUse)
	}

	// Idempotence across another save/reload cycle — append semantics would
	// grow the list ([global, instance, instance, ...]) here.
	if err := cfg2.SaveInstance(workspace); err != nil {
		t.Fatalf("second SaveInstance: %v", err)
	}
	cfg3, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("second reload LoadWithInstance: %v", err)
	}
	if len(cfg3.Hooks.PreToolUse) != 1 || cfg3.Hooks.PreToolUse[0].Command != "echo instance-hook" {
		t.Errorf("hook list not stable across save/reload cycles: %+v", cfg3.Hooks.PreToolUse)
	}

	// The global file keeps its own hook untouched.
	globData, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(globData), "echo global-hook") {
		t.Errorf("global file lost its hook; got:\n%s", globData)
	}
}
