package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRealStoreStartupLoad measures the exact startup path this fix targets:
// maintenance triggered (like any startup List()) while a Load(resumeID) for
// the real 500MB+ session comes in. Build tag keeps it out of normal runs.
//
//	go test -tags 'goolm perfreal' -run TestRealStoreStartupLoad ./internal/session/ -v -count=3
//
// Requires GGCODE_REAL_STORE=1 to touch the real store (read-only + index
// reconciliation, same as production maintenance).
func TestRealStoreStartupLoad(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".ggcode", "sessions")
	if os.Getenv("GGCODE_REAL_STORE") != "1" {
		t.Skip("set GGCODE_REAL_STORE=1 to run against the real session store")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("no real store at %s: %v", dir, err)
	}
	const resumeID = "20260719-183709-4839c282ad869c5f"
	if _, err := os.Stat(filepath.Join(dir, resumeID+".jsonl")); err != nil {
		t.Skipf("target session file missing: %v", err)
	}

	s, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	listDone := make(chan struct{})
	go func() {
		defer close(listDone)
		start := time.Now()
		if _, err := s.List(); err != nil {
			t.Logf("List err: %v", err)
		}
		t.Logf("List (triggers maintenance) took %v", time.Since(start))
	}()

	// Let the maintenance goroutine enter its scan, like a real startup where
	// UI warmup fires List() before repl.Load(resumeID).
	time.Sleep(10 * time.Millisecond)

	start := time.Now()
	ses, err := s.Load(resumeID)
	loadDur := time.Since(start)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Logf("Load(%s) took %v msgs=%d", resumeID, loadDur, len(ses.Messages))
	if loadDur > 10*time.Second {
		t.Errorf("REGRESSION: Load blocked %v behind maintenance (>10s)", loadDur)
	}

	select {
	case <-listDone:
	case <-time.After(120 * time.Second):
		t.Fatal("List/maintenance did not finish in 120s")
	}
}
