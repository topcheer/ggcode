package agentruntime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
)

func TestBuildInteractiveRuntimeCoreRegistersSharedBootstrapTools(t *testing.T) {
	wd := t.TempDir()
	cfg := config.DefaultConfig()

	policy := BuildInteractivePermissionPolicy(cfg, wd, false)
	core, err := BuildInteractiveRuntimeCore(cfg, wd, policy)
	if err != nil {
		t.Fatal(err)
	}
	if core.Registry == nil || core.MCPManager == nil || core.PluginManager == nil || core.CommandManager == nil || core.SaveMemoryTool == nil {
		t.Fatal("expected runtime core fields to be populated")
	}

	names := map[string]bool{}
	for _, toolDef := range core.Registry.List() {
		names[toolDef.Name()] = true
	}
	for _, want := range []string{"save_memory", "list_mcp_capabilities", "get_mcp_prompt", "read_mcp_resource", "run_command"} {
		if !names[want] {
			t.Fatalf("expected tool %q in runtime core registry", want)
		}
	}
}

func TestBuildInteractiveRuntimeCoreLoadsProjectSkills(t *testing.T) {
	wd := t.TempDir()
	skillsDir := filepath.Join(wd, ".ggcode", "skills", "collaborate")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `---
name: Collaborate
description: Team workflow
when_to_use: Use when collaboration is needed.
---
Skill body`
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	core, err := BuildInteractiveRuntimeCore(cfg, wd, permission.NewConfigPolicyWithMode(nil, []string{wd}, permission.AutoMode))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, cmd := range core.CommandManager.List() {
		if cmd.Name == "collaborate" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected project skill to be loaded into shared command manager")
	}
}

func TestBuildInteractivePermissionPolicyMatchesConfigToolPerms(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AllowedDirs = []string{"."}
	cfg.DefaultMode = "auto"
	cfg.ToolPerms = map[string]config.ToolPermission{
		"read_file": "allow",
		"edit_file": "deny",
	}
	wd := t.TempDir()
	policy := BuildInteractivePermissionPolicy(cfg, wd, false)
	if policy == nil {
		t.Fatal("expected policy")
	}
	if got := policy.Mode(); got != permission.AutoMode {
		t.Fatalf("mode = %v, want auto", got)
	}
	if !policy.AllowedPath(filepath.Join(wd, "foo.txt")) {
		t.Fatal("expected working dir path to be allowed")
	}
}
