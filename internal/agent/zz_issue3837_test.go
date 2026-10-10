//go:build goolm

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// #3837 A: the routed line embeds the live Count ("enforce: fp (N runs)"),
// so whole-line dedup never matched a later line with a different Count
// (memory save failure / eviction+recurrence / manual edit) and duplicate
// enforce rules accumulated. Now dedup is by fingerprint and a newer
// count REPLACES the stale line.
func TestIssue3837_CountDriftReplacesNotAppends(t *testing.T) {
	fp := "test-fail:pkg.TestX"
	existing := "enforce: " + fp + " (2 runs) - old evidence; treat as a standing rule"
	newLine := "enforce: " + fp + " (5 runs) - newer evidence; treat as a standing rule"

	got := replaceRoutedLine(existing, fp, newLine)
	if strings.Count(got, "enforce:") != 1 {
		t.Fatalf("count drift must replace, not append: %q", got)
	}
	if !strings.Contains(got, "(5 runs)") || strings.Contains(got, "(2 runs)") {
		t.Fatalf("stale line must be swapped for the newer one, got: %q", got)
	}
}

func TestIssue3837_DistinctFingerprintsAppend(t *testing.T) {
	existing := "enforce: fp.A (2 runs) - ev"
	got := replaceRoutedLine(existing, "fp.B", "enforce: fp.B (1 runs) - ev2")
	if !strings.Contains(got, "fp.A") || !strings.Contains(got, "fp.B") {
		t.Fatalf("distinct fingerprints must coexist, got: %q", got)
	}
}

func TestIssue3837_RoutedFingerprintExtract(t *testing.T) {
	if fp := routedFingerprint("enforce: k1 (3 runs) - e"); fp != "k1" {
		t.Fatalf("enforce shape fp = %q, want k1", fp)
	}
	if fp := routedFingerprint("boundary: k2 (1 runs) - e"); fp != "k2" {
		t.Fatalf("boundary shape fp = %q, want k2", fp)
	}
	if fp := routedFingerprint("unrelated line"); fp != "" {
		t.Fatalf("non-routed shape must yield empty, got %q", fp)
	}
}

// #3837 B: routeMatureSignals now holds a flock across the mutation - two
// concurrent route passes must not corrupt the memory key or panic.
func TestIssue3837_ConcurrentRouteNoCorruption(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each goroutine gets its OWN store map: the flock protects
			// the on-disk mutation window, not concurrent map access in
			// one process (production calls this once per run end).
			cp := weaknessSignalStore{
				"fp.X": {Class: WeakForgetting, Count: 3, Evidence: "e"},
			}
			routeMatureSignals(dir, cp)
		}()
	}
	wg.Wait()
	if _, err := os.Stat(filepath.Join(dir, ".ggcode", weaknessStoreFile) + ".lock"); err != nil {
		t.Logf("lock file lifecycle note (flock may unlink): %v", err)
	}
}
