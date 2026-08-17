package agent

import (
	"strings"
	"testing"
)

func TestSolutionFixation_NoEdits(t *testing.T) {
	s := newSolutionFixationState()
	if msg := s.checkAndWarn(); msg != "" {
		t.Fatalf("expected empty warning, got: %s", msg)
	}
}

func TestSolutionFixation_BelowThreshold(t *testing.T) {
	s := newSolutionFixationState()
	// 2 failed edits (below threshold of 3)
	s.recordToolCall("edit_file", `{"file_path":"/src/auth.go"}`, true)
	s.recordToolCall("edit_file", `{"file_path":"/src/auth.go"}`, true)
	if msg := s.checkAndWarn(); msg != "" {
		t.Fatalf("expected empty warning below threshold, got: %s", msg)
	}
}

func TestSolutionFixation_TriggersOnThreeFailures(t *testing.T) {
	s := newSolutionFixationState()
	s.recordToolCall("edit_file", `{"file_path":"/src/auth.go"}`, true)
	s.recordToolCall("edit_file", `{"file_path":"/src/auth.go"}`, true)
	s.recordToolCall("edit_file", `{"file_path":"/src/auth.go"}`, true)
	msg := s.checkAndWarn()
	if msg == "" {
		t.Fatal("expected warning after 3 failed edits on same file")
	}
	if !strings.Contains(msg, "auth.go") {
		t.Errorf("warning should mention file name, got: %s", msg)
	}
	if !strings.Contains(msg, "Solution Fixation") {
		t.Errorf("warning should mention Solution Fixation, got: %s", msg)
	}
}

func TestSolutionFixation_SuccessfulEditsDoNotCount(t *testing.T) {
	s := newSolutionFixationState()
	s.recordToolCall("edit_file", `{"file_path":"/src/auth.go"}`, true)
	s.recordToolCall("edit_file", `{"file_path":"/src/auth.go"}`, true)
	s.recordToolCall("edit_file", `{"file_path":"/src/auth.go"}`, false) // success
	if msg := s.checkAndWarn(); msg != "" {
		t.Fatalf("expected no warning when only 2 failures, got: %s", msg)
	}
}

func TestSolutionFixation_DifferentFilesDoNotTrigger(t *testing.T) {
	s := newSolutionFixationState()
	s.recordToolCall("edit_file", `{"file_path":"/src/auth.go"}`, true)
	s.recordToolCall("edit_file", `{"file_path":"/src/user.go"}`, true)
	s.recordToolCall("edit_file", `{"file_path":"/src/db.go"}`, true)
	if msg := s.checkAndWarn(); msg != "" {
		t.Fatalf("expected no warning when failures are on different files, got: %s", msg)
	}
}

func TestSolutionFixation_MaxWarnings(t *testing.T) {
	s := newSolutionFixationState()
	// Trigger on auth.go
	for i := 0; i < 3; i++ {
		s.recordToolCall("edit_file", `{"file_path":"/src/auth.go"}`, true)
	}
	if msg := s.checkAndWarn(); msg == "" {
		t.Fatal("expected first warning")
	}
	// Trigger on user.go - second warning
	for i := 0; i < 3; i++ {
		s.recordToolCall("edit_file", `{"file_path":"/src/user.go"}`, true)
	}
	if msg := s.checkAndWarn(); msg == "" {
		t.Fatal("expected second warning")
	}
	// Third trigger - should be suppressed
	for i := 0; i < 3; i++ {
		s.recordToolCall("edit_file", `{"file_path":"/src/db.go"}`, true)
	}
	if msg := s.checkAndWarn(); msg != "" {
		t.Fatalf("expected third warning to be suppressed, got: %s", msg)
	}
}

func TestSolutionFixation_DoesNotRefireSameFile(t *testing.T) {
	s := newSolutionFixationState()
	for i := 0; i < 4; i++ {
		s.recordToolCall("edit_file", `{"file_path":"/src/auth.go"}`, true)
	}
	if msg := s.checkAndWarn(); msg == "" {
		t.Fatal("expected first warning")
	}
	// More failures on same file - should not refire
	for i := 0; i < 3; i++ {
		s.recordToolCall("edit_file", `{"file_path":"/src/auth.go"}`, true)
	}
	if msg := s.checkAndWarn(); msg != "" {
		t.Fatalf("expected no second warning for same file, got: %s", msg)
	}
}

func TestSolutionFixation_WindowEviction(t *testing.T) {
	s := newSolutionFixationState()
	// Fill window with failures on auth.go, then push them out with other calls
	for i := 0; i < 3; i++ {
		s.recordToolCall("edit_file", `{"file_path":"/src/auth.go"}`, true)
	}
	// Now add 13 calls to other files to push auth.go failures out of window
	for i := 0; i < 13; i++ {
		s.recordToolCall("edit_file", `{"file_path":"/src/other.go"}`, false)
	}
	if msg := s.checkAndWarn(); msg != "" {
		t.Fatalf("expected no warning after window eviction, got: %s", msg)
	}
}

func TestSolutionFixation_NonEditToolsIgnored(t *testing.T) {
	s := newSolutionFixationState()
	s.recordToolCall("run_command", `{"command":"go test"}`, true)
	s.recordToolCall("run_command", `{"command":"go test"}`, true)
	s.recordToolCall("run_command", `{"command":"go test"}`, true)
	if msg := s.checkAndWarn(); msg != "" {
		t.Fatalf("expected no warning for non-edit tools, got: %s", msg)
	}
}

func TestSolutionFixation_MultiEditFile(t *testing.T) {
	s := newSolutionFixationState()
	s.recordToolCall("multi_edit_file", `{"file_path":"/src/complex.go"}`, true)
	s.recordToolCall("multi_edit_file", `{"file_path":"/src/complex.go"}`, true)
	s.recordToolCall("multi_edit_file", `{"file_path":"/src/complex.go"}`, true)
	if msg := s.checkAndWarn(); msg == "" {
		t.Fatal("expected warning for multi_edit_file failures")
	}
}

func TestSolutionFixation_WriteFile(t *testing.T) {
	s := newSolutionFixationState()
	s.recordToolCall("write_file", `{"path":"/src/new.go"}`, true)
	s.recordToolCall("write_file", `{"path":"/src/new.go"}`, true)
	s.recordToolCall("write_file", `{"path":"/src/new.go"}`, true)
	if msg := s.checkAndWarn(); msg == "" {
		t.Fatal("expected warning for write_file failures")
	}
}

func TestSolutionFixation_ExtractPathJSON(t *testing.T) {
	// #393: extraction returns the full path; normalization (now keeping
	// the full cleaned path, not the base name) is tested separately.
	// #639: extraction returns ALL paths (multi-file tools attribute every
	// files[] entry), compared as a comma-joined list for readability.
	tests := []struct {
		name string
		args string
		want string
	}{
		{"file_path field", `{"file_path":"/foo/bar.go","old_text":"x"}`, "/foo/bar.go"},
		{"path field", `{"path":"./baz.go"}`, "./baz.go"},
		{"notebook_path field", `{"notebook_path":"/x/nb.ipynb"}`, "/x/nb.ipynb"},
		{"empty", `{}`, ""},
		{"invalid json", `not json`, ""},
		{"missing path", `{"content":"hello"}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Join(extractFilePathsFromEditArgs(tt.args), ",")
			if got != tt.want {
				t.Errorf("extractFilePathsFromEditArgs(%q) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestSolutionFixation_Reset(t *testing.T) {
	s := newSolutionFixationState()
	s.recordToolCall("edit_file", `{"file_path":"/src/auth.go"}`, true)
	s.recordToolCall("edit_file", `{"file_path":"/src/auth.go"}`, true)
	s.recordToolCall("edit_file", `{"file_path":"/src/auth.go"}`, true)
	s.checkAndWarn() // fires
	s.reset()
	if msg := s.checkAndWarn(); msg != "" {
		t.Fatalf("expected no warning after reset, got: %s", msg)
	}
	if len(s.recentCalls) != 0 || len(s.failedByFile) != 0 {
		t.Fatal("reset did not clear state")
	}
}

func TestSolutionFixation_NormalizePath(t *testing.T) {
	// #393: the counting key keeps the FULL cleaned path — the old base-name
	// reduction merged cmd/a/main.go with internal/b/main.go and stacked
	// failures from different targets into one false warning.
	tests := []struct {
		input string
		want  string
	}{
		{"/abs/path/file.go", "/abs/path/file.go"},
		{"./relative/file.go", "./relative/file.go"},
		{"file.go", "file.go"},
		{"", ""},
		{"/", ""},
		{"cmd/a/main.go", "cmd/a/main.go"},
		{"internal/b/main.go", "internal/b/main.go"},
		{`C:\Users\x\main.go`, "C:/Users/x/main.go"},
		{"dup//sep.go", "dup/sep.go"},
	}
	for _, tt := range tests {
		got := normalizePathFixation(tt.input)
		if got != tt.want {
			t.Errorf("normalizePathFixation(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
