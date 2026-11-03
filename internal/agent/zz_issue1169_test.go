package agent

import "testing"

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

func TestIssue1169_DifferentFilesStillUnexplored(t *testing.T) {
	s := newRecklessExecState()
	s.recordReadTool("read_file", `{"file_path":"/repo/internal/agent/foo.go"}`)
	_ = s.recordEditTool("edit_file", `{"file_path":"/repo/internal/agent/bar.go"}`)
	if s.unexplored != 1 {
		t.Fatalf("unexplored = %d, want 1", s.unexplored)
	}
}

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

func TestIssue1169_RecklessPathKeyMatchesReadValidityKey(t *testing.T) {
	cases := []string{"  a/b.go  ", "./a/b.go", "a//b.go"}
	for _, c := range cases {
		if recklessPathKey(c) != readValidityKey(c) {
			t.Fatalf("recklessPathKey(%q) = %q, readValidityKey = %q", c, recklessPathKey(c), readValidityKey(c))
		}
	}
}
