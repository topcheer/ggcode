//go:build goolm

package agent

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestIssue3925_ConcurrentRouteKeepsBothIncrements(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(dir, ".ggcode", weaknessStoreFile)
	seed := weaknessSignalStore{
		"test-fail:pkg/a.TestX": {Class: WeakRare, Count: 1, LastTS: time.Now()},
	}
	if err := saveWeaknessStore(storePath, seed); err != nil {
		t.Fatal(err)
	}

	run := func() {
		a := NewAgent(nil, nil, "sys", 5)
		a.workingDir = dir
		a.testFails = newTestFailCollector()
		a.testFails.record("go test ./...", "--- FAIL: TestX\nFAIL\tpkg/a\t0.1s\n")
		a.routeWeaknessSignals()
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); run() }()
	}
	wg.Wait()

	final := loadWeaknessStore(storePath)
	if rec, ok := final["test-fail:pkg/a.TestX"]; !ok || rec.Count != 3 {
		t.Fatalf("both concurrent increments must survive (seed 1 + 2), got %+v (found=%v)", rec, ok)
	}
}

func TestIssue3925_StandAloneWrapperStillRoutes(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := weaknessSignalStore{
		"errcat:cmdfail": {Class: WeakBoundary, Count: 2, Evidence: "e"},
	}
	routeMatureSignals(dir, store)
	if !store["errcat:cmdfail"].Routed {
		t.Fatal("wrapper must still route mature signals")
	}
}
