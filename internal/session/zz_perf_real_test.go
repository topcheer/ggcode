package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
