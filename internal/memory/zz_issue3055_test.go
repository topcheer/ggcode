package memory

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

	if err := am.ApplySupersession("new-key", []string{"old-key"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	before := am.SupersededSet()
	if !before["old-key"] {
		t.Fatal("setup: retirement record must be visible before the race")
	}

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

	if s := (&AutoMemory{dir: dir}).SupersededSet(); !s["old-key"] {
		t.Fatal("final sidecar state lost the retirement record")
	}

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
