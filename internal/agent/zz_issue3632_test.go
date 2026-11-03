package agent

import "testing"

func TestIssue3632_RunCommandPayloadMarkersNotPoor(t *testing.T) {
	cases := []string{

		"if strings.Contains(lower, \"[output truncated]\") ||\n\t\tstrings.Contains(lower, \"[result too large]\") {",

		"internal/agent/tool_effectiveness.go:167:[output truncated]",
		"search hit: [max results reached]",
		"log line mentions [... mcp result truncated: 4096 bytes] mid-sentence",
	}
	for _, c := range cases {
		if isPoorResult("run_command", c) {
			t.Fatalf("run_command payload literal miscounted as poor result: %q", c)
		}
	}

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

	if !isPoorResult("some_tool", "ok\n[output truncated]") {
		t.Fatal("generic marker branch must keep firing for other tools")
	}
}
