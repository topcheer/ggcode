package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue4620_CountersPastSkipGates(t *testing.T) {
	data, err := readAgentAndProcessingSources()
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	redactGate := strings.Index(text, "a.runToolNames = append(a.runToolNames, tc.Name)")
	if redactGate < 0 {
		t.Fatal("runToolNames append not found")
	}
	for _, marker := range []string{
		"runStats.recordToolCall(tc.Name)",
		"a.toolCallBudget.record()",
		"a.toolThermal.recordToolCall(tc.Name)",
		"a.iterPressure.recordToolCall(tc.Name, iter+1)",
		"extractPathsFromToolCall(tc.Name, tc.Arguments, runStats)",
	} {
		idx := strings.Index(text, marker)
		if idx < 0 {
			t.Fatalf("counter %q not found", marker)
		}
		if idx < redactGate {
			t.Fatalf("counter %q sits before the post-gate runToolNames append (#4620)", marker)
		}
	}

	for _, marker := range []string{"runStats.recordToolCall(tc.Name)", "a.toolCallBudget.record()"} {
		if strings.Count(text, marker) != 1 {
			t.Fatalf("expected exactly one %q, found %d", marker, strings.Count(text, marker))
		}
	}
}

func TestIssue4621_ServerToolBlocksCountAsContent(t *testing.T) {
	if hasNonThinkingContent([]provider.ContentBlock{
		{Type: "thinking", Text: "reasoning"},
		{Type: "server_tool_use", Text: ""},
	}) {
		return
	}
	t.Fatal("server_tool_use block must count as non-thinking content (#4621)")
}

func TestIssue4621_ServerToolResultBlocksCountAsContent(t *testing.T) {
	for _, typ := range []string{
		"web_search_tool_result", "web_fetch_tool_result",
		"tool_search_tool_result", "code_execution_tool_result",
	} {
		if !hasNonThinkingContent([]provider.ContentBlock{{Type: typ}}) {
			t.Fatalf("%s block must count as non-thinking content (#4621)", typ)
		}
	}
}

func TestIssue4621_ThinkingOnlyStillCountsAsEmpty(t *testing.T) {
	if hasNonThinkingContent([]provider.ContentBlock{
		{Type: "thinking", Text: "hmm"},
		{Type: "redacted_thinking"},
	}) {
		t.Fatal("thinking-only reply must still count as empty (#4392 contract preserved)")
	}
}
