package agent

// #3642 probes: cross-session pruneStaleSpillDirs must not remove spill
// dirs owned by live processes, no matter how old; dead-PID dirs >24h and
// legacy dirs >7d must still be reaped.

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestIssue3642_SpillDirPIDParsing(t *testing.T) {
	if pid, ok := spillDirPID("ggcode-spill-12345-abc123"); !ok || pid != 12345 {
		t.Fatalf("PID-format dir must parse, got %d %v", pid, ok)
	}
	if _, ok := spillDirPID("ggcode-spill-abc123"); ok {
		t.Fatal("legacy random-suffix dir must not parse as PID")
	}
	if _, ok := spillDirPID("ggcode-spill-0-xyz"); ok {
		t.Fatal("pid 0 is invalid")
	}
}

func TestIssue3642_LiveOwnerNeverPruned(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	// Simulate: own PID dir with ancient ModTime (the >24h long session).
	dir := filepath.Join(tmp, "ggcode-spill-"+strconv.Itoa(os.Getpid())+"-probe")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	spill := filepath.Join(dir, "000001.000-grep.txt")
	if err := os.WriteFile(spill, []byte("promised recoverable output"), 0o600); err != nil {
		t.Fatal(err)
	}
	ancient := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(dir, ancient, ancient); err != nil {
		t.Fatal(err)
	}

	pruneStaleSpillDirs()
	if _, err := os.Stat(spill); err != nil {
		t.Fatalf("live-owner spill dir was pruned despite owner being this very process: %v", err)
	}
}

func TestIssue3642_DeadOwnerPrunedAfter24h(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	// PID 2^22-1 range: use a PID that is essentially guaranteed dead
	// (kernel max PID + guarded by Atoi bounds). Use a large implausible PID.
	dir := filepath.Join(tmp, "ggcode-spill-999999999-probe")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ancient := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(dir, ancient, ancient); err != nil {
		t.Fatal(err)
	}

	pruneStaleSpillDirs()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dead-owner >24h dir must be reaped, got err=%v", err)
	}
}

func TestIssue3642_LegacyDirKeptUnder7Days(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	dir := filepath.Join(tmp, "ggcode-spill-oldrand")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * time.Hour) // >24h but <7d
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}

	pruneStaleSpillDirs()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("legacy dir <7d must be kept (owner unknowable), err=%v", err)
	}
}
