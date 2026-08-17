package mcp

// Regression tests for issue #645:
//   Bug 1 — mcpTool holds mutable ContextFill but did not implement
//           tool.Cloner, so Registry.Clone shared the instance across agents:
//           the main agent's fill (e.g. 0.75 → 9KB cap) bled into concurrent
//           swarm teammates' MCP results. Fix: Clone returns an independent
//           copy starting at fill 0.
//   Bug 2 — SetSamplingHandler/SetElicitationHandler wrote the handler
//           fields without c.mu while Initialize and the read loop read them.
//           Fix: c.mu-guarded setters plus locked snapshot accessors.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

// staticCaller is a deterministic toolCaller returning a fixed payload.
type issue645Caller struct{}

func (issue645Caller) CallTool(ctx context.Context, name string, args map[string]interface{}) (*CallToolResult, error) {
	return &CallToolResult{Content: []ToolContent{{Type: "text", Text: strings.Repeat("x", 20*1024)}}}, nil
}

// TestIssue645_McpToolImplementsCloner: mcpTool must satisfy tool.Cloner.
func TestIssue645_McpToolImplementsCloner(t *testing.T) {
	var c tool.Cloner = &mcpTool{name: "t", toolName: "t", caller: issue645Caller{}}
	if c.Clone() == nil {
		t.Fatal("Clone returned nil")
	}
}

// TestIssue645_CloneHasIndependentFill (Bug 1 core): raising the original's
// fill to 0.75 must NOT shrink the clone's result cap — before the fix there
// was no Clone at all and Registry.Clone shared the instance, so the
// teammate's results were silently truncated to 9KB by the main agent's fill.
func TestIssue645_CloneHasIndependentFill(t *testing.T) {
	base := &mcpTool{
		name:     "mcp__srv__tool",
		caller:   issue645Caller{},
		toolName: "tool",
		desc:     "d",
		schema:   json.RawMessage(`{"type":"object"}`),
		srvName:  "srv",
	}
	// Main agent runs at high fill → its own results capped at 9KB.
	base.SetContextFill(0.75)
	mainRes, err := base.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("main execute: %v", err)
	}
	if len(mainRes.Content) > 10*1024 {
		t.Fatalf("main result should be capped to ~9KB under fill 0.75, got %d", len(mainRes.Content))
	}

	// Teammate gets a clone via the registry path — its fill starts at 0 and
	// its result must use the full 50KB cap until it injects its own fill.
	clone, ok := tool.Cloner(base).Clone().(*mcpTool)
	if !ok {
		t.Fatalf("Clone returned %T, want *mcpTool", tool.Cloner(base).Clone())
	}
	if clone.name != base.name || clone.caller != toolCaller(issue645Caller{}) {
		t.Fatal("clone lost identity fields")
	}
	cloneRes, err := clone.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("clone execute: %v", err)
	}
	if len(cloneRes.Content) <= 10*1024 {
		t.Fatalf("clone result must use full 50KB cap (independent fill), got %d bytes — truncation semantics leaked from original", len(cloneRes.Content))
	}

	// Setting the clone's fill must not affect the original either.
	clone.SetContextFill(0.75)
	base.SetContextFill(0)
	baseRes, _ := base.Execute(context.Background(), json.RawMessage(`{}`))
	if len(baseRes.Content) <= 10*1024 {
		t.Fatal("original's cap must follow its own (now zero) fill, not the clone's")
	}
}

// TestIssue645_RegistryCloneGivesTeammatesOwnMCPInstances: end-to-end through
// Registry.Clone — the cloned registry must hold a DIFFERENT mcpTool instance.
func TestIssue645_RegistryCloneGivesTeammatesOwnMCPInstances(t *testing.T) {
	adapter := NewAdapter("srv", issue645Caller{}, []ToolDefinition{{Name: "tool", Description: "d"}})
	reg := tool.NewRegistry()
	if err := adapter.RegisterTools(reg); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	orig, _ := reg.Get("mcp__srv__tool")
	clonedReg := reg.Clone()
	clone, _ := clonedReg.Get("mcp__srv__tool")
	if orig == clone {
		t.Fatal("Registry.Clone must not share a mutable mcpTool instance between agents")
	}
	// Fill injected on the main agent's instance must not reach the teammate's.
	orig.(interface{ SetContextFill(float64) }).SetContextFill(0.9)
	cloneRes, err := clone.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("clone execute: %v", err)
	}
	if len(cloneRes.Content) <= 10*1024 {
		t.Fatal("teammate result truncated by main agent's fill — clone not independent")
	}
}

// TestIssue645_SetHandlerConcurrent (Bug 2, run under -race): concurrent
// setter calls plus Initialize-path reads must be race-free.
func TestIssue645_SetHandlerConcurrent(t *testing.T) {
	c := NewClient("race-srv", "true", nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(i int) { // writer
			defer wg.Done()
			c.SetSamplingHandler(func(ctx context.Context, p SamplingParams) (*SamplingResult, error) {
				return &SamplingResult{}, nil
			})
			c.SetElicitationHandler(func(ctx context.Context, p ElicitationParams) (*ElicitationResult, error) {
				return &ElicitationResult{}, nil
			})
		}(i)
		go func() { // reader (Initialize caps / dispatch gate pattern)
			defer wg.Done()
			_ = c.samplingHandlerLocked() != nil
			_ = c.elicitationHandlerLocked() != nil
		}()
	}
	wg.Wait()
}
