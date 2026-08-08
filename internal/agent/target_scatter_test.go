package agent

import (
	"strings"
	"testing"
)

func TestTargetScatter_NoScatterWithConvergence(t *testing.T) {
	s := newTargetScatterState()
	// Read the same file 3 times, then edit - should not trigger
	s.recordToolCall("read_file", `{"path":"/foo/bar.go"}`)
	s.recordToolCall("read_file", `{"path":"/foo/bar.go"}`)
	s.recordToolCall("read_file", `{"path":"/foo/bar.go"}`)
	if msg := s.check(); msg != "" {
		t.Fatalf("expected no warning before mutation, got: %s", msg)
	}
	s.recordToolCall("edit_file", `{"file_path":"/foo/bar.go"}`)
	if msg := s.check(); msg != "" {
		t.Fatalf("expected no warning after mutation, got: %s", msg)
	}
}

func TestTargetScatter_Detected(t *testing.T) {
	s := newTargetScatterState()
	// 6 different files, all diagnostic, no mutation
	files := []string{
		`{"path":"/a/file1.go"}`,
		`{"path":"/b/file2.go"}`,
		`{"path":"/c/file3.go"}`,
		`{"path":"/d/file4.go"}`,
		`{"path":"/e/file5.go"}`,
		`{"path":"/f/file6.go"}`,
	}
	for _, f := range files {
		s.recordToolCall("read_file", f)
	}
	msg := s.check()
	if msg == "" {
		t.Fatal("expected scatter warning, got empty")
	}
	if !strings.Contains(msg, "Target Scatter") {
		t.Errorf("warning should mention 'Target Scatter', got: %s", msg)
	}
	if !strings.Contains(msg, "6 unique") {
		t.Errorf("warning should mention 6 unique targets, got: %s", msg)
	}
}

func TestTargetScatter_BelowThreshold(t *testing.T) {
	s := newTargetScatterState()
	// Only 4 unique targets (below threshold of 5)
	files := []string{
		`{"path":"/a/file1.go"}`,
		`{"path":"/b/file2.go"}`,
		`{"path":"/c/file3.go"}`,
		`{"path":"/d/file4.go"}`,
	}
	for _, f := range files {
		s.recordToolCall("read_file", f)
	}
	if msg := s.check(); msg != "" {
		t.Fatalf("expected no warning with only 4 unique targets, got: %s", msg)
	}
}

func TestTargetScatter_MutationResets(t *testing.T) {
	s := newTargetScatterState()
	// Scatter across 5 files, then edit - should reset
	files := []string{
		`{"path":"/a/file1.go"}`,
		`{"path":"/b/file2.go"}`,
		`{"path":"/c/file3.go"}`,
		`{"path":"/d/file4.go"}`,
		`{"path":"/e/file5.go"}`,
	}
	for _, f := range files {
		s.recordToolCall("read_file", f)
	}
	s.recordToolCall("edit_file", `{"file_path":"/a/file1.go"}`)
	if msg := s.check(); msg != "" {
		t.Fatalf("expected no warning after mutation resets, got: %s", msg)
	}
}

func TestTargetScatter_GrepSearchLSP(t *testing.T) {
	s := newTargetScatterState()
	// Different tool types targeting different paths
	s.recordToolCall("grep", `{"path":"/pkg1/utils.go","pattern":"foo"}`)
	s.recordToolCall("search_files", `{"directory":"/pkg2"}`)
	s.recordToolCall("lsp_hover", `{"path":"/pkg3/types.go"}`)
	s.recordToolCall("lsp_definition", `{"path":"/pkg4/main.go"}`)
	s.recordToolCall("lsp_references", `{"path":"/pkg5/handler.go"}`)
	s.recordToolCall("code_search", `{"query":"authentication logic"}`)
	msg := s.check()
	if msg == "" {
		t.Fatal("expected scatter warning with diverse tools, got empty")
	}
}

func TestTargetScatter_MaxWarns(t *testing.T) {
	s := newTargetScatterState()
	// Trigger first warning
	for i := 0; i < 6; i++ {
		s.recordToolCall("read_file", `{"path":"/dir`+string(rune('a'+i))+`/file.go"}`)
	}
	msg1 := s.check()
	if msg1 == "" {
		t.Fatal("expected first warning")
	}
	// Add more to trigger re-fire gap
	for i := 0; i < 4; i++ {
		s.recordToolCall("read_file", `{"path":"/more`+string(rune('a'+i))+`/file.go"}`)
	}
	msg2 := s.check()
	if msg2 == "" {
		t.Fatal("expected second warning after gap")
	}
	// Third should be suppressed (max 2)
	for i := 0; i < 4; i++ {
		s.recordToolCall("read_file", `{"path":"/even`+string(rune('a'+i))+`/file.go"}`)
	}
	msg3 := s.check()
	if msg3 != "" {
		t.Fatalf("expected no third warning (max warns), got: %s", msg3)
	}
}

func TestTargetScatter_MinCallsNotMet(t *testing.T) {
	s := newTargetScatterState()
	// Only 3 calls total - below minimum threshold
	s.recordToolCall("read_file", `{"path":"/a/f1.go"}`)
	s.recordToolCall("read_file", `{"path":"/b/f2.go"}`)
	s.recordToolCall("read_file", `{"path":"/c/f3.go"}`)
	if msg := s.check(); msg != "" {
		t.Fatalf("expected no warning with only 3 calls, got: %s", msg)
	}
}

func TestTargetScatter_WindowTrims(t *testing.T) {
	s := newTargetScatterState()
	// Fill beyond window size with same file repeatedly
	for i := 0; i < 10; i++ {
		s.recordToolCall("read_file", `{"path":"/same/file.go"}`)
	}
	// All same file, unique count = 1, should not trigger
	if msg := s.check(); msg != "" {
		t.Fatalf("expected no warning with same file repeated, got: %s", msg)
	}
}

func TestScatterIsDiagnostic(t *testing.T) {
	diagnostics := []string{"read_file", "grep", "search_files", "lsp_hover", "code_search"}
	for _, d := range diagnostics {
		if !scatterIsDiagnostic(d) {
			t.Errorf("%s should be diagnostic", d)
		}
	}
	nonDiagnostics := []string{"edit_file", "write_file", "run_command", "git_commit"}
	for _, d := range nonDiagnostics {
		if scatterIsDiagnostic(d) {
			t.Errorf("%s should NOT be diagnostic", d)
		}
	}
}

func TestScatterNormalizePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`{"path":"/foo/bar/baz.go"}`, "foo/bar/baz.go"},
		{`{"file":"/src/main.go"}`, "src/main.go"},
		{`{"directory":"/pkg"}`, "pkg"},
		{`/very/deep/path/to/some/file.go`, "path/to/some/file.go"},
		{`""`, ""},
		{`no-path-here`, "no-path-here"},
	}
	for _, tc := range tests {
		got := scatterNormalizePath(tc.input)
		if got != tc.want {
			t.Errorf("scatterNormalizePath(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestTargetScatter_Reset(t *testing.T) {
	s := newTargetScatterState()
	for i := 0; i < 6; i++ {
		s.recordToolCall("read_file", `{"path":"/d`+string(rune('a'+i))+`/f.go"}`)
	}
	s.reset()
	if s.totalCalls != 0 || s.warnCount != 0 || len(s.uniqueTargets) != 0 {
		t.Fatal("reset should clear all state")
	}
}
