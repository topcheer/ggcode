package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeRedundantTestFile creates a file with the given content and returns its path.
func writeRedundantTestFile(t *testing.T, name string, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRedundantRead_FirstReadNoWarning(t *testing.T) {
	state := newRedundantReadState()
	path := writeRedundantTestFile(t, "big.go", strings.Repeat("line\n", 500)) // >2KB

	hint := state.checkRedundantRead(path, false)
	if hint != "" {
		t.Errorf("first read should not trigger warning, got: %s", hint)
	}
}

func TestRedundantRead_SecondReadTriggersWarning(t *testing.T) {
	state := newRedundantReadState()
	path := writeRedundantTestFile(t, "big.go", strings.Repeat("line\n", 500))

	// First read - no warning.
	state.checkRedundantRead(path, false)

	// Second read without changes - should warn.
	hint := state.checkRedundantRead(path, false)
	if hint == "" {
		t.Error("second read of unchanged file should trigger warning")
	}
	if !strings.Contains(hint, "already read") {
		t.Errorf("warning should mention 'already read', got: %s", hint)
	}
}

func TestRedundantRead_ThirdReadNoDuplicateWarning(t *testing.T) {
	state := newRedundantReadState()
	path := writeRedundantTestFile(t, "big.go", strings.Repeat("line\n", 500))

	state.checkRedundantRead(path, false) // first read
	state.checkRedundantRead(path, false) // second read - warning

	// Third read - should NOT warn again (dedup).
	hint := state.checkRedundantRead(path, false)
	if hint != "" {
		t.Errorf("third read should not trigger duplicate warning, got: %s", hint)
	}
}

func TestRedundantRead_SmallFileNoWarning(t *testing.T) {
	state := newRedundantReadState()
	path := writeRedundantTestFile(t, "small.txt", "tiny") // <2KB

	state.checkRedundantRead(path, false)         // first read
	hint := state.checkRedundantRead(path, false) // second read
	if hint != "" {
		t.Errorf("small file re-read should not trigger warning, got: %s", hint)
	}
}

func TestRedundantRead_FileModifiedNoWarning(t *testing.T) {
	state := newRedundantReadState()
	path := writeRedundantTestFile(t, "big.go", strings.Repeat("line\n", 500))

	state.checkRedundantRead(path, false) // first read

	// Modify the file externally.
	time.Sleep(10 * time.Millisecond) // ensure mtime changes
	if err := os.WriteFile(path, []byte(strings.Repeat("modified\n", 500)), 0644); err != nil {
		t.Fatal(err)
	}

	// Second read after modification - should NOT warn (content changed).
	hint := state.checkRedundantRead(path, false)
	if hint != "" {
		t.Errorf("re-read of modified file should not trigger warning, got: %s", hint)
	}
}

func TestRedundantRead_AfterWriteClearsState(t *testing.T) {
	state := newRedundantReadState()
	path := writeRedundantTestFile(t, "big.go", strings.Repeat("line\n", 500))

	state.checkRedundantRead(path, false) // first read

	// Simulate agent editing the file.
	state.recordWrite(path)

	// Read after edit - should NOT warn (content was modified by agent).
	hint := state.checkRedundantRead(path, false)
	if hint != "" {
		t.Errorf("read after write should not trigger warning, got: %s", hint)
	}
}

func TestRedundantRead_AfterWriteThenRereadWithoutChange(t *testing.T) {
	state := newRedundantReadState()
	path := writeRedundantTestFile(t, "big.go", strings.Repeat("line\n", 500))

	state.checkRedundantRead(path, false) // first read
	state.recordWrite(path)               // agent edits
	state.checkRedundantRead(path, false) // re-read after edit - no warning

	// Read again without changes - should warn now.
	hint := state.checkRedundantRead(path, false)
	if hint == "" {
		t.Error("second consecutive re-read after write+read should trigger warning")
	}
}

func TestRedundantRead_Reset(t *testing.T) {
	state := newRedundantReadState()
	path := writeRedundantTestFile(t, "big.go", strings.Repeat("line\n", 500))

	state.checkRedundantRead(path, false)
	state.checkRedundantRead(path, false) // warning fires

	state.reset()

	// After reset, first read should not warn.
	hint := state.checkRedundantRead(path, false)
	if hint != "" {
		t.Errorf("first read after reset should not warn, got: %s", hint)
	}
}

func TestRedundantRead_EmptyPath(t *testing.T) {
	state := newRedundantReadState()
	hint := state.checkRedundantRead("", false)
	if hint != "" {
		t.Errorf("empty path should return empty hint, got: %s", hint)
	}
}

func TestRedundantRead_NonExistentFile(t *testing.T) {
	state := newRedundantReadState()
	// Non-existent path - should not panic, should return empty.
	hint := state.checkRedundantRead("/nonexistent/path/to/file.go", false)
	if hint != "" {
		t.Errorf("non-existent file should return empty hint, got: %s", hint)
	}
}
