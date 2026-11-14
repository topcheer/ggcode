package a2aintrospect

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/a2a"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

// #5637: the PEER-side face of sub-agent live context. AttachSubagentEvents
// must fill each children-face child with the bounded event tail from the
// manager's in-memory ring, so a peer reading the children snapshot can
// watch a running sub-agent in-flight - the introspect twin of the local
// subagent_context tool.
func TestAttachSubagentEvents_FillsTailFromRing(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	defer mgr.Shutdown()
	id := mgr.Spawn("probe", "", "do something", nil, nil)

	mgr.EmitForTest(id,
		subagent.AgentEvent{Type: subagent.AgentEventText, Text: "planning the fix"},
		subagent.AgentEvent{Type: subagent.AgentEventToolCall, ToolName: "read_file", ToolArgs: `{"path":"a.go"}`},
		subagent.AgentEvent{Type: subagent.AgentEventToolResult, ToolName: "read_file", Result: "contents", IsError: false},
		subagent.AgentEvent{Type: subagent.AgentEventToolResult, ToolName: "run_command", Result: "boom", IsError: true},
	)

	subs := FromSubagents(mgr.List())
	if len(subs) != 1 {
		t.Fatalf("want 1 child, got %d", len(subs))
	}
	AttachSubagentEvents(subs, mgr, 10)

	evs := subs[0].Events
	if len(evs) != 4 {
		t.Fatalf("want 4 tail events, got %d: %+v", len(evs), evs)
	}
	want := []struct{ typ, head string }{
		{"text", "planning the fix"},
		{"tool", `read_file {"path":"a.go"}`},
		{"result", "read_file: contents"},
		{"error", "run_command: boom"},
	}
	for i, w := range want {
		if evs[i].Type != w.typ || !strings.Contains(evs[i].Head, w.head) {
			t.Errorf("event[%d] = {%s %q}, want {%s containing %q}", i, evs[i].Type, evs[i].Head, w.typ, w.head)
		}
	}
}

// Tail must be bounded: asking for 2 of 4 ring entries keeps only the
// newest 2, oldest-first.
func TestAttachSubagentEvents_BoundsTail(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	defer mgr.Shutdown()
	id := mgr.Spawn("probe", "", "t", nil, nil)

	var batch []subagent.AgentEvent
	for i := 0; i < 4; i++ {
		batch = append(batch, subagent.AgentEvent{Type: subagent.AgentEventText, Text: "e" + string(rune('0'+i))})
	}
	mgr.EmitForTest(id, batch...)

	subs := FromSubagents(mgr.List())
	AttachSubagentEvents(subs, mgr, 2)
	evs := subs[0].Events
	if len(evs) != 2 {
		t.Fatalf("want bounded tail of 2, got %d", len(evs))
	}
	if !strings.Contains(evs[0].Head, "e2") || !strings.Contains(evs[1].Head, "e3") {
		t.Errorf("want newest 2 (e2,e3) oldest-first, got %+v", evs)
	}
}

// nil source and empty rings are no-ops; unknown IDs inside the adapter
// loop are skipped silently (RecentEvents returns ok=false).
func TestAttachSubagentEvents_NilAndEmpty(t *testing.T) {
	subs := []a2a.RuntimeSubagent{{ID: "x", State: "running"}}
	AttachSubagentEvents(subs, nil, 10) // nil src: no panic, no events
	if subs[0].Events != nil {
		t.Errorf("nil source must not attach events, got %+v", subs[0].Events)
	}

	mgr := subagent.NewManager(config.SubAgentConfig{})
	defer mgr.Shutdown()
	id := mgr.Spawn("probe", "", "t", nil, nil)
	subs2 := FromSubagents(mgr.List())
	AttachSubagentEvents(subs2, mgr, 10) // ring empty: Events stays nil
	if len(subs2) != 1 || subs2[0].Events != nil {
		t.Errorf("empty ring must leave Events nil, got %+v", subs2)
	}
	_ = id
}
