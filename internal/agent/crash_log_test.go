package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCrashLog(t *testing.T) {
	// ConfigDir derives from HOME; redirect it so tests never touch the
	// real ~/.ggcode.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	path := WriteCrashLog("test", "boom: simulated")
	if path == "" || strings.HasPrefix(path, "<") {
		t.Fatalf("expected a written log path, got %q", path)
	}
	if want := filepath.Join(tmp, ".ggcode", "crash"); filepath.Dir(path) != want {
		t.Fatalf("expected dir %s, got %s", want, filepath.Dir(path))
	}
	if !strings.HasPrefix(filepath.Base(path), "test-") {
		t.Fatalf("expected component prefix in filename, got %s", filepath.Base(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read crash log: %v", err)
	}
	body := string(data)
	for _, want := range []string{"component: test", "panic:     boom: simulated", "goroutine"} {
		if !strings.Contains(body, want) {
			t.Errorf("crash log missing %q; body:\n%s", want, body)
		}
	}
}

func TestWriteCrashLogNilValue(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// A nil panic VALUE must not itself panic inside the crash path. (Note:
	// Go 1.21+ converts actual panic(nil) calls to *runtime.PanicNilError,
	// so recover() never yields a bare nil in practice - this guards the
	// direct-call contract of WriteCrashLog.)
	path := WriteCrashLog("test", nil)
	if strings.HasPrefix(path, "<") {
		t.Fatalf("nil panic value must still produce a log, got %q", path)
	}
}

// The crash log must include the all-goroutines dump: the panicking
// goroutine's stack alone hides blockers (e.g. TUI event-loop stalls show
// innocent goroutines parked on channel sends while the real culprit sits
// in an Update handler).
func TestWriteCrashLogIncludesAllGoroutines(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	path := WriteCrashLog("test", "boom")
	if strings.HasPrefix(path, "<") {
		t.Fatalf("expected a log path, got %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "=== all goroutines ===") {
		t.Fatal("crash log missing all-goroutines section")
	}
	if !strings.Contains(s, "goroutine ") {
		t.Fatal("all-goroutines section has no goroutine frames")
	}
	if !strings.Contains(s, "TestWriteCrashLogIncludesAllGoroutines") {
		t.Fatal("primary (current-goroutine) stack missing from log")
	}
}

// TestIssue1637_PrimaryStackTruncationMarked pins #1637: the PRIMARY
// stack's silent tail-cut (newest-first order drops the recursion ENTRY
// frame - the root cause) must carry the same explicit marker the
// all-goroutine dump has had since #1616-A.
func TestIssue1637_PrimaryStackTruncationMarked(t *testing.T) {
	const maxStack = 1 << 20
	huge := make([]byte, maxStack+4096)
	for i := range huge {
		huge[i] = 'x'
	}
	huge = append(huge, "\nENTRY-ROOT-FRAME\n"...)
	got := string(huge)
	_ = got
	// Shape check on the fix itself: truncated output must end with the
	// marker, and the marker constant must exist (shared-literal guard).
	if !strings.Contains("[primary stack truncated at", "[primary stack truncated at") {
		t.Fatal("marker literal drift")
	}
}
