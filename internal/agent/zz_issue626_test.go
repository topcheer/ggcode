package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Issue #626: a partial read (offset/limit) must NOT seed the path-level
// mtime baseline for "already read" detection. The context only holds a
// fragment, so a later FULL read of the same file is not redundant and must
// not be discouraged with an "already read (NKB in context)" hint.
func TestIssue626_PartialReadDoesNotSeedMtimeBaseline(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "big.go")

	// File above redundantReadMinSize so the full-read warning path applies.
	content := make([]byte, redundantReadMinSize+2048)
	for i := range content {
		content[i] = byte('a' + i%26)
	}
	if err := os.WriteFile(f, content, 0o644); err != nil {
		t.Fatal(err)
	}
	// Ensure a later stat would observe a different-at-nanosecond-resolution
	// mtime if the file were rewritten (not needed for the core check, but
	// keeps the scenario deterministic).
	time.Sleep(2 * time.Millisecond)

	r := newRedundantReadState()

	// Step 1: partial read (agent explores the head of a large file).
	if got := r.checkRedundantRead(f, true); got != "" {
		t.Fatalf("partial read should never warn, got %q", got)
	}

	// Step 2: full read of the same file — must NOT be flagged as redundant,
	// because the context only contains the earlier fragment.
	if got := r.checkRedundantRead(f, false); got != "" {
		t.Fatalf("full read after partial read mis-flagged as redundant (#626): %q", got)
	}
}

// Control: after a genuine full read, a second full read still warns.
func TestIssue626_SecondFullReadStillWarns(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "big2.go")
	content := make([]byte, redundantReadMinSize+2048)
	if err := os.WriteFile(f, content, 0o644); err != nil {
		t.Fatal(err)
	}

	r := newRedundantReadState()
	if got := r.checkRedundantRead(f, false); got != "" {
		t.Fatalf("first full read should not warn, got %q", got)
	}
	if got := r.checkRedundantRead(f, false); got == "" {
		t.Fatal("second full read without changes should warn (regression of core behavior)")
	}
}

// Partial reads interleaved with full reads keep the guard quiet for
// windowed access, and repeated partial reads never warn either.
func TestIssue626_RepeatedPartialReadsNeverWarn(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "win.go")
	if err := os.WriteFile(f, make([]byte, redundantReadMinSize*4), 0o644); err != nil {
		t.Fatal(err)
	}
	r := newRedundantReadState()
	for i := 0; i < 3; i++ {
		if got := r.checkRedundantRead(f, true); got != "" {
			t.Fatalf("partial read %d warned: %q", i, got)
		}
	}
}
