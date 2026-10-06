package agentruntime

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/mcp"
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
	for _, want := range []string{"save_memory", "delete_memory", "list_mcp_capabilities", "get_mcp_prompt", "read_mcp_resource", "run_command"} {
		if !names[want] {
			t.Fatalf("expected tool %q in runtime core registry", want)
		}
	}
}

// TestBuildInteractiveRuntimeCoreGatesProjectMCPServers pins the startup
// containment wiring: a workspace .mcp.json stdio server must NOT enter the
// MCP manager until approved for this workspace (or explicitly bypassed).
func TestBuildInteractiveRuntimeCoreGatesProjectMCPServers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(mcp.ProjectGateEnvVar, "")
	wd := t.TempDir()
	mcpJSON := `{"mcpServers":{"proj-srv":{"type":"stdio","command":"echo","args":["hi"]}}}`
	if err := os.WriteFile(filepath.Join(wd, ".mcp.json"), []byte(mcpJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	policy := BuildInteractivePermissionPolicy(cfg, wd, false)

	snapshotNames := func(core *InteractiveRuntimeCore) map[string]bool {
		out := map[string]bool{}
		for _, s := range core.MCPManager.SnapshotMCP() {
			out[s.Name] = true
		}
		return out
	}

	core, err := BuildInteractiveRuntimeCore(cfg, wd, policy)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotNames(core)["proj-srv"] {
		t.Fatal("project .mcp.json server must be gated out at startup")
	}
	// sa-45: the gate's user-facing warning must ride StartupNotices so
	// the TUI first frame can surface it (not debug.Log only).
	if !slices.ContainsFunc(core.StartupNotices, func(n string) bool {
		return strings.Contains(n, `blocked project MCP server "proj-srv"`)
	}) {
		t.Fatalf("gate warning missing from StartupNotices: %v", core.StartupNotices)
	}

	// Explicit per-invocation bypass (disclosed via warnings) admits it.
	t.Setenv(mcp.ProjectGateEnvVar, "1")
	core, err = BuildInteractiveRuntimeCore(cfg, wd, policy)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshotNames(core)["proj-srv"] {
		t.Fatal("bypass must admit the project .mcp.json server")
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

func TestInteractivePermissionMode(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DefaultMode = ""
	if got := InteractivePermissionMode(cfg, false); got != permission.SupervisedMode {
		t.Fatalf("empty mode = %v, want supervised", got)
	}
	if got := InteractivePermissionModeWithDefault(cfg, false, "auto"); got != permission.AutoMode {
		t.Fatalf("default auto mode = %v, want auto", got)
	}
	cfg.DefaultMode = "plan"
	if got := InteractivePermissionMode(cfg, false); got != permission.PlanMode {
		t.Fatalf("plan mode = %v, want plan", got)
	}
	if got := InteractivePermissionMode(cfg, true); got != permission.BypassMode {
		t.Fatalf("bypass mode = %v, want bypass", got)
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
