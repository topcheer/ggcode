package tool

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
)

// nonBuiltinAllowlisted maps every allowlisted tool that RegisterBuiltinTools
// does NOT register to a constructor from this package, so a rename of any of
// them (the list_memory / memory_recall / cron / team / worktree family lives
// in agentruntime, tui and cmd) fails here instead of as a bare "Object has no
// member" at run time.
var nonBuiltinAllowlisted = map[string]Tool{
	"list_memory":           NewListMemoryTool(nil, nil),
	"memory_recall":         NewMemoryRecallTool(nil, nil),
	"recall_toolflow":       NewRecallToolFlowTool(),
	"list_namedagent":       ListNamedAgentTool{},
	"list_worktree":         &ListWorktree{},
	"task_list":             TaskListTool{},
	"task_get":              TaskGetTool{},
	"task_output":           TaskOutputTool{},
	"cron_list":             CronListTool{},
	"cron_get":              CronGetTool{},
	"subagent_context":      SubagentContextTool{},
	"team_list":             TeamListTool{},
	"teammate_list":         TeammateListTool{},
	"teammate_results":      TeammateResultsTool{},
	"team_review_digest":    TeamReviewDigestTool{},
	"swarm_task_list":       SwarmTaskListTool{},
	"get_mcp_prompt":        GetMCPPromptTool{},
	"recall_compacted":      &RecallCompactedTool{},
	"session_recall":        SessionRecall{},
	"why":                   Why{},
	"recall_experience":     NewRecallExperienceTool(""),
	"symbols":               SymbolsTool{},
	"current_time":          NewCurrentTimeTool(),
	"list_commands":         ListCommandsTool{},
	"read_command_output":   ReadCommandOutputTool{},
	"wait_command":          WaitCommandTool{},
	"ci_status":             CIStatusTool{},
	"pr_delta":              PRDelta{},
	"review_changes":        ReviewChanges{},
	"scan_todos":            ScanTodosTool{},
	"code_health":           CodeHealthTool{},
	"dep_graph":             DepGraphTool{},
	"list_agents":           ListAgentsTool{},
	"list_mcp_capabilities": ListMCPCapabilitiesTool{},
	"read_mcp_resource":     ReadMCPResourceTool{},
}

func builtinRegistryForAllowlist(t *testing.T) *Registry {
	t.Helper()
	reg := NewRegistry()
	if err := RegisterBuiltinTools(reg, nil, t.TempDir(), nil, nil); err != nil {
		t.Fatalf("RegisterBuiltinTools: %v", err)
	}
	return reg
}

// Every sandbox name must be a real tool: registered by RegisterBuiltinTools,
// or produced by the constructor named in nonBuiltinAllowlisted with exactly
// that Name(). This is the test that would have caught the stale list before
// `tools.list_memory` failed at run time.
func TestSandboxAllowlist_EveryNameIsARealTool(t *testing.T) {
	reg := builtinRegistryForAllowlist(t)
	for _, name := range SandboxToolNames() {
		if _, ok := reg.Get(name); ok {
			continue
		}
		ctor, ok := nonBuiltinAllowlisted[name]
		if !ok {
			t.Errorf("%q is allowlisted but is neither built-in nor listed in nonBuiltinAllowlisted - add the constructor or drop the name", name)
			continue
		}
		if got := ctor.Name(); got != name {
			t.Errorf("constructor for %q produces Name() %q", name, got)
		}
	}
	for name := range nonBuiltinAllowlisted {
		if !readOnlyToolNames[name] {
			t.Errorf("nonBuiltinAllowlisted carries %q but the allowlist does not - keep them in sync", name)
		}
	}
}

// The permission layer's read-only classification and the sandbox allowlist
// must agree for every registered built-in. Anything read-only for the
// permission layer but withheld from the sandbox needs an explicit reason.
func TestSandboxAllowlist_AgreesWithPermissionReadOnly(t *testing.T) {
	withheld := map[string]string{
		"sleep":          "waits for wall time; pointless under the 30 s run budget",
		"code_execution": "recursion",
	}
	reg := builtinRegistryForAllowlist(t)
	for _, name := range reg.ToolNames() {
		if !permission.IsReadOnlyTool(name) {
			continue
		}
		if readOnlyToolNames[name] {
			continue
		}
		if reason, ok := withheld[name]; ok {
			_ = reason
			continue
		}
		t.Errorf("%q is read-only for the permission layer but not callable in code_execution - add it to sandboxToolGroups or to the withheld table with a reason", name)
	}
	for name := range withheld {
		if readOnlyToolNames[name] {
			t.Errorf("%q is documented as withheld but is in the allowlist", name)
		}
	}
}

// The description is generated from the same groups, so it must list exactly
// the callable names - no stale entries, no missing ones.
func TestSandboxAllowlist_DescriptionMatchesAllowlist(t *testing.T) {
	desc := CodeExecution{}.Description()
	for _, name := range SandboxToolNames() {
		if !strings.Contains(desc, name) {
			t.Errorf("description does not mention allowlisted tool %q", name)
		}
	}
	for _, stale := range []string{"sleep", "config", "intention", "edit_file", "run_command", "browser"} {
		if readOnlyToolNames[stale] {
			t.Errorf("%q must not be callable in the sandbox", stale)
		}
	}
	if !strings.Contains(desc, "tools.names()") {
		t.Error("description must point the model at tools.names()")
	}
}

// End to end through the real sandbox: a formerly missing tool (list_memory)
// is callable, names()/describe() work, and a non-read-only or unknown member
// yields the actionable message instead of goja's bare TypeError.
func TestSandbox_ListMemoryCallableAndFriendlyErrors(t *testing.T) {
	reg := builtinRegistryForAllowlist(t)
	if err := reg.Register(NewListMemoryTool(nil, nil)); err != nil {
		t.Fatal(err)
	}
	ce := CodeExecution{Registry: reg}

	res, err := ce.Execute(t.Context(), json.RawMessage(`{"code":"const r = await tools.list_memory({scope:\"all\",query:\"zz\"}); console.log(typeof r + ':' + (r||'').length);","description":"list_memory through the sandbox"}`))
	if err != nil || res.IsError {
		t.Fatalf("list_memory must be callable in the sandbox: err=%v res=%+v", err, res)
	}
	if strings.Contains(res.Content, "no member") {
		t.Fatalf("bare goja TypeError leaked: %s", res.Content)
	}

	res, _ = ce.Execute(t.Context(), json.RawMessage(`{"code":"const n = JSON.parse(await tools.names()); console.log(n.includes('list_memory'), n.includes('edit_file'), n.length);","description":"names()"}`))
	if res.IsError || !strings.Contains(res.Content, "true false") {
		t.Fatalf("names() must list list_memory and not edit_file: %+v", res)
	}

	res, _ = ce.Execute(t.Context(), json.RawMessage(`{"code":"const d = JSON.parse(await tools.describe('grep')); console.log(d.name, d.parameters.type, Object.keys(d.parameters.properties).length > 0);","description":"describe()"}`))
	if res.IsError || !strings.Contains(res.Content, "grep object true") {
		t.Fatalf("describe('grep') must return name+parameters: %+v", res)
	}
	res, _ = ce.Execute(t.Context(), json.RawMessage(`{"code":"await tools.describe('edit_file');","description":"describe() on a write tool"}`))
	if !res.IsError || !strings.Contains(res.Content, "not callable inside code_execution") {
		t.Fatalf("describe() of a write tool must be refused: %+v", res)
	}

	// Registered but not read-only -> actionable message.
	res, _ = ce.Execute(t.Context(), json.RawMessage(`{"code":"await tools.edit_file({});","description":"write tool in sandbox"}`))
	if !res.IsError {
		t.Fatal("edit_file must not run in the sandbox")
	}
	for _, want := range []string{"tools.edit_file", "not a read-only tool", "tools.names()"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("error must mention %q, got: %s", want, res.Content)
		}
	}
	if strings.Contains(res.Content, "Object has no member") {
		t.Errorf("bare TypeError leaked: %s", res.Content)
	}

	// Not registered at all -> says so (the screenshot case: a name the model
	// guessed).
	res, _ = ce.Execute(t.Context(), json.RawMessage(`{"code":"await tools.definitely_not_a_tool({});","description":"unknown tool"}`))
	if !res.IsError || !strings.Contains(res.Content, "does not exist") {
		t.Fatalf("unknown member must be reported as non-existent: %+v", res)
	}

	// The tools Proxy makes the message available INSIDE a try/catch as well -
	// the exit-point rewrite above only ever covered uncaught errors, which is
	// what the live session showed after the first fix: a caught
	// `tools.edit_file` still surfaced goja's bare TypeError.
	res, _ = ce.Execute(t.Context(), json.RawMessage(`{"code":"let m=''; try { await tools.edit_file({}); } catch(e) { m=String(e); } let u=''; try { await tools.definitely_not_a_tool({}); } catch(e) { u=String(e); } console.log(m); console.log(u); console.log(typeof tools.then, Object.keys(tools).includes('grep'), 'grep' in tools, 'edit_file' in tools);","description":"caught errors and object semantics"}`))
	if res.IsError {
		t.Fatalf("caught-error probe failed: %+v", res)
	}
	for _, want := range []string{"tools.edit_file is not callable inside code_execution", "tools.definitely_not_a_tool does not exist",
		"undefined true true false"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("caught-error probe must contain %q, got: %s", want, res.Content)
		}
	}
	if strings.Contains(res.Content, "Object has no member") {
		t.Errorf("bare TypeError leaked through a caught error: %s", res.Content)
	}
}
