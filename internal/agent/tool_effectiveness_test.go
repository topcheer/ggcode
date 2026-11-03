package agent

import (
	"strings"
	"testing"
)

func TestToolEffTracker_BasicTracking(t *testing.T) {
	tr := newToolEffTracker()

	for i := 0; i < 3; i++ {
		g := tr.recordCall("grep", "some/path:42:match found", false)
		if g != "" {
			t.Fatalf("unexpected guidance on success: %s", g)
		}
	}
}

func TestToolEffTracker_AllErrorsFiresGuidance(t *testing.T) {
	tr := newToolEffTracker()

	var guidance string
	for i := 0; i < 3; i++ {
		guidance = tr.recordCall("grep", "error: pattern invalid", true)
	}
	if guidance == "" {
		t.Fatal("expected guidance after 3 consecutive failures")
	}
	if !strings.Contains(guidance, "grep") {
		t.Errorf("guidance should mention tool name, got: %s", guidance)
	}
	if !strings.Contains(guidance, "Tool Effectiveness") {
		t.Errorf("guidance should have header, got: %s", guidance)
	}
}

func TestToolEffTracker_EmptyResultsFireGuidance(t *testing.T) {
	tr := newToolEffTracker()

	var guidance string
	for i := 0; i < 3; i++ {
		guidance = tr.recordCall("grep", "No matches found", false)
	}
	if guidance == "" {
		t.Fatal("expected guidance after 3 empty search results")
	}
}

func TestToolEffTracker_MixedResultsNoGuidance(t *testing.T) {
	tr := newToolEffTracker()

	tr.recordCall("grep", "file.go:1:match", false)
	tr.recordCall("grep", "file.go:2:match", false)
	g := tr.recordCall("grep", "error: invalid pattern", true)
	if g != "" {
		t.Fatalf("should not fire guidance at 66%% success rate: %s", g)
	}
}

func TestToolEffTracker_MinSampleRequired(t *testing.T) {
	tr := newToolEffTracker()

	g := tr.recordCall("grep", "error", true)
	if g != "" {
		t.Fatal("should not fire before minSample")
	}
	g = tr.recordCall("grep", "error", true)
	if g != "" {
		t.Fatal("should not fire before minSample")
	}
}

func TestToolEffTracker_MaxFires(t *testing.T) {
	tr := newToolEffTracker()

	for i := 0; i < 6; i++ {
		tr.recordCall("grep", "error", true)
	}

	g := tr.recordCall("grep", "error", true)
	if g != "" {
		t.Fatalf("should stop firing after maxFires: %s", g)
	}
}

func TestToolEffTracker_Reset(t *testing.T) {
	tr := newToolEffTracker()

	for i := 0; i < 3; i++ {
		tr.recordCall("grep", "error", true)
	}
	if len(tr.totals) == 0 {
		t.Fatal("should have data before reset")
	}

	tr.reset()
	if len(tr.totals) != 0 {
		t.Fatal("reset should clear all data")
	}
	if len(tr.calls) != 0 {
		t.Fatal("reset should clear calls")
	}
}

func TestToolEffTracker_SpecificToolAlternatives(t *testing.T) {
	tr := newToolEffTracker()

	var guidance string
	for i := 0; i < 3; i++ {
		guidance = tr.recordCall("edit_file", "old_text not found in file", false)
	}
	if guidance == "" {
		t.Fatal("expected guidance for edit_file failures")
	}
	if !strings.Contains(guidance, "Re-read") && !strings.Contains(guidance, "read") {
		t.Errorf("guidance should suggest re-reading file, got: %s", guidance)
	}
}

func TestToolEffTracker_GenericFallback(t *testing.T) {
	tr := newToolEffTracker()

	var guidance string
	for i := 0; i < 3; i++ {
		guidance = tr.recordCall("custom_tool_xyz", "error: something failed", true)
	}
	if guidance == "" {
		t.Fatal("expected generic guidance for unknown tool")
	}
	if !strings.Contains(guidance, "different tool") {
		t.Errorf("generic guidance should suggest different tool, got: %s", guidance)
	}
}

func TestToolEffTracker_SlidingWindow(t *testing.T) {
	tr := newToolEffTracker()

	for i := 0; i < 4; i++ {
		tr.recordCall("grep", "error", true)
	}

	for i := 0; i < toolEffWindow; i++ {
		tr.recordCall("grep", "match found here", false)
	}

	window := tr.calls["grep"]
	for _, ev := range window {
		if !ev.successful {
			t.Fatal("window should contain only successes after recovery")
		}
	}
}

func TestToolEffTracker_Summary(t *testing.T) {
	tr := newToolEffTracker()

	if s := tr.summary(); s != "" {
		t.Fatalf("summary should be empty with no data, got: %s", s)
	}

	for i := 0; i < 5; i++ {
		tr.recordCall("read_file", "file contents here", false)
	}

	for i := 0; i < 4; i++ {
		tr.recordCall("grep", "error", true)
	}

	s := tr.summary()
	if !strings.Contains(s, "grep") {
		t.Errorf("summary should mention low-effectiveness tool, got: %s", s)
	}
	if strings.Contains(s, "read_file") {
		t.Errorf("summary should not mention high-effectiveness tool, got: %s", s)
	}
}

func TestIssue1213_SummaryCountsPoorResults(t *testing.T) {
	tr := newToolEffTracker()

	for i := 0; i < 8; i++ {
		tr.recordCall("grep", "no matches found", false)
	}

	s := tr.summary()
	if !strings.Contains(s, "grep") {
		t.Fatalf("summary must report poor-result-only tool, got: %q", s)
	}
	if !strings.Contains(s, "0/8") {
		t.Errorf("summary should show 0/8 successes, got: %s", s)
	}

	tr2 := newToolEffTracker()
	for i := 0; i < 8; i++ {
		tr2.recordCall("grep", "file.go:1:match", false)
	}
	if s2 := tr2.summary(); s2 != "" {
		t.Errorf("all-success tool should not appear in summary, got: %q", s2)
	}
}

func TestIsPoorResult(t *testing.T) {
	tests := []struct {
		tool    string
		content string
		want    bool
	}{
		{"grep", "No matches found", true},
		{"grep", "file.go:1:matched text", false},
		{"edit_file", "old_text not found in file", true},
		{"edit_file", "successfully edited", false},
		{"run_command", "Output truncated, too many lines", true},
		{"read_file", "file contents", false},
		{"", "", false},
	}

	for _, tt := range tests {
		got := isPoorResult(tt.tool, tt.content)
		if got != tt.want {
			t.Errorf("isPoorResult(%q, %q) = %v, want %v", tt.tool, tt.content, got, tt.want)
		}
	}
}

func TestIssue1211_EditRejectionPhrasesScopedToEditTools(t *testing.T) {

	benign := []string{
		`strings.Contains(lower, "old_text not found") ||`,
		`// CHANGELOG: fixed "no changes applied" bug (#999)`,
		`docs: no modifications were needed for this release`,
		`error output says: file already exists; use edit (historical log)`,
	}
	for _, content := range benign {

		for _, tool := range []string{"read_file", "run_command", "multi_file_read"} {
			if isPoorResult(tool, content) {
				t.Errorf("isPoorResult(%q, %q) = true, want false (payload mention)", tool, content)
			}
		}
	}

	for _, content := range []string{
		"old_text not found in /path/file.go",
		"no changes applied: old_text matches existing content",
		"no modifications were made",
		"file already exists; use edit_file instead",
	} {
		for _, tool := range []string{"edit_file", "multi_edit_file", "write_file"} {
			if !isPoorResult(tool, content) {
				t.Errorf("isPoorResult(%q, %q) = false, want true (edit rejection)", tool, content)
			}
		}
	}
}

func TestToolEffTracker_TruncationAdvisory(t *testing.T) {
	tr := newToolEffTracker()

	var guidance string
	for i := 0; i < 3; i++ {
		guidance = tr.recordCall("run_command", "Output truncated: too many lines to display", false)
	}
	if guidance == "" {
		t.Fatal("expected guidance for truncated output pattern")
	}
}

func TestIssue1208_TruncationMarkerAnchoring(t *testing.T) {

	benign := []string{

		`strings.Contains(lower, "mcp result truncated") ||`,
		`// mcp resource truncated handling`,
		`# docs: output truncated at 1MB in the plugin layer`,

		`archive.log: output truncated at request of sender`,

		`[3 similar lines omitted]`,
	}
	for _, content := range benign {
		if isPoorResult("read_file", content) {
			t.Errorf("isPoorResult(read_file, %q) = true, want false (unanchored mention)", content)
		}
		if isPoorResult("run_command", content) {
			t.Errorf("isPoorResult(run_command, %q) = true, want false (unanchored mention)", content)
		}
	}

	emitted := []string{
		"\n\n[... MCP result truncated: 9000 bytes total, showing first 4000 ...]",
		"\n\n[... MCP resource truncated: 9000 bytes total, showing first 4000 ...]",
		"\n...[output truncated at 1MB]",
		"... [LSP output truncated]",
		"[output truncated]",
		"[result too large]",
		"[max results reached]",
		"[... truncated: 200 lines]",

		"... [42 lines omitted — output truncated, showing tail] ...",
		"STDERR:\n... [7 lines omitted — stderr truncated, showing tail] ...",
	}

	probeTools := []string{"run_command", "mcp__probe__tool", "lsp_hover"}
	for _, content := range emitted {
		for _, tool := range probeTools {
			if !isPoorResult(tool, content) {
				t.Errorf("isPoorResult(%q, %q) = false, want true (emitted advisory)", tool, content)
			}
		}

		for _, tool := range []string{"read_file", "multi_file_read"} {
			if isPoorResult(tool, content) {
				t.Errorf("isPoorResult(%q, %q) = true, want false (#2964 file payload)", tool, content)
			}
		}
	}
}

func TestToolEffTracker_PerToolIsolation(t *testing.T) {
	tr := newToolEffTracker()

	for i := 0; i < 3; i++ {
		tr.recordCall("grep", "error", true)
	}

	g := tr.recordCall("read_file", "file contents", false)
	if g != "" {
		t.Fatalf("read_file should not get guidance from grep failures: %s", g)
	}

	if tr.totals["grep"] != 3 {
		t.Errorf("grep total should be 3, got %d", tr.totals["grep"])
	}
	if tr.totals["read_file"] != 1 {
		t.Errorf("read_file total should be 1, got %d", tr.totals["read_file"])
	}
}

func TestAgentHasToolEffTracker(t *testing.T) {
	a := &Agent{toolEff: newToolEffTracker()}
	if a.toolEff == nil {
		t.Fatal("Agent.toolEff should be non-nil when initialized")
	}

	fired := false
	for i := 0; i < 4; i++ {
		if g := a.toolEff.recordCall("grep", "No matches found", false); g != "" {
			fired = true
		}
	}
	if g := a.toolEff.recordCall("grep", "error: invalid pattern", true); g != "" {
		fired = true
	}
	if !fired {
		t.Fatal("expected tool effectiveness guidance after 4 empty + 1 error grep calls via Agent.toolEff")
	}

	a.toolEff.reset()
	if len(a.toolEff.totals) != 0 || len(a.toolEff.calls) != 0 {
		t.Fatal("toolEff.reset() should clear per-tool data")
	}
}
