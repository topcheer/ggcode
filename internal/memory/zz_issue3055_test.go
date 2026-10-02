package memory

// Regression probes for #3055:
//   - C1: the supersede sidecar must be written atomically (temp+rename) -
//     a torn JSON read made loadSuperseded silently degrade to an empty
//     index: retired memories resurrected into the prompt filter and the
//     next ApplySupersession permanently wiped the retirement records.
//   - C2: a Stat failure other than NotExist (EACCES parent) must still
//     count the referenced path as broken.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestIssue3055_C1_ConcurrentLoadNeverSeesTornSidecar(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	// Establish a real retirement record.
	if err := am.ApplySupersession("new-key", []string{"old-key"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	before := am.SupersededSet()
	if !before["old-key"] {
		t.Fatal("setup: retirement record must be visible before the race")
	}

	// Hammer concurrent save+load: with the old plain WriteFile a reader
	// could observe a half-written file, lose the index, and the loop's
	// next ApplySupersession rewrote the sidecar empty (records lost
	// forever). With temp+rename the reader sees either the old or the new
	// complete file.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if s := (&AutoMemory{dir: dir}).SupersededSet(); !s["old-key"] {
					close(stop)
					return
				}
			}
		}
	}()
	for i := 0; i < 50; i++ {
		if err := am.ApplySupersession("new-key", []string{"old-key"}); err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
	}
	select {
	case <-stop:
		t.Fatal("#3055-C1: concurrent load lost the retirement index (torn write)")
	default:
	}
	close(stop)
	wg.Wait()

	// And the final on-disk state keeps the record.
	if s := (&AutoMemory{dir: dir}).SupersededSet(); !s["old-key"] {
		t.Fatal("final sidecar state lost the retirement record")
	}
	// No temp-file litter.
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file leaked: %s", e.Name())
		}
	}
}

func TestIssue3055_C2_StatErrorCountsAsBroken(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions; EACCES unreachable")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(locked, "secret.txt")
	if err := os.WriteFile(inner, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	// Unit-level: findBrokenPaths is the exact function whose Stat gate C2
	// changes; an absolute locked path must be reported as broken.
	broken := findBrokenPaths("See details at "+inner+" for the full breakdown.", dir)
	found := false
	for _, p := range broken {
		if strings.Contains(p, "secret.txt") {
			found = true
		}
	}
	if !found {
		t.Fatal("#3055-C2: EACCES-blocked Stat must still report the path as broken")
	}
}
