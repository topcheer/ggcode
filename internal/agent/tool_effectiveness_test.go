package agent

import (
	"strings"
	"testing"
)

func TestToolEffTracker_BasicTracking(t *testing.T) {
	tr := newToolEffTracker()

	// Three successful calls should not trigger guidance
	for i := 0; i < 3; i++ {
		g := tr.recordCall("grep", "some/path:42:match found", false)
		if g != "" {
			t.Fatalf("unexpected guidance on success: %s", g)
		}
	}
}

func TestToolEffTracker_AllErrorsFiresGuidance(t *testing.T) {
	tr := newToolEffTracker()

	// Three failed calls should trigger guidance
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

	// 2 success, 1 failure = 66% success rate, above threshold
	tr.recordCall("grep", "file.go:1:match", false)
	tr.recordCall("grep", "file.go:2:match", false)
	g := tr.recordCall("grep", "error: invalid pattern", true)
	if g != "" {
		t.Fatalf("should not fire guidance at 66%% success rate: %s", g)
	}
}

func TestToolEffTracker_MinSampleRequired(t *testing.T) {
	tr := newToolEffTracker()

	// Only 2 calls (below minSample of 3)
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

	// Fire guidance twice (maxFires=2)
	for i := 0; i < 6; i++ {
		tr.recordCall("grep", "error", true)
	}
	// After 2 fires, should stop producing guidance
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

	// Trigger for edit_file
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

	// Unknown tool should get generic guidance
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

	// Fill with failures to trigger guidance
	for i := 0; i < 4; i++ {
		tr.recordCall("grep", "error", true)
	}

	// Now add successes - the sliding window should eventually push out old failures
	// and stop firing guidance
	for i := 0; i < toolEffWindow; i++ {
		tr.recordCall("grep", "match found here", false)
	}

	// Window should now be all successes, guidance should not fire
	window := tr.calls["grep"]
	for _, ev := range window {
		if !ev.successful {
			t.Fatal("window should contain only successes after recovery")
		}
	}
}

func TestToolEffTracker_Summary(t *testing.T) {
	tr := newToolEffTracker()

	// No data
	if s := tr.summary(); s != "" {
		t.Fatalf("summary should be empty with no data, got: %s", s)
	}

	// Good tool
	for i := 0; i < 5; i++ {
		tr.recordCall("read_file", "file contents here", false)
	}
	// Bad tool
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

// TestIssue1208_TruncationMarkerAnchoring verifies that the MCP/plugin
// truncation-marker variants are anchored to the bracketed forms the tool
// layer actually emits. Successful read_file/run_command results whose
// CONTENT merely mentions the phrases mid-line (e.g. reading this repo's own
// source, which contains "mcp result truncated" in tool_effectiveness.go)
// must NOT be classified as poor results (#1208, regression of #358).
func TestIssue1208_TruncationMarkerAnchoring(t *testing.T) {
	// File/command content that mentions markers without the bracketed
	// advisory form - all successful, none poor.
	benign := []string{
		// source code that contains the unanchored phrases (this repo!)
		`strings.Contains(lower, "mcp result truncated") ||`,
		`// mcp resource truncated handling`,
		`# docs: output truncated at 1MB in the plugin layer`,
		// command output mentioning truncation of something else
		`archive.log: output truncated at request of sender`,
		// output_compress's agent-side redundancy marker - guidance lines
		// collapsed, output NOT degraded (must not be classified poor)
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

	// Actual tool-layer emitted forms - all poor.
	emitted := []string{
		"\n\n[... MCP result truncated: 9000 bytes total, showing first 4000 ...]",
		"\n\n[... MCP resource truncated: 9000 bytes total, showing first 4000 ...]",
		"\n...[output truncated at 1MB]",
		"... [LSP output truncated]",
		"[output truncated]",
		"[result too large]",
		"[max results reached]",
		"[... truncated: 200 lines]",
		// run_command truncateMiddle head+tail markers (run_command.go),
		// stdout and stderr variants - found in #1208 review.
		"... [42 lines omitted — output truncated, showing tail] ...",
		"STDERR:\n... [7 lines omitted — stderr truncated, showing tail] ...",
	}
	for _, content := range emitted {
		if !isPoorResult("read_file", content) {
			t.Errorf("isPoorResult(read_file, %q) = false, want true (emitted advisory)", content)
		}
	}
}

func TestToolEffTracker_PerToolIsolation(t *testing.T) {
	tr := newToolEffTracker()

	// grep fails 3 times
	for i := 0; i < 3; i++ {
		tr.recordCall("grep", "error", true)
	}

	// read_file succeeds - should not be affected by grep's failures
	g := tr.recordCall("read_file", "file contents", false)
	if g != "" {
		t.Fatalf("read_file should not get guidance from grep failures: %s", g)
	}

	// Verify per-tool tracking
	if tr.totals["grep"] != 3 {
		t.Errorf("grep total should be 3, got %d", tr.totals["grep"])
	}
	if tr.totals["read_file"] != 1 {
		t.Errorf("read_file total should be 1, got %d", tr.totals["read_file"])
	}
}

// TestAgentHasToolEffTracker asserts the Agent struct carries a wired
// toolEffTracker (issue #334: tracker was previously 100% dead code).
func TestAgentHasToolEffTracker(t *testing.T) {
	a := &Agent{toolEff: newToolEffTracker()}
	if a.toolEff == nil {
		t.Fatal("Agent.toolEff should be non-nil when initialized")
	}

	// 4 empty grep results + 1 error should fire guidance at least once
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

	// Per-user-turn reset must clear tracker state
	a.toolEff.reset()
	if len(a.toolEff.totals) != 0 || len(a.toolEff.calls) != 0 {
		t.Fatal("toolEff.reset() should clear per-tool data")
	}
}
