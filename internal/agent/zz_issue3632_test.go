package agent

// #3632 probes: run_command payloads are arbitrary command stdout. cat-ing
// or grepping this repository's own sources surfaces the bracketed
// truncation-marker literals themselves - a successful, complete lookup that
// the generic marker branch used to count as a poor result (self-referential
// false positive). run_command's only tool-layer degradation marker is
// truncateMiddle's "truncated, showing tail]" tail, which keeps firing.

import "testing"

func TestIssue3632_RunCommandPayloadMarkersNotPoor(t *testing.T) {
	cases := []string{
		// cat of this repo's own tool sources embedding the markers
		"if strings.Contains(lower, \"[output truncated]\") ||\n\t\tstrings.Contains(lower, \"[result too large]\") {",
		// rg hunting the markers during truncation debugging
		"internal/agent/tool_effectiveness.go:167:[output truncated]",
		"search hit: [max results reached]",
		"log line mentions [... mcp result truncated: 4096 bytes] mid-sentence",
	}
	for _, c := range cases {
		if isPoorResult("run_command", c) {
			t.Fatalf("run_command payload literal miscounted as poor result: %q", c)
		}
	}
	// Line-start advisory forms still count for run_command (#1208 keeps).
	if !isPoorResult("run_command", "\n\n[... MCP result truncated: 9000 bytes total, showing first 4000 ...]") {
		t.Fatal("line-start advisory must still count as poor result")
	}
	if !isPoorResult("run_command", "... [LSP output truncated]") {
		t.Fatal("ellipsis-prefixed advisory line must still count as poor result")
	}
}

func TestIssue3632_RunCommandRealTruncationStillPoor(t *testing.T) {
	if !isPoorResult("run_command", "... [37 lines omitted - output truncated, showing tail] ...") {
		t.Fatal("truncateMiddle's real degradation marker must still count as poor result")
	}
}

func TestIssue3632_OtherToolsGenericMarkersUnchanged(t *testing.T) {
	// Non-run_command tools keep the generic bracket-marker branch.
	if !isPoorResult("some_tool", "ok\n[output truncated]") {
		t.Fatal("generic marker branch must keep firing for other tools")
	}
}
