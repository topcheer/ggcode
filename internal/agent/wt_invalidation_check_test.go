package agent

import (
	"strings"
	"testing"
)

func TestWTInvalidation_BasicDetection(t *testing.T) {
	w := newWTInvalidationState()

	// Read 3 files
	w.recordRead("internal/agent/agent.go")
	w.recordRead("internal/agent/expired_read_check.go")
	w.recordRead("internal/agent/tool_target_mismatch.go")

	msg := w.checkMutation("git_checkout", `{"branch":"feature"}`)
	if msg == "" {
		t.Fatal("expected warning message for git_checkout after 3 reads")
	}
	if !strings.Contains(msg, "Working-Tree Invalidation") {
		t.Errorf("expected message to contain warning header, got: %s", msg)
	}
	if !strings.Contains(msg, "git_checkout") {
		t.Errorf("expected message to mention tool name, got: %s", msg)
	}
	if !strings.Contains(msg, "3 file(s)") {
		t.Errorf("expected message to mention 3 files, got: %s", msg)
	}
}

func TestWTInvalidation_TooFewReads(t *testing.T) {
	w := newWTInvalidationState()

	// Only 1 read -- below threshold
	w.recordRead("file.go")

	msg := w.checkMutation("git_checkout", `{}`)
	if msg != "" {
		t.Errorf("expected no warning with only 1 read, got: %s", msg)
	}
}

func TestWTInvalidation_NoReads(t *testing.T) {
	w := newWTInvalidationState()

	msg := w.checkMutation("git_checkout", `{}`)
	if msg != "" {
		t.Errorf("expected no warning with 0 reads, got: %s", msg)
	}
}

func TestWTInvalidation_NonMutatingTool(t *testing.T) {
	w := newWTInvalidationState()

	w.recordRead("file1.go")
	w.recordRead("file2.go")
	w.recordRead("file3.go")

	if isWTMutatingTool("read_file") {
		t.Error("read_file should not be a WT-mutating tool")
	}
	if isWTMutatingTool("edit_file") {
		t.Error("edit_file should not be a WT-mutating tool")
	}
	if isWTMutatingTool("run_command") {
		t.Error("run_command should not be classified as WT-mutating (not a git op)")
	}
}

func TestWTInvalidation_MutatingTools(t *testing.T) {
	tools := []string{
		"git_checkout", "git_reset", "git_stash",
		"git_pull", "git_revert", "git_merge",
		"git_rebase", "git_cherry_pick",
	}
	for _, tool := range tools {
		if !isWTMutatingTool(tool) {
			t.Errorf("%s should be classified as WT-mutating", tool)
		}
	}
}

func TestWTInvalidation_MaxWarnings(t *testing.T) {
	w := newWTInvalidationState()

	// First batch of reads + mutation
	w.recordRead("file1.go")
	w.recordRead("file2.go")
	msg1 := w.checkMutation("git_checkout", `{}`)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	// More reads + second mutation
	w.recordRead("file3.go")
	w.recordRead("file4.go")
	msg2 := w.checkMutation("git_reset", `{}`)
	if msg2 == "" {
		t.Fatal("expected second warning")
	}

	// Third mutation should be suppressed
	w.recordRead("file5.go")
	msg3 := w.checkMutation("git_stash", `{}`)
	if msg3 != "" {
		t.Errorf("expected no third warning (max reached), got: %s", msg3)
	}
}

func TestWTInvalidation_NoRepeatWithoutNewReads(t *testing.T) {
	w := newWTInvalidationState()

	w.recordRead("file1.go")
	w.recordRead("file2.go")

	msg1 := w.checkMutation("git_checkout", `{}`)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	// Same mutation again without new reads -- should not repeat
	msg2 := w.checkMutation("git_reset", `{}`)
	if msg2 != "" {
		t.Errorf("expected no repeat warning without new reads, got: %s", msg2)
	}
}

func TestWTInvalidation_Reset(t *testing.T) {
	w := newWTInvalidationState()

	w.recordRead("file1.go")
	w.recordRead("file2.go")
	_ = w.checkMutation("git_checkout", `{}`)

	w.reset()

	if len(w.readFiles) != 0 {
		t.Errorf("expected readFiles cleared after reset, got %d", len(w.readFiles))
	}
	if w.warnedCount != 0 {
		t.Errorf("expected warnedCount 0 after reset, got %d", w.warnedCount)
	}
}

func TestWTInvalidation_NormalizePath(t *testing.T) {
	tests := []struct {
		input  string
		output string
	}{
		{"./internal/agent/foo.go", "internal/agent/foo.go"},
		{"internal/agent/foo.go", "internal/agent/foo.go"},
		{"", ""},
		{"  file.go  ", "file.go"},
	}
	for _, tc := range tests {
		got := normalizeWTPath(tc.input)
		if got != tc.output {
			t.Errorf("normalizeWTPath(%q) = %q, want %q", tc.input, got, tc.output)
		}
	}
}

func TestWTInvalidation_DeduplicateReads(t *testing.T) {
	w := newWTInvalidationState()

	// Same file read multiple times -- should only count once
	w.recordRead("file.go")
	w.recordRead("file.go")
	w.recordRead("file.go")

	if len(w.readFiles) != 1 {
		t.Errorf("expected 1 unique read, got %d", len(w.readFiles))
	}
}

func TestWTInvalidation_PathListTruncation(t *testing.T) {
	w := newWTInvalidationState()

	// Read 5 files
	for i := 0; i < 5; i++ {
		w.recordRead("file" + string(rune('0'+i)) + ".go")
	}

	msg := w.checkMutation("git_checkout", `{}`)
	if msg == "" {
		t.Fatal("expected warning message")
	}
	if !strings.Contains(msg, "and 2 more") {
		t.Errorf("expected truncation suffix 'and 2 more', got: %s", msg)
	}
}

func TestWTInvalidation_ErrorResult(t *testing.T) {
	w := newWTInvalidationState()

	w.recordRead("file1.go")
	w.recordRead("file2.go")

	// The agent loop checks !result.IsError before calling checkMutation,
	// but the function itself doesn't filter -- test the integration logic
	// is correct by verifying the function works regardless.
	msg := w.checkMutation("git_checkout", `{}`)
	if msg == "" {
		t.Fatal("expected warning even for error result (filtering is in agent.go)")
	}
}

func TestWTInvalidation_ExtractReadPath(t *testing.T) {
	// read_file path extraction
	path := extractWTReadPath("read_file", `{"path":"/foo/bar.go","description":"test"}`)
	if path != "/foo/bar.go" {
		t.Errorf("expected /foo/bar.go, got %s", path)
	}

	// multi_file_read returns empty (handled by extractMultiReadPaths)
	path = extractWTReadPath("multi_file_read", `{"files":[{"path":"a.go"}]}`)
	if path != "" {
		t.Errorf("expected empty for multi_file_read, got %s", path)
	}

	// search tools return empty
	path = extractWTReadPath("grep", `{"pattern":"foo"}`)
	if path != "" {
		t.Errorf("expected empty for grep, got %s", path)
	}
}

func TestWTInvalidation_ExtractMultiReadPaths(t *testing.T) {
	args := `{"files":[{"path":"a.go"},{"path":"b.go"},{"path":"c.go"}]}`
	paths := extractMultiReadPaths(args)
	if len(paths) != 3 {
		t.Fatalf("expected 3 paths, got %d", len(paths))
	}
	if paths[0] != "a.go" || paths[1] != "b.go" || paths[2] != "c.go" {
		t.Errorf("unexpected paths: %v", paths)
	}
}

func TestWTInvalidation_ExtractMultiReadPaths_InvalidJSON(t *testing.T) {
	paths := extractMultiReadPaths("not json")
	if paths != nil {
		t.Errorf("expected nil for invalid JSON, got %v", paths)
	}
}
