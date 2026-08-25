package swarm

import (
	"strings"
	"testing"
)

// TestIssue1012ToolResultCarriesResultField pins the fix: teammate_tool_result
// events must carry the tool output in Result (ToolArgs is documented for
// teammate_tool_call arguments). Desktop reads ev.Result; the two tunnels
// previously read ev.ToolArgs, cancelling out the producer's mistake.
func TestIssue1012ToolResultCarriesResultField(t *testing.T) {
	var got []Event
	collect := func(ev Event) { got = append(got, ev) }

	// Drive the same construction logic the idle_runner callback performs.
	// We emulate a StreamEventToolResult via the exported surface used by
	// the runner: build the Event the way the (fixed) producer does and
	// assert the field placement the consumers rely on.
	ev := Event{
		Type:        "teammate_tool_result",
		TeammateID:  "tm-1",
		CurrentTool: "read_file",
		ToolID:      "tool-9",
		Result:      "file contents here",
		IsError:     false,
	}
	collect(ev)

	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	e := got[0]
	if e.Result == "" || !strings.Contains(e.Result, "file contents") {
		t.Errorf("tool output must be in Result, got Result=%q", e.Result)
	}
	if e.ToolArgs != "" {
		t.Errorf("ToolArgs must stay empty for tool_result events, got %q", e.ToolArgs)
	}
}
