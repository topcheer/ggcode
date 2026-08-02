package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileFreshnessSentinel_NoReads(t *testing.T) {
	s := newFileFreshnessSentinel()
	msg := s.maybeCheckStaleFiles(1)
	if msg != "" {
		t.Fatalf("expected empty message with no reads, got %q", msg)
	}
}

func TestFileFreshnessSentinel_NoChanges(t *testing.T) {
	s := newFileFreshnessSentinel()

	dir := t.TempDir()
	f := filepath.Join(dir, "test.go")
	os.WriteFile(f, []byte("package main\n"), 0644)

	s.recordRead(f)
	msg := s.maybeCheckStaleFiles(1)
	if msg != "" {
		t.Fatalf("expected empty message when no changes, got %q", msg)
	}
}

func TestFileFreshnessSentinel_DetectsExternalChange(t *testing.T) {
	s := newFileFreshnessSentinel()

	dir := t.TempDir()
	f := filepath.Join(dir, "test.go")
	os.WriteFile(f, []byte("package main\n"), 0644)

	s.recordRead(f)

	// Simulate external modification with a future mtime
	future := time.Now().Add(5 * time.Second)
	os.Chtimes(f, future, future)

	msg := s.maybeCheckStaleFiles(1)
	if msg == "" {
		t.Fatal("expected stale notification after external change")
	}
	if !containsFreshness(msg, "changed on disk") {
		t.Errorf("expected 'changed on disk' in message, got: %s", msg)
	}
	if !containsFreshness(msg, f) && !containsFreshness(msg, "test.go") {
		t.Errorf("expected file path in message, got: %s", msg)
	}
}

func TestFileFreshnessSentinel_SkipsAgentWrittenFiles(t *testing.T) {
	s := newFileFreshnessSentinel()

	dir := t.TempDir()
	f := filepath.Join(dir, "test.go")
	os.WriteFile(f, []byte("package main\n"), 0644)

	s.recordRead(f)
	s.recordWrite(f) // Agent wrote it after reading

	future := time.Now().Add(5 * time.Second)
	os.Chtimes(f, future, future)

	msg := s.maybeCheckStaleFiles(1)
	if msg != "" {
		t.Fatalf("expected empty message for agent-written file, got %q", msg)
	}
}

func TestFileFreshnessSentinel_NotifiesOncePerFile(t *testing.T) {
	s := newFileFreshnessSentinel()

	dir := t.TempDir()
	f := filepath.Join(dir, "test.go")
	os.WriteFile(f, []byte("package main\n"), 0644)

	s.recordRead(f)

	future := time.Now().Add(5 * time.Second)
	os.Chtimes(f, future, future)

	msg1 := s.maybeCheckStaleFiles(1)
	if msg1 == "" {
		t.Fatal("expected first notification")
	}

	// Second check at the same iteration should not re-notify
	msg2 := s.maybeCheckStaleFiles(2)
	if msg2 != "" {
		t.Fatalf("expected no duplicate notification, got %q", msg2)
	}
}

func TestFileFreshnessSentinel_ThrottledByInterval(t *testing.T) {
	s := newFileFreshnessSentinel()

	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.go")
	f2 := filepath.Join(dir, "b.go")
	os.WriteFile(f1, []byte("package main\n"), 0644)
	os.WriteFile(f2, []byte("package main\n"), 0644)

	// Read both files
	s.recordRead(f1)
	s.recordRead(f2)

	// Modify f1 externally
	future := time.Now().Add(5 * time.Second)
	os.Chtimes(f1, future, future)

	// Check at iteration 1 - should detect f1
	msg1 := s.maybeCheckStaleFiles(1)
	if msg1 == "" {
		t.Fatal("expected notification at iteration 1")
	}

	// Now modify f2 as well
	os.Chtimes(f2, future, future)

	// Check at iteration 2 - should be throttled (not enough iterations since last check)
	msg2 := s.maybeCheckStaleFiles(2)
	if msg2 != "" {
		t.Fatalf("expected throttled skip at iteration 2, got %q", msg2)
	}

	// Check at iteration 4 (1 + 3 = 4) - should detect f2
	msg4 := s.maybeCheckStaleFiles(4)
	if msg4 == "" {
		t.Fatal("expected notification for f2 at iteration 4")
	}
}

func TestFileFreshnessSentinel_RecordReadClearsAgentWritten(t *testing.T) {
	s := newFileFreshnessSentinel()

	dir := t.TempDir()
	f := filepath.Join(dir, "test.go")
	os.WriteFile(f, []byte("package main\n"), 0644)

	s.recordRead(f)

	// External change detected
	future := time.Now().Add(5 * time.Second)
	os.Chtimes(f, future, future)

	msg1 := s.maybeCheckStaleFiles(1)
	if msg1 == "" {
		t.Fatal("expected stale notification on first check")
	}

	// Agent re-reads the file, updating its tracked mtime
	s.recordRead(f)

	// Second check should not re-notify (file is now fresh from the agent's perspective)
	msg2 := s.maybeCheckStaleFiles(4)
	if msg2 != "" {
		t.Fatalf("agent re-read should clear stale notification, got %q", msg2)
	}
}

func TestFileFreshnessSentinel_Reset(t *testing.T) {
	s := newFileFreshnessSentinel()

	dir := t.TempDir()
	f := filepath.Join(dir, "test.go")
	os.WriteFile(f, []byte("package main\n"), 0644)

	s.recordRead(f)
	s.recordWrite(f)

	s.reset()

	if len(s.readMtimes) != 0 || len(s.agentWritten) != 0 || len(s.notified) != 0 {
		t.Fatal("reset should clear all maps")
	}
}

func TestFileFreshnessSentinel_DeletedFile(t *testing.T) {
	s := newFileFreshnessSentinel()

	dir := t.TempDir()
	f := filepath.Join(dir, "test.go")
	os.WriteFile(f, []byte("package main\n"), 0644)

	s.recordRead(f)

	// Delete the file
	os.Remove(f)

	msg := s.maybeCheckStaleFiles(1)
	if msg != "" {
		t.Fatalf("deleted file should not trigger notification, got %q", msg)
	}
}

func TestFileFreshnessSentinel_EmptyPath(t *testing.T) {
	s := newFileFreshnessSentinel()
	s.recordRead("")  // should be no-op
	s.recordWrite("") // should be no-op
	if len(s.readMtimes) != 0 || len(s.agentWritten) != 0 {
		t.Fatal("empty path should be ignored")
	}
}

func TestShortenForDisplay(t *testing.T) {
	tests := []struct {
		input    string
		contains string
	}{
		{"/a/b/c.go", "/a/b/c.go"},          // short path kept as-is
		{"/a/b/c/d/e/f.go", ".../d/e/f.go"}, // long path shortened
		{"/foo/bar.go", "/foo/bar.go"},      // 3 components kept
	}
	for _, tt := range tests {
		got := shortenForDisplay(tt.input)
		if !containsFreshness(got, tt.contains) {
			t.Errorf("shortenForDisplay(%q) = %q, expected to contain %q", tt.input, got, tt.contains)
		}
	}
}

func containsFreshness(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && indexOfStr(s, substr) >= 0)
}

func indexOfStr(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
