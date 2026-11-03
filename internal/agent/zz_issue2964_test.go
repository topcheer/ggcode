package agent

import (
	"strings"
	"testing"
)

func TestIssue2964FilePayloadToolsExemptFromMarkers(t *testing.T) {

	payloads := []string{

		"\ttrimmed = truncateUTF8Safe(trimmed, maxOutputSize) + \"\\n... [output truncated]\"",

		"\treturn head + fmt.Sprintf(\"\\n... [%d lines omitted - %s truncated, showing tail] ...\\n\", omittedLines, label) + tail",

		"[result too large]",
		"[max results reached]",
		"... [LSP output truncated]",
		"\n\n[... MCP result truncated: 9000 bytes total, showing first 4000 ...]",
		"\n\n[... MCP resource truncated: 9000 bytes total, showing first 4000 ...]",
		"[... truncated: 200 lines]",
	}
	for _, tool := range []string{"read_file", "multi_file_read"} {
		for _, content := range payloads {
			if isPoorResult(tool, content) {
				t.Errorf("isPoorResult(%q, %q) = true, want false (#2964: marker literal in file payload)", tool, content)
			}
		}
	}
}

func TestIssue2964MarkerToolsStillDetected(t *testing.T) {
	if !isPoorResult("lsp_hover", "hover info\n... [LSP output truncated]") {
		t.Error("isPoorResult(lsp_hover, truncated suffix) = false, want true")
	}
	if !isPoorResult("run_command", "head\n... [42 lines omitted - output truncated, showing tail] ...\ntail") {
		t.Error("isPoorResult(run_command, showing-tail marker) = false, want true")
	}
	if !isPoorResult("mcp__probe__tool", "\n\n[... MCP result truncated: 9000 bytes total, showing first 4000 ...]") {
		t.Error("isPoorResult(mcp__probe__tool, MCP marker) = false, want true")
	}
}

func TestIssue2964TrackerEndToEnd(t *testing.T) {
	tr := newToolEffTracker()
	for i := 0; i < 3; i++ {
		content := strings.Repeat("line of real file content\n", 10) +
			"\ttrimmed = truncateUTF8Safe(trimmed, maxOutputSize) + \"\\n... [output truncated]\""
		guidance := tr.recordCall("read_file", content, false)
		if guidance != "" {
			t.Fatalf("call %d: guidance fired for successful read_file: %q", i+1, guidance)
		}
	}

	s := tr.summary()
	if strings.Contains(s, "read_file") {
		t.Errorf("summary() mentions read_file after 3 successful reads:\n%s", s)
	}
}
