package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func whyMsg(role string, blocks ...provider.ContentBlock) provider.Message {
	return provider.Message{Role: role, Content: blocks}
}

func whyThinking(s string) provider.ContentBlock {
	return provider.ContentBlock{Type: "thinking", ReasoningContent: s}
}

func whyToolUse(id, name string, input map[string]any) provider.ContentBlock {
	raw, _ := json.Marshal(input)
	return provider.ContentBlock{Type: "tool_use", ToolID: id, ToolName: name, Input: raw}
}

func whyToolResult(id, output string, isErr bool) provider.ContentBlock {
	return provider.ContentBlock{Type: "tool_result", ToolID: id, Output: output, IsError: isErr}
}

func TestExplainDecisionsPairsThinkingWithTool(t *testing.T) {
	msgs := []provider.Message{
		whyMsg("user", provider.ContentBlock{Type: "text", Text: "fix it"}),
		whyMsg("assistant",
			whyThinking("The test failure points at agent.go; I will edit the guard."),
			whyToolUse("t1", "edit_file", map[string]any{"file_path": "internal/agent/agent.go"}),
		),
		whyMsg("user", whyToolResult("t1", "Replaced 1 occurrence", false)),
	}
	out := ExplainDecisions(msgs, 5)
	if !strings.Contains(out, "edit_file internal/agent/agent.go") {
		t.Errorf("tool line missing target: %q", out)
	}
	if !strings.Contains(out, "points at agent.go") {
		t.Errorf("thinking excerpt missing: %q", out)
	}
	if !strings.Contains(out, "result: ok") {
		t.Errorf("ok result missing: %q", out)
	}
}

func TestExplainDecisionsUsesNearestPrecedingThinking(t *testing.T) {
	msgs := []provider.Message{
		whyMsg("assistant",
			whyThinking("first reasoning about exploration"),
			whyToolUse("t1", "read_file", map[string]any{"path": "a.go"}),
			whyThinking("second reasoning about the fix"),
			whyToolUse("t2", "edit_file", map[string]any{"file_path": "b.go"}),
		),
		whyMsg("user",
			whyToolResult("t1", "ok", false),
			whyToolResult("t2", "ok", false),
		),
	}
	out := ExplainDecisions(msgs, 5)
	if !strings.Contains(out, "first reasoning") {
		t.Errorf("t1 should cite first thinking: %q", out)
	}
	if !strings.Contains(out, "second reasoning") {
		t.Errorf("t2 should cite second (nearest preceding) thinking: %q", out)
	}
}

func TestExplainDecisionsCompactedMarkerNotExpanded(t *testing.T) {
	msgs := []provider.Message{
		whyMsg("assistant",
			whyThinking("[compacted: original 5000 chars]"),
			whyToolUse("t1", "edit_file", map[string]any{"file_path": "x.go"}),
		),
	}
	out := ExplainDecisions(msgs, 5)
	if !strings.Contains(out, "[compacted: original 5000 chars]") {
		t.Errorf("compacted marker should be shown verbatim: %q", out)
	}
}

func TestExplainDecisionsNoReasoningDegrades(t *testing.T) {
	msgs := []provider.Message{
		whyMsg("assistant", whyToolUse("t1", "run_command", map[string]any{"command": "go build ./..."})),
	}
	out := ExplainDecisions(msgs, 5)
	if !strings.Contains(out, "<no reasoning block recorded>") {
		t.Errorf("no-reasoning degradation missing: %q", out)
	}
	if !strings.Contains(out, "go build ./...") {
		t.Errorf("command target missing: %q", out)
	}
}

func TestExplainDecisionsEmptyWhenNoToolCalls(t *testing.T) {
	msgs := []provider.Message{
		whyMsg("user", provider.ContentBlock{Type: "text", Text: "hi"}),
		whyMsg("assistant", provider.ContentBlock{Type: "text", Text: "hello"}),
	}
	if out := ExplainDecisions(msgs, 5); out != "" {
		t.Errorf("expected empty output, got %q", out)
	}
}

func TestExplainDecisionsErrorAnchorsFromFileLine(t *testing.T) {
	msgs := []provider.Message{
		whyMsg("assistant",
			whyThinking("build it"),
			whyToolUse("t1", "run_command", map[string]any{"command": "go build"}),
		),
		whyMsg("user", whyToolResult("t1",
			"# pkg/example\n./internal/agent/agent.go:1203:5: undefined: Foo", true)),
	}
	out := ExplainDecisions(msgs, 5)
	if !strings.Contains(out, "error:") {
		t.Errorf("error outcome missing: %q", out)
	}
	if !strings.Contains(out, "internal/agent/agent.go") {
		t.Errorf("file:line anchor missing: %q", out)
	}
}

func TestExplainDecisionsRespectsCountWindow(t *testing.T) {
	var msgs []provider.Message
	for i := 0; i < 7; i++ {
		id := string(rune('a' + i))
		msgs = append(msgs,
			whyMsg("assistant", whyThinking("r"+id), whyToolUse("t"+id, "read_file", map[string]any{"path": id + ".go"})),
			whyMsg("user", whyToolResult("t"+id, "ok", false)),
		)
	}
	out := ExplainDecisions(msgs, 3)
	if got := strings.Count(out, "["); got != 3 {
		t.Errorf("expected 3 entries in window, got %d: %q", got, out)
	}
	if !strings.Contains(out, "e.go") || strings.Contains(out, "a.go") {
		t.Errorf("window should keep the LAST 3 tool messages: %q", out)
	}
}

func TestWhyCountArgClamp(t *testing.T) {
	cases := []struct {
		parts []string
		want  int
	}{
		{[]string{"/why"}, 5},
		{[]string{"/why", "3"}, 3},
		{[]string{"/why", "0"}, 1},
		{[]string{"/why", "-4"}, 1},
		{[]string{"/why", "999"}, 20},
		{[]string{"/why", "abc"}, 5},
	}
	for _, c := range cases {
		if got := WhyCountArg(c.parts); got != c.want {
			t.Errorf("WhyCountArg(%v) = %d, want %d", c.parts, got, c.want)
		}
	}
}
