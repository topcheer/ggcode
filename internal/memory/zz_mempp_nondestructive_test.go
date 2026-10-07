package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestAutoMemory builds an AutoMemory rooted in a temp dir (house
// pattern: NewAutoMemory() then override am.dir, per auto_test.go:154-156).
func newTestAutoMemory(t *testing.T) *AutoMemory {
	t.Helper()
	am := NewAutoMemory()
	am.dir = t.TempDir()
	return am
}

// TestArchiveOnOverwrite: same-key saves must archive, not destroy, the
// outgoing version (Mem++ arXiv:2610.02002 non-destructive write path).
func TestArchiveOnOverwrite(t *testing.T) {
	am := newTestAutoMemory(t)
	if err := am.SaveMemory("deploy", "v1 use blue-green"); err != nil {
		t.Fatal(err)
	}
	if err := am.SaveMemory("deploy", "v2 use rolling"); err != nil {
		t.Fatal(err)
	}
	// Live file holds v2.
	if got, _ := am.LoadKey("deploy"); got != "v2 use rolling" {
		t.Fatalf("live = %q, want v2", got)
	}
	// .history holds exactly one archived v1.
	vers := am.HistoryVersions("deploy")
	if len(vers) != 1 {
		t.Fatalf("history versions = %d, want 1", len(vers))
	}
	data, err := os.ReadFile(vers[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v1 use blue-green" {
		t.Fatalf("archived = %q, want v1", string(data))
	}
}

// TestReadAsOf: temporal read returns the version that held at the asked
// instant, including the live file for instants after the last write.
func TestReadAsOf(t *testing.T) {
	am := newTestAutoMemory(t)
	if err := am.SaveMemory("runbook", "first"); err != nil {
		t.Fatal(err)
	}
	mid := time.Now()
	// Guarantee archive stamp (taken at write2) is strictly after mid.
	time.Sleep(10 * time.Millisecond)
	if err := am.SaveMemory("runbook", "second"); err != nil {
		t.Fatal(err)
	}

	if got, _, ok, err := am.ReadMemoryAsOf("runbook", mid); err != nil || !ok {
		t.Fatalf("as_of(mid) err=%v ok=%v", err, ok)
	} else if got != "first" {
		t.Fatalf("as_of(mid) = %q, want first", got)
	}

	if got, _, ok, err := am.ReadMemoryAsOf("runbook", time.Now()); err != nil || !ok {
		t.Fatalf("as_of(now) err=%v ok=%v", err, ok)
	} else if got != "second" {
		t.Fatalf("as_of(now) = %q, want second", got)
	}

	// as_of predating every version: key did not exist.
	if _, _, ok, err := am.ReadMemoryAsOf("runbook", time.Time{}); err != nil || ok {
		t.Fatalf("as_of(zero) err=%v ok=%v, want ok=false", err, ok)
	}
}

// TestReadAsOfNoHistory: a never-overwritten key degrades gracefully.
func TestReadAsOfNoHistory(t *testing.T) {
	am := newTestAutoMemory(t)
	if err := am.SaveMemory("fresh", "only"); err != nil {
		t.Fatal(err)
	}
	if got, _, ok, err := am.ReadMemoryAsOf("fresh", time.Now()); err != nil || !ok || got != "only" {
		t.Fatalf("got=%q ok=%v err=%v, want only/true/nil", got, ok, err)
	}
	if vers := am.HistoryVersions("fresh"); len(vers) != 0 {
		t.Fatalf("history = %d, want 0", len(vers))
	}
}

// TestHistoryFilenameCrossPlatform: archive filenames must be valid on
// Windows (no ':') - the syscall.Flock #574 lesson applied proactively.
func TestHistoryFilenameCrossPlatform(t *testing.T) {
	am := newTestAutoMemory(t)
	if err := am.SaveMemory("k", "a"); err != nil {
		t.Fatal(err)
	}
	if err := am.SaveMemory("k", "b"); err != nil {
		t.Fatal(err)
	}
	for _, v := range am.HistoryVersions("k") {
		base := filepath.Base(v.Path)
		if strings.ContainsAny(base, `:<>|"*?/\`) {
			t.Fatalf("archive name %q contains Windows-illegal chars", base)
		}
	}
}

// TestCorruptHistoryDegrades: a foreign/corrupt .history entry must not
// break version enumeration or as_of reads.
func TestCorruptHistoryDegrades(t *testing.T) {
	am := newTestAutoMemory(t)
	if err := am.SaveMemory("k", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := am.SaveMemory("k", "v2"); err != nil {
		t.Fatal(err)
	}
	// Foreign file inside .history (neither prefix nor suffix match) and a
	// subdirectory: both must be skipped, not fatal.
	hdir := filepath.Join(am.dir, historyDirName)
	if err := os.WriteFile(filepath.Join(hdir, "README.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(hdir, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}
	if got, _, ok, err := am.ReadMemoryAsOf("k", time.Now()); err != nil || !ok || got != "v2" {
		t.Fatalf("got=%q ok=%v err=%v, want v2/true/nil", got, ok, err)
	}
}
