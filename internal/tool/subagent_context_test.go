package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

// #5637: the main agent must read a running sub-agent's live context
// in-flight. Pin the tool contract: header + recent events from the ring,
// unknown-ID error, and the manager-side RecentEvents clamp.
func TestSubagentContext_UnknownID(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	sc := SubagentContextTool{Manager: mgr}
	res, err := sc.Execute(context.Background(), json.RawMessage(`{"agent_id":"agent-999","description":"t"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "unknown agent run") {
		t.Errorf("expected unknown-run error, got: %+v", res)
	}
}

func TestSubagentContext_MissingID(t *testing.T) {
	sc := SubagentContextTool{Manager: subagent.NewManager(config.SubAgentConfig{})}
	res, err := sc.Execute(context.Background(), json.RawMessage(`{"description":"t"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("expected error for missing agent_id")
	}
}

func TestSubagentContext_LiveRunHeaderAndEvents(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	defer mgr.Shutdown()
	id := mgr.Spawn("probe", "", "do something", nil, context.Background())

	// Feed the event ring directly: text + tool call + result.
	mgr.EmitForTest(id,
		subagent.AgentEvent{Type: subagent.AgentEventText, Text: "thinking about the plan"},
		subagent.AgentEvent{Type: subagent.AgentEventToolCall, ToolName: "read_file", ToolArgs: `{"path":"x.go"}`},
		subagent.AgentEvent{Type: subagent.AgentEventToolResult, ToolName: "read_file", Result: "file contents here", IsError: false},
	)

	sc := SubagentContextTool{Manager: mgr}
	res, err := sc.Execute(context.Background(), json.RawMessage(`{"agent_id":"`+id+`","tail":10,"description":"t"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	for _, want := range []string{"status=", "thinking about the plan", "[tool] read_file", "[result] read_file: file contents here"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("output missing %q; got:\n%s", want, res.Content)
		}
	}
}

func TestManagerRecentEvents_Clamp(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	defer mgr.Shutdown()
	id := mgr.Spawn("probe2", "", "t", nil, context.Background())
	evs := make([]subagent.AgentEvent, 30)
	for i := range evs {
		evs[i] = subagent.AgentEvent{Type: subagent.AgentEventText, Text: "e" + string(rune('a'+i%26))}
	}
	mgr.EmitForTest(id, evs...)

	got, ok := mgr.RecentEvents(id, 5)
	if !ok || len(got) != 5 {
		t.Fatalf("want 5 events, got %d (ok=%v)", len(got), ok)
	}
	all, _ := mgr.RecentEvents(id, 0) // 0 => default 20
	if len(all) != 20 {
		t.Fatalf("n=0 should apply the default 20, got %d", len(all))
	}
	full, _ := mgr.RecentEvents(id, 400)
	if len(full) != 30 {
		t.Fatalf("n=400 should return all 30, got %d", len(full))
	}
}
