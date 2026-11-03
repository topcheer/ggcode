package agentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSectionCollectorConcurrentRefresh(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/x\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sc := &SectionCollector{working: root}
	done := make(chan struct{})
	go func() { sc.refresh(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("refresh hung")
	}
	snap := sc.Snapshot()
	if !strings.Contains(snap.Overview, "go.mod") {
		t.Errorf("overview missing go.mod: %q", snap.Overview)
	}
	if sc.idle {
		t.Error("first refresh should not be idle")
	}

	go func() { sc.refresh() }()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		sc.mu.RLock()
		idle := sc.idle
		sc.mu.RUnlock()
		if idle {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	sc.mu.RLock()
	idle := sc.idle
	sc.mu.RUnlock()
	if !idle {
		t.Error("second unchanged refresh should be idle")
	}
}

func TestInitGlobalSectionCollectorBudget(t *testing.T) {
	root := t.TempDir()
	InitGlobalSectionCollector(root)
	defer StopGlobalSectionCollector()

	snap, ok := GlobalSectionSnapshot()
	if !ok {
		t.Fatal("no snapshot after init")
	}
	_ = snap
}
