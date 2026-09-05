package agent

import (
	"fmt"
	"testing"
)

func TestClassifyDegraded(t *testing.T) {
	tests := []struct {
		name    string
		tool    string
		content string
		want    degradedKind
	}{
		{"empty content on content tool", "read_file", "", degradedEmpty},
		{"whitespace only", "read_file", "   \n\t  ", degradedEmpty},
		{"null value", "run_command", "null", degradedNullish},
		{"nil value", "some_tool", "nil", degradedNullish},
		{"none value", "some_tool", "none", degradedNullish},
		{"undefined value", "some_tool", "undefined", degradedNullish},
		{"empty json object", "some_tool", "{}", degradedNullish},
		{"empty json array", "some_tool", "[]", degradedNullish},
		// #1457-A: a footer WITH pagination guidance is the tool's designed
		// paging signal, not degradation - the old expectation codified the
		// contradiction (2-per-run budget burned on normal paging reads).
		{"guided pagination footer is NOT degraded", "read_file", "     1\tpackage main\n[File truncated: showing lines 1-1 of 50. Use read_file with offset/limit for more.]", degradedNone},
		{"guided pagination multi_file_read", "multi_file_read", "[showing lines 1-10 of 200. Use offset/limit]", degradedNone},
		{"output too large footer", "grep", "[output too large, showing first 10]", degradedTruncated},
		{"#339 source containing 'output truncated' literal is NOT truncated", "read_file", "\"\"\"\n     3\ttruncated = truncateUTF8Safe(trimmed, maxOutputSize) + \"\\n... [output truncated]\"\"\n\"\"\"", degradedNone},
		{"#339 inline 'truncated' in code comment is NOT truncated", "grep", "main.go:42: // handle truncated output gracefully", degradedNone},
		{"no results found", "search_files", "no results found", degradedNoResult},
		{"no matches", "grep", "0 matches", degradedNoResult},
		{"no such file", "read_file", "no such file or directory", degradedNoResult},
		{"file not found", "read_file", "Error: file not found", degradedNoResult},
		{"no symbols", "lsp_workspace_symbols", "no symbols found", degradedNoResult},
		{"#339 glob returning single short filename is NOT degraded", "glob", "a.go", degradedNone},
		{"#339 grep files_with_matches single short path is NOT degraded", "grep", "a.go", degradedNone},
		{"short content on content tool", "read_file", "hello", degradedEmpty},
		{"valid content - normal", "read_file", "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n", degradedNone},
		{"valid content - normal short non-content tool", "run_command", "ok", degradedNone},
		{"valid content - substantial", "grep", "found 5 matches in 3 files:\nfile1.go:10:match1\nfile1.go:25:match2", degradedNone},
		{"nullish not exact - longer text", "read_file", "null pointer dereference occurred at runtime", degradedNone},
		// #143: "not found" in legitimate code content must NOT be degradedNoResult.
		{"#143 grep returns error-handling code with 'not found'", "grep", "main.go:42:  return fmt.Errorf(\"record not found: %w\", err)", degradedNone},
		{"#143 read_file reads file with error definitions", "read_file", "// ErrNotFound is returned when key not found in cache\nvar ErrNotFound = errors.New(\"not found\")", degradedNone},
		{"#143 run_command output mentions 'not found' in test", "run_command", "PASS (ok: github.com/pkg/errors)\nok github.com/myproject/handler 0.123s\n  handler.go: \"not found\" error path tested", degradedNone},
		{"#143 grep returns 'file not found' in code", "grep", "config.go:15:  return errors.New(\"file not found: \" + path)", degradedNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyDegraded(tt.tool, tt.content)
			if got != tt.want {
				t.Errorf("classifyDegraded(%q, %q) = %v, want %v", tt.tool, tt.content, got, tt.want)
			}
		})
	}
}

func TestErrorPropagateDetection(t *testing.T) {
	s := newErrorPropagateState()

	// Step 1: a degraded output (empty result on a read_file that "succeeded").
	g := s.recordResult("read_file", "", false)
	if g != "" {
		t.Fatalf("first degraded output should not immediately warn, got: %s", g)
	}

	// Step 2: subsequent tool call (potential downstream consumer).
	g = s.recordResult("grep", "some valid content here that is long enough", false)
	if g != "" {
		t.Fatalf("only 1 downstream call should not trigger yet, got: %s", g)
	}

	// Step 3: another subsequent call -- now threshold reached.
	g = s.recordResult("edit_file", "edited successfully", false)
	if g == "" {
		t.Fatal("should trigger propagation warning after 2 downstream calls")
	}
	if !contains(g, "Error Propagation Chain") {
		t.Errorf("guidance should mention propagation chain, got: %s", g)
	}
	if !contains(g, "read_file") {
		t.Errorf("guidance should mention origin tool, got: %s", g)
	}
	if !contains(g, "empty") {
		t.Errorf("guidance should mention degraded kind, got: %s", g)
	}
}

func TestErrorPropagateNoFalsePositive(t *testing.T) {
	s := newErrorPropagateState()

	// Normal valid outputs should never trigger.
	for i := 0; i < 10; i++ {
		g := s.recordResult("read_file", "package main\n\nfunc main() {}\n", false)
		if g != "" {
			t.Fatalf("valid outputs should not trigger propagation warning, got: %s", g)
		}
	}
}

func TestErrorPropagateExplicitErrorIgnored(t *testing.T) {
	s := newErrorPropagateState()

	// Explicit errors (IsError=true) are handled by error_cascade.go,
	// not this detector. Should not create a chain.
	g := s.recordResult("read_file", "", true) // isError = true
	if g != "" {
		t.Fatalf("explicit errors should not trigger propagation chain, got: %s", g)
	}

	// Even with downstream calls, no chain exists.
	g = s.recordResult("grep", "valid content", false)
	if g != "" {
		t.Fatalf("no chain should exist from explicit errors, got: %s", g)
	}
	g = s.recordResult("grep", "more content", false)
	if g != "" {
		t.Fatalf("no chain should exist from explicit errors, got: %s", g)
	}
}

func TestErrorPropagateMaxWarnings(t *testing.T) {
	s := newErrorPropagateState()

	// Create first degraded chain and trigger it.
	s.recordResult("read_file", "", false)
	s.recordResult("edit_file", "ok", false)
	g1 := s.recordResult("edit_file", "ok", false)
	if g1 == "" {
		t.Fatal("first chain should trigger")
	}

	// Create second degraded chain and trigger it.
	s.recordResult("read_file", "null", false)
	s.recordResult("edit_file", "ok", false)
	g2 := s.recordResult("edit_file", "ok", false)
	if g2 == "" {
		t.Fatal("second chain should trigger")
	}

	// Third degraded chain should NOT trigger (max 2 warnings per run).
	s.recordResult("read_file", "", false)
	s.recordResult("edit_file", "ok", false)
	g3 := s.recordResult("edit_file", "ok", false)
	if g3 != "" {
		t.Fatalf("third chain should not trigger (max warnings reached), got: %s", g3)
	}
}

func TestErrorPropagateReset(t *testing.T) {
	s := newErrorPropagateState()

	s.recordResult("read_file", "", false)
	s.recordResult("grep", "valid", false)
	s.recordResult("edit_file", "ok", false)

	s.reset()
	if len(s.chains) != 0 {
		t.Errorf("after reset, chains should be empty, got %d", len(s.chains))
	}
	if s.totalSteps != 0 {
		t.Errorf("after reset, totalSteps should be 0, got %d", s.totalSteps)
	}
	if s.warningsFired != 0 {
		t.Errorf("after reset, warningsFired should be 0, got %d", s.warningsFired)
	}
}

func TestErrorPropagateNullishDegraded(t *testing.T) {
	s := newErrorPropagateState()

	// A null return that is not an error.
	g := s.recordResult("run_command", "null", false)
	if g != "" {
		t.Fatalf("immediate nullish should not warn yet, got: %s", g)
	}

	g = s.recordResult("grep", "some valid data here", false)
	if g != "" {
		t.Fatalf("1 downstream should not warn, got: %s", g)
	}

	g = s.recordResult("edit_file", "done", false)
	if g == "" {
		t.Fatal("should warn after nullish + 2 downstream")
	}
	if !contains(g, "nullish") {
		t.Errorf("should mention nullish kind, got: %s", g)
	}
}

func TestErrorPropagateTruncatedDegraded(t *testing.T) {
	s := newErrorPropagateState()

	s.recordResult("grep", "file1.go\n[File truncated: showing lines 1-1 of 100]", false)
	s.recordResult("read_file", "valid content", false)
	g := s.recordResult("read_file", "more valid content", false)

	if g == "" {
		t.Fatal("should warn after truncated + 2 downstream")
	}
	if !contains(g, "may have consumed") {
		t.Errorf("guidance should use 'may have consumed it' wording, got: %s", g)
	}
	if !contains(g, "step 1") {
		t.Errorf("guidance should include origin.step locator, got: %s", g)
	}
}

func TestErrorPropagateNoResultDegraded(t *testing.T) {
	s := newErrorPropagateState()

	s.recordResult("search_files", "no results found", false)
	s.recordResult("read_file", "valid content", false)
	g := s.recordResult("read_file", "more valid content", false)

	if g == "" {
		t.Fatal("should warn after no-result + 2 downstream")
	}
	if !contains(g, "no-result") {
		t.Errorf("should mention no-result kind, got: %s", g)
	}
}

func TestDegradedKindString(t *testing.T) {
	tests := []struct {
		kind degradedKind
		want string
	}{
		{degradedNone, "none"},
		{degradedEmpty, "empty"},
		{degradedNullish, "nullish"},
		{degradedTruncated, "truncated"},
		{degradedNoResult, "no-result"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("degradedKind(%d).String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestIsContentTool(t *testing.T) {
	contentTools := []string{
		"read_file", "multi_file_read", "grep", "search_files", "code_search",
		"lsp_references", "lsp_definition", "lsp_workspace_symbols",
		"lsp_implementation", "web_search", "web_fetch", "glob",
	}
	for _, tool := range contentTools {
		if !isContentTool(tool) {
			t.Errorf("isContentTool(%q) = false, want true", tool)
		}
	}
	nonContent := []string{"edit_file", "write_file", "run_command", "git_commit", "todo_write"}
	for _, tool := range nonContent {
		if isContentTool(tool) {
			t.Errorf("isContentTool(%q) = true, want false", tool)
		}
	}
}

// contains and containsStr are defined in reflection_test.go

// TestIssue1554C_HeadTailMarkerDetected pins #1554-C: the EXACT marker
// guardToolOutput writes must be classified as a truncation footer.
func TestIssue1554C_HeadTailMarkerDetected(t *testing.T) {
	marker := fmt.Sprintf("\n\n%s %s total, showing head + tail ...]\n\n", toolHeadTailTruncationPrefix, "1.2 MB")
	if !hasTruncationFooter(marker) {
		t.Fatal("guardToolOutput's own head+tail marker must be detected as a truncation footer")
	}
	if !hasTruncationFooter("some output\n" + marker + "more output") {
		t.Fatal("marker embedded mid-output must be detected (line-anchored)")
	}
}
