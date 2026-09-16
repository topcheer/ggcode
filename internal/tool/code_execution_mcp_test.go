package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// fakeMCPTool is a minimal registry tool shaped like the MCP adapter's
// registered tools (mcp__ prefix + optional SandboxSafe declaration) for
// exercising the code-execution MCP bridge (sa-19) without a live server.
type fakeMCPTool struct {
	name  string
	desc  string
	safe  bool
	resp  string
	mu    sync.Mutex
	calls int
}

func (f *fakeMCPTool) Name() string        { return f.name }
func (f *fakeMCPTool) Description() string { return f.desc }
func (f *fakeMCPTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)
}

// SandboxSafe mirrors mcpTool.SandboxSafe: safe=true only for read_only
// servers' tools.
func (f *fakeMCPTool) SandboxSafe() bool { return f.safe }

func (f *fakeMCPTool) Execute(_ context.Context, _ json.RawMessage) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return Result{Content: f.resp}, nil
}

func (f *fakeMCPTool) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func mcpTestRegistry() (*Registry, *fakeMCPTool, *fakeMCPTool) {
	safe := &fakeMCPTool{
		name: "mcp__test__search",
		desc: "searches test data",
		safe: true,
		resp: `{"hits": [1, 2, 3]}`,
	}
	unsafe := &fakeMCPTool{
		name: "mcp__test__create_issue",
		desc: "creates a test issue (side effect)",
		safe: false,
		resp: "created",
	}
	reg := NewRegistry()
	// Registration order does not matter; ToolNames sorts by name.
	_ = reg.Register(safe)
	_ = reg.Register(unsafe)
	return reg, safe, unsafe
}

func runMCPSandbox(t *testing.T, reg *Registry, code string) (Result, error) {
	t.Helper()
	ce := CodeExecution{Registry: reg}
	result, err := ce.Execute(context.Background(), json.RawMessage(fmt.Sprintf(`{"code": %q}`, code)))
	return result, err
}

// TestCodeExecution_MCPListAndDescribe verifies discovery: mcpList reports
// every MCP tool with its sandbox eligibility, and mcpDescribe returns the
// on-demand schema — including for sandbox=false tools (describing is
// read-only).
func TestCodeExecution_MCPListAndDescribe(t *testing.T) {
	reg, safe, unsafe := mcpTestRegistry()

	code := "var list = JSON.parse(await tools.mcpList());\n" +
		"var out = [];\n" +
		"for (var i = 0; i < list.length; i++) {\n" +
		"  out.push(list[i].name + ':' + list[i].sandbox);\n" +
		"}\n" +
		"console.log(out.sort().join(','));\n" +
		"var d = JSON.parse(await tools.mcpDescribe('mcp__test__create_issue'));\n" +
		"console.log('describe:' + d.name + ':' + String(JSON.stringify(d.parameters).indexOf('q') > -1));\n" +
		"console.log('safe-desc:' + (JSON.parse(await tools.mcpDescribe('mcp__test__search'))).description);\n"
	result, err := runMCPSandbox(t, reg, code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	out := result.Content
	if !strings.Contains(out, "mcp__test__search:true") {
		t.Errorf("expected sandbox-safe tool listed as callable, got:\n%s", out)
	}
	if !strings.Contains(out, "mcp__test__create_issue:false") {
		t.Errorf("expected write tool listed as not sandbox-callable, got:\n%s", out)
	}
	if !strings.Contains(out, "describe:mcp__test__create_issue:true") {
		t.Errorf("expected mcpDescribe to return parameters schema, got:\n%s", out)
	}
	if !strings.Contains(out, "safe-desc:"+safe.desc) {
		t.Errorf("expected mcpDescribe description for safe tool, got:\n%s", out)
	}
	if unsafe.callCount() != 0 {
		t.Errorf("mcpList/mcpDescribe must not execute tools, got %d calls", unsafe.callCount())
	}
}

// TestCodeExecution_MCPDescribeRejectsNonMCP verifies mcpDescribe only
// accepts mcp__-prefixed names.
func TestCodeExecution_MCPDescribeRejectsNonMCP(t *testing.T) {
	reg, _, _ := mcpTestRegistry()
	result, _ := runMCPSandbox(t, reg, "await tools.mcpDescribe('read_file');\n")
	if !result.IsError {
		t.Fatalf("expected error for non-MCP name, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "mcp__server__tool") {
		t.Errorf("expected guidance message, got: %s", result.Content)
	}
}

// TestCodeExecution_MCPSafeToolCallable verifies a sandbox-safe MCP tool is
// callable from JS and its result stays inside the sandbox (only the model's
// console.log summary reaches the context).
func TestCodeExecution_MCPSafeToolCallable(t *testing.T) {
	reg, safe, _ := mcpTestRegistry()
	code := "var r = JSON.parse(await tools['mcp__test__search']({q: 'x'}));\n" +
		"console.log('total hits: ' + r.hits.length);\n"
	result, err := runMCPSandbox(t, reg, code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "total hits: 3") {
		t.Errorf("expected reduced summary in output, got: %s", result.Content)
	}
	if safe.callCount() != 1 {
		t.Errorf("expected exactly 1 MCP execution, got %d", safe.callCount())
	}
}

// TestCodeExecution_MCPWriteToolNotExposed verifies write-capable MCP tools
// are not injected: calling them from JS throws.
func TestCodeExecution_MCPWriteToolNotExposed(t *testing.T) {
	reg, _, unsafe := mcpTestRegistry()
	code := "await tools['mcp__test__create_issue']({title: 'nope'});\n" +
		"console.log('should not reach');\n"
	result, _ := runMCPSandbox(t, reg, code)
	if !result.IsError {
		t.Fatalf("expected error calling non-exposed write tool, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "has no member") {
		t.Errorf("expected undefined-member error, got: %s", result.Content)
	}
	if unsafe.callCount() != 0 {
		t.Errorf("write tool must never execute from the sandbox, got %d calls", unsafe.callCount())
	}
}

// TestCodeExecution_MCPBudgetExceeded verifies the per-run MCP call budget:
// beyond maxMCPCallsPerRun calls, further calls are rejected and the run
// errors out.
func TestCodeExecution_MCPBudgetExceeded(t *testing.T) {
	reg, safe, _ := mcpTestRegistry()
	code := "var last = '';\n" +
		"for (var i = 0; i < 35; i++) {\n" +
		"  last = await tools['mcp__test__search']({q: String(i)});\n" +
		"}\n" +
		"console.log(last);\n"
	result, _ := runMCPSandbox(t, reg, code)
	if !result.IsError {
		t.Fatalf("expected budget-exceeded error, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "MCP call budget exceeded") {
		t.Errorf("expected budget error message, got: %s", result.Content)
	}
	if got := safe.callCount(); got != maxMCPCallsPerRun {
		t.Errorf("expected exactly %d executions before rejection, got %d", maxMCPCallsPerRun, got)
	}
}
