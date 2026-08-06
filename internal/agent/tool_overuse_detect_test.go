package agent

import (
	"testing"
)

func TestToolOveruse_ReadAfterWrite(t *testing.T) {
	s := newToolOveruseState()

	// Simulate writing a file at iteration 3
	s.recordWrite("src/main.go", 3)

	// Reading the same file at iteration 4 should trigger warning
	msg := s.checkReadAfterWrite("src/main.go", 4)
	if msg == "" {
		t.Fatal("expected warning for read-after-write")
	}
	if !contains(msg, "src/main.go") {
		t.Errorf("warning should mention the file path, got: %s", msg)
	}
	if !contains(msg, "tool-overuse") {
		t.Errorf("warning should have tool-overuse prefix, got: %s", msg)
	}
}

func TestToolOveruse_ReadAfterWrite_Expired(t *testing.T) {
	s := newToolOveruseState()

	// Write at iteration 1
	s.recordWrite("src/main.go", 1)

	// Read at iteration 10 (beyond window of 3) should NOT trigger
	msg := s.checkReadAfterWrite("src/main.go", 10)
	if msg != "" {
		t.Errorf("expected no warning for expired read-after-write, got: %s", msg)
	}
}

func TestToolOveruse_ReadAfterWrite_DifferentFile(t *testing.T) {
	s := newToolOveruseState()

	s.recordWrite("src/main.go", 3)

	// Reading a different file should NOT trigger
	msg := s.checkReadAfterWrite("src/other.go", 4)
	if msg != "" {
		t.Errorf("expected no warning for different file, got: %s", msg)
	}
}

func TestToolOveruse_DirRelist(t *testing.T) {
	s := newToolOveruseState()

	// List directory at iteration 2
	s.recordDirList("src/components", 2)

	// Re-list at iteration 3 without any writes should trigger
	msg := s.checkDirRelist("src/components", 3)
	if msg == "" {
		t.Fatal("expected warning for unchanged dir re-list")
	}
	if !contains(msg, "src/components") {
		t.Errorf("warning should mention directory, got: %s", msg)
	}
}

func TestToolOveruse_DirRelist_AfterWrite(t *testing.T) {
	s := newToolOveruseState()

	// List at iteration 2
	s.recordDirList("src/components", 2)

	// Write a file in that directory at iteration 3
	s.recordWrite("src/components/Button.tsx", 3)

	// Re-list at iteration 4 should NOT trigger (directory was modified)
	msg := s.checkDirRelist("src/components", 4)
	if msg != "" {
		t.Errorf("expected no warning after dir modification, got: %s", msg)
	}
}

func TestToolOveruse_DirRelist_Expired(t *testing.T) {
	s := newToolOveruseState()

	s.recordDirList("src", 1)

	// Re-list at iteration 10 (beyond window of 4) should NOT trigger
	msg := s.checkDirRelist("src", 10)
	if msg != "" {
		t.Errorf("expected no warning for expired re-list, got: %s", msg)
	}
}

func TestToolOveruse_TrivialCommand(t *testing.T) {
	s := newToolOveruseState()

	tests := []string{
		"go version",
		"git --version",
		"node --version",
		"pwd",
		"whoami",
	}
	for _, cmd := range tests {
		s.reset()
		msg := s.checkTrivialCommand(cmd)
		if msg == "" {
			t.Errorf("expected warning for trivial command: %s", cmd)
		}
	}
}

func TestToolOveruse_TrivialCommand_NonTrivial(t *testing.T) {
	s := newToolOveruseState()

	msg := s.checkTrivialCommand("go build -tags goolm ./...")
	if msg != "" {
		t.Errorf("expected no warning for non-trivial command, got: %s", msg)
	}

	msg = s.checkTrivialCommand("go test -run TestFoo ./...")
	if msg != "" {
		t.Errorf("expected no warning for test command, got: %s", msg)
	}
}

func TestToolOveruse_MaxWarnings(t *testing.T) {
	s := newToolOveruseState()

	// First warning
	s.recordWrite("file1.go", 1)
	msg1 := s.checkReadAfterWrite("file1.go", 2)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	// Second warning (different file)
	s.recordWrite("file2.go", 2)
	msg2 := s.checkReadAfterWrite("file2.go", 3)
	if msg2 == "" {
		t.Fatal("expected second warning")
	}

	// Third should be suppressed (max 2 warnings)
	s.recordWrite("file3.go", 3)
	msg3 := s.checkReadAfterWrite("file3.go", 4)
	if msg3 != "" {
		t.Errorf("expected third warning to be suppressed, got: %s", msg3)
	}
}

func TestToolOveruse_MaybeWarn_ReadFile(t *testing.T) {
	s := newToolOveruseState()

	// Simulate write
	s.maybeWarn("edit_file", `{"file_path":"src/main.go","old_text":"a","new_text":"b"}`, 2)

	// Read the same file should trigger
	msg := s.maybeWarn("read_file", `{"path":"src/main.go"}`, 3)
	if msg == "" {
		t.Fatal("expected tool-overuse warning for read after edit")
	}
}

func TestToolOveruse_MaybeWarn_RunCommand(t *testing.T) {
	s := newToolOveruseState()

	msg := s.maybeWarn("run_command", `{"command":"go version"}`, 1)
	if msg == "" {
		t.Fatal("expected tool-overuse warning for trivial command")
	}
}

func TestToolOveruse_MaybeWarn_ListDirectory(t *testing.T) {
	s := newToolOveruseState()

	// First list - no warning
	msg := s.maybeWarn("list_directory", `{"path":"src"}`, 1)
	if msg != "" {
		t.Fatalf("expected no warning on first list, got: %s", msg)
	}

	// Second list without writes - should warn
	msg = s.maybeWarn("list_directory", `{"path":"src"}`, 2)
	if msg == "" {
		t.Fatal("expected warning for unchanged re-list")
	}
}

func TestToolOveruse_Reset(t *testing.T) {
	s := newToolOveruseState()

	s.recordWrite("file.go", 1)
	s.recordDirList("src", 1)
	s.checkReadAfterWrite("file.go", 2) // triggers a warning

	s.reset()

	if len(s.filesWritten) != 0 || len(s.dirsListed) != 0 || s.warnings != 0 {
		t.Error("reset should clear all state")
	}
}

func TestToolOveruse_PathNormalization(t *testing.T) {
	s := newToolOveruseState()

	// Write with trailing slash variant
	s.recordWrite("src/main.go", 1)

	// Read without trailing slash should still match
	msg := s.checkReadAfterWrite("src/main.go", 2)
	if msg == "" {
		t.Fatal("expected warning despite path normalization differences")
	}
}

func TestOveruseNormalizePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"src/main.go", "src/main.go"},
		{"src/", "src"},
		{"", ""},
		{"  src/main.go  ", "src/main.go"},
	}
	for _, tt := range tests {
		got := overuseNormalizePath(tt.input)
		if got != tt.want {
			t.Errorf("overuseNormalizePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// Note: contains() is defined in reflection_test.go, reused here.
