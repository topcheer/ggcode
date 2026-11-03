package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/hooks"
)

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

	cfg.ToolPerms["bash"] = ToolPermDeny
	if err := cfg.SaveInstance(workspace); err != nil {
		t.Fatalf("SaveInstance: %v", err)
	}

	instData, err := os.ReadFile(filepath.Join(InstanceDir(workspace), "ggcode.yaml"))
	if err != nil {
		t.Fatalf("instance config should exist after SaveInstance: %v", err)
	}
	if !strings.Contains(string(instData), "bash: deny") {
		t.Errorf("instance config missing bash: deny override; got:\n%s", instData)
	}

	cfg2, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("reload LoadWithInstance: %v", err)
	}
	if got := cfg2.ToolPerms["bash"]; got != ToolPermDeny {
		t.Errorf("after reload bash perm = %q, want deny (instance override lost)", got)
	}

	globData, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(globData), "bash: allow") {
		t.Errorf("global file lost its bash: allow; got:\n%s", globData)
	}
}

func TestInstanceToolPermMergeMatrix(t *testing.T) {
	global := &Config{ToolPerms: map[string]ToolPermission{
		"bash": ToolPermAllow,
	}}
	instance := &Config{ToolPerms: map[string]ToolPermission{
		"bash": ToolPermDeny,
		"grep": ToolPermAsk,
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

	cfg2, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("reload LoadWithInstance: %v", err)
	}
	if len(cfg2.Hooks.PreToolUse) != 1 || cfg2.Hooks.PreToolUse[0].Command != "echo instance-hook" {
		t.Errorf("after reload pre_tool_use = %+v, want exactly one instance-hook", cfg2.Hooks.PreToolUse)
	}

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

	globData, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(globData), "echo global-hook") {
		t.Errorf("global file lost its hook; got:\n%s", globData)
	}
}
