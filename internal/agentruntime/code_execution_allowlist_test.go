package agentruntime

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/tool"
)

// The code_execution sandbox resolves allowlisted names against the REAL
// registry. This builds the interactive core (the memory / recall / symbols /
// MCP / config registrations live here, not in tool.RegisterBuiltinTools) plus
// the cron tools, and asserts every sandbox name exists - the memory family
// (list_memory, memory_recall, ...) is exactly what was missing when
// `tools.list_memory` failed with "Object has no member".
//
// Names wired only by cmd/ggcode (root.go) and internal/tui are listed as
// registeredElsewhere; they are still covered by the constructor check in
// internal/tool (TestSandboxAllowlist_EveryNameIsARealTool).
func TestSandboxAllowlist_ResolvesInRuntimeCoreRegistry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	wd := t.TempDir()
	cfg := config.DefaultConfig()
	policy := BuildInteractivePermissionPolicy(cfg, wd, false)
	core, err := BuildInteractiveRuntimeCore(cfg, wd, policy)
	if err != nil {
		t.Fatal(err)
	}
	RegisterCronTools(core.Registry, NewSessionCronScheduler("sess-test", wd, func(string, bool) {}))

	have := map[string]bool{}
	for _, td := range core.Registry.List() {
		have[td.Name()] = true
	}
	registeredElsewhere := map[string]bool{
		// cmd/ggcode/root.go + internal/tui wiring, after the core is built
		"recall_compacted": true,
		// agentruntime/subsystems.go (needs live managers) and cmd/ggcode wiring
		"list_namedagent": true, "subagent_context": true,
		"team_list": true, "teammate_list": true, "teammate_results": true, "team_review_digest": true, "swarm_task_list": true,
		"task_list": true, "task_get": true, "task_output": true, "list_agents": true,
	}
	for _, name := range tool.SandboxToolNames() {
		if have[name] || registeredElsewhere[name] {
			continue
		}
		t.Errorf("sandbox allowlists %q but the runtime core registry has no such tool - rename, or move it to registeredElsewhere with the owner", name)
	}
	for name := range registeredElsewhere {
		if have[name] {
			t.Errorf("%q is now registered by the runtime core - drop it from registeredElsewhere", name)
		}
	}
}
