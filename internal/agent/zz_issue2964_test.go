package agent

import (
	"strings"
	"testing"
)

// zz_issue2964_test.go - regression probes for #2964: isPoorResult's
// truncation-marker branch applied full-text Contains to ALL non-search tools,
// so read_file on a file whose CONTENT contains a complete marker literal
// (this repo's own tool sources embed them, e.g. git_show.go's
// `+ "\n... [output truncated]"` line) was counted as a failed call. Three
// such reads tripped the effectiveness window (0/3 <= threshold) and fired a
// bogus "read_file has only 0% success rate" guidance, plus polluted summary().
//
// Fix: filePayloadTools (read_file, multi_file_read) results are raw file
// payload - their marker branch is skipped entirely (mirrors #1211's
// edit-rejection scoping).

// TestIssue2964FilePayloadToolsExemptFromMarkers pins the fix at the
// isPoorResult level: every marker literal, including the COMPLETE bracketed
// emitted-advisory forms, is payload when it arrives via a file-payload tool.
func TestIssue2964FilePayloadToolsExemptFromMarkers(t *testing.T) {
	// Contents copied from this repository's own sources - the exact
	// dogfooding scenario in the issue.
	payloads := []string{
		// internal/tool/git_show.go:99 / git_blame.go:88 (verbatim source line)
		"\ttrimmed = truncateUTF8Safe(trimmed, maxOutputSize) + \"\\n... [output truncated]\"",
		// internal/tool/run_command.go:499 (source line, full marker literal)
		"\treturn head + fmt.Sprintf(\"\\n... [%d lines omitted - %s truncated, showing tail] ...\\n\", omittedLines, label) + tail",
		// mid-file mentions of every other bracketed form
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

// TestIssue2964MarkerToolsStillDetected guards against over-scoping: the
// tools that genuinely append these markers keep full-text detection.
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

// TestIssue2964TrackerEndToEnd reproduces the issue's exact scenario: three
// consecutive successful read_file calls whose content embeds marker
// literals must NOT fire effectiveness guidance nor pollute the summary.
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
	// All three must count as successful: summary must show no failures and
	// no success-rate warning for read_file.
	s := tr.summary()
	if strings.Contains(s, "read_file") {
		t.Errorf("summary() mentions read_file after 3 successful reads:\n%s", s)
	}
}
