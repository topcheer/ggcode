package agent

// Regression tests for issue #1169: the reckless-exec detector must match
// read and edit paths after normalization. An absolute-path read plus a
// repo-relative-path edit (or vice versa, or a "./" prefix difference) of
// the same file is explored content, not an unexplored edit. Normalization
// follows the readValidityKey pattern from read_validity_check.go (fix
// #557) plus a #627-style unique suffix match.

import "testing"

// Absolute read + relative edit of the same file counts as explored.
func TestIssue1169_AbsoluteReadRelativeEdit(t *testing.T) {
	s := newRecklessExecState()
	s.recordReadTool("read_file", `{"file_path":"/repo/internal/agent/foo.go"}`)
	if warn := s.recordEditTool("edit_file", `{"file_path":"internal/agent/foo.go"}`); warn {
		t.Fatal("edit of an explored file (abs read, rel edit) fired a warning")
	}
	if s.unexplored != 0 {
		t.Fatalf("unexplored = %d, want 0", s.unexplored)
	}
}

// Reverse direction: relative read + absolute edit.
func TestIssue1169_RelativeReadAbsoluteEdit(t *testing.T) {
	s := newRecklessExecState()
	s.recordReadTool("read_file", `{"file_path":"internal/agent/foo.go"}`)
	if warn := s.recordEditTool("edit_file", `{"file_path":"/repo/internal/agent/foo.go"}`); warn {
		t.Fatal("edit of an explored file (rel read, abs edit) fired a warning")
	}
	if s.unexplored != 0 {
		t.Fatalf("unexplored = %d, want 0", s.unexplored)
	}
}

// "./" prefix and redundant separators must normalize away (#557 pattern).
func TestIssue1169_DotSlashAndCleanNormalization(t *testing.T) {
	s := newRecklessExecState()
	s.recordReadTool("read_file", `{"file_path":"internal//agent/foo.go"}`)
	if warn := s.recordEditTool("edit_file", `{"file_path":"./internal/agent/foo.go"}`); warn {
		t.Fatal("edit of an explored file (./ and // prefix variants) fired a warning")
	}
	if s.unexplored != 0 {
		t.Fatalf("unexplored = %d, want 0", s.unexplored)
	}
}

// Different files must still count as unexplored.
func TestIssue1169_DifferentFilesStillUnexplored(t *testing.T) {
	s := newRecklessExecState()
	s.recordReadTool("read_file", `{"file_path":"/repo/internal/agent/foo.go"}`)
	_ = s.recordEditTool("edit_file", `{"file_path":"/repo/internal/agent/bar.go"}`)
	if s.unexplored != 1 {
		t.Fatalf("unexplored = %d, want 1", s.unexplored)
	}
}

// A read under a longer prefix is still explored via unique suffix match.
func TestIssue1169_UniqueSuffixMatch(t *testing.T) {
	s := newRecklessExecState()
	s.recordReadTool("read_file", `{"file_path":"/repo/deep/nested/dir/internal/agent/foo.go"}`)
	if warn := s.recordEditTool("edit_file", `{"file_path":"internal/agent/foo.go"}`); warn {
		t.Fatal("edit of a suffix-matched explored file fired a warning")
	}
	if s.unexplored != 0 {
		t.Fatalf("unexplored = %d, want 0", s.unexplored)
	}
}

// recklessPathKey should behave like readValidityKey on normalized input.
func TestIssue1169_RecklessPathKeyMatchesReadValidityKey(t *testing.T) {
	cases := []string{"  a/b.go  ", "./a/b.go", "a//b.go"}
	for _, c := range cases {
		if recklessPathKey(c) != readValidityKey(c) {
			t.Fatalf("recklessPathKey(%q) = %q, readValidityKey = %q", c, recklessPathKey(c), readValidityKey(c))
		}
	}
}
