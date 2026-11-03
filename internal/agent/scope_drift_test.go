package agent

import (
	"strings"
	"testing"
)

func TestScopeDriftState_Simple(t *testing.T) {
	s := newScopeDriftState()

	s.recordEdit("internal/agent/foo.go")
	s.recordEdit("internal/agent/bar.go")
	s.recordEdit("internal/config/baz.go")

	msg := s.check(false)
	if msg != "" {
		t.Fatalf("expected no scope drift warning for 2 dirs, got: %s", msg)
	}
}

func TestScopeDriftState_TriggerOnDirCount(t *testing.T) {
	s := newScopeDriftState()

	dirs := []string{
		"internal/agent/a.go",
		"internal/config/b.go",
		"internal/tool/c.go",
		"internal/chat/d.go",
		"internal/im/e.go",
		"internal/cron/f.go",
	}
	for _, f := range dirs {
		s.recordEdit(f)
	}

	msg := s.check(false)
	if msg == "" {
		t.Fatal("expected scope drift warning for 6 dirs on simple task")
	}
	if !strings.Contains(msg, "Scope check") {
		t.Fatalf("unexpected message: %s", msg)
	}
	if s.fired != true {
		t.Fatal("fired flag should be true after trigger")
	}

	msg2 := s.check(false)
	if msg2 != "" {
		t.Fatal("expected no second warning after already fired")
	}
}

func TestScopeDriftState_ComplexTaskHigherThreshold(t *testing.T) {
	s := newScopeDriftState()

	dirs := []string{
		"internal/agent/a.go",
		"internal/config/b.go",
		"internal/tool/c.go",
		"internal/chat/d.go",
		"internal/im/e.go",
		"internal/cron/f.go",
		"internal/cost/g.go",
	}
	for _, f := range dirs {
		s.recordEdit(f)
	}

	msg := s.check(true)
	if msg != "" {
		t.Fatalf("expected no scope drift warning for 7 dirs on complex task, got: %s", msg)
	}
}

func TestScopeDriftState_ComplexTaskTriggers(t *testing.T) {
	s := newScopeDriftState()

	for i := 0; i < 11; i++ {
		dir := "pkg" + string(rune('a'+i)) + "/sub"
		s.recordEdit(dir + "/file.go")
	}

	msg := s.check(true)
	if msg == "" {
		t.Fatal("expected scope drift warning for 11 dirs on complex task")
	}
}

func TestScopeDriftState_FileCountThreshold(t *testing.T) {
	s := newScopeDriftState()

	for i := 0; i < 26; i++ {
		s.recordEdit("internal/agent/file" + string(rune('a'+i%26)) + ".go")
	}

	msg := s.check(false)
	if msg == "" {
		t.Fatal("expected scope drift warning for 26 files")
	}
	if !strings.Contains(msg, "files") {
		t.Fatalf("expected message to mention files, got: %s", msg)
	}
}

func TestScopeDriftState_MinOpsBeforeCheck(t *testing.T) {
	s := newScopeDriftState()

	s.recordEdit("dir1/a.go")
	s.recordEdit("dir2/b.go")
	s.recordEdit("dir3/c.go")

	msg := s.check(false)
	if msg != "" {
		t.Fatal("expected no warning before minimum ops threshold")
	}
}

func TestScopeDriftState_EmptyPath(t *testing.T) {
	s := newScopeDriftState()
	s.recordEdit("")
	if len(s.editFiles) != 0 || s.productiveCount != 0 {
		t.Fatal("empty path should not be recorded")
	}
}

func TestScopeDriftState_DirSignature(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"internal/agent/sub", "internal/agent"},
		{"src/components", "src/components"},
		{"pkg", "pkg"},
		{"a/b/c/d", "a/b"},
		{"", ""},
	}
	for _, tt := range tests {
		got := dirSignature(tt.input)
		if got != tt.expected {
			t.Errorf("dirSignature(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestScopeDriftState_Reset(t *testing.T) {
	s := newScopeDriftState()
	s.recordEdit("internal/agent/a.go")
	s.recordEdit("internal/config/b.go")
	s.fired = true
	s.productiveCount = 10

	s.reset()

	if len(s.editedDirs) != 0 || len(s.editFiles) != 0 || s.productiveCount != 0 || s.fired {
		t.Fatal("reset should clear all state")
	}
}
