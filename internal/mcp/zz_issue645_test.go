package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

type issue645Caller struct{}

func (issue645Caller) CallTool(ctx context.Context, name string, args map[string]interface{}) (*CallToolResult, error) {
	return &CallToolResult{Content: []ToolContent{{Type: "text", Text: strings.Repeat("x", 20*1024)}}}, nil
}

func TestIssue645_McpToolImplementsCloner(t *testing.T) {
	var c tool.Cloner = &mcpTool{name: "t", toolName: "t", caller: issue645Caller{}}
	if c.Clone() == nil {
		t.Fatal("Clone returned nil")
	}
}

func TestIssue645_CloneHasIndependentFill(t *testing.T) {
	base := &mcpTool{
		name:     "mcp__srv__tool",
		caller:   issue645Caller{},
		toolName: "tool",
		desc:     "d",
		schema:   json.RawMessage(`{"type":"object"}`),
		srvName:  "srv",
	}

	base.SetContextFill(0.75)
	mainRes, err := base.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("main execute: %v", err)
	}
	if len(mainRes.Content) > 10*1024 {
		t.Fatalf("main result should be capped to ~9KB under fill 0.75, got %d", len(mainRes.Content))
	}

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

	clone.SetContextFill(0.75)
	base.SetContextFill(0)
	baseRes, _ := base.Execute(context.Background(), json.RawMessage(`{}`))
	if len(baseRes.Content) <= 10*1024 {
		t.Fatal("original's cap must follow its own (now zero) fill, not the clone's")
	}
}

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

	orig.(interface{ SetContextFill(float64) }).SetContextFill(0.9)
	cloneRes, err := clone.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("clone execute: %v", err)
	}
	if len(cloneRes.Content) <= 10*1024 {
		t.Fatal("teammate result truncated by main agent's fill — clone not independent")
	}
}

func TestIssue645_SetHandlerConcurrent(t *testing.T) {
	c := NewClient("race-srv", "true", nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			c.SetSamplingHandler(func(ctx context.Context, p SamplingParams) (*SamplingResult, error) {
				return &SamplingResult{}, nil
			})
			c.SetElicitationHandler(func(ctx context.Context, p ElicitationParams) (*ElicitationResult, error) {
				return &ElicitationResult{}, nil
			})
		}(i)
		go func() {
			defer wg.Done()
			_ = c.samplingHandlerLocked() != nil
			_ = c.elicitationHandlerLocked() != nil
		}()
	}
	wg.Wait()
}
