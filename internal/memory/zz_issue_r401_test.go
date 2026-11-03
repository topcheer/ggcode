package memory

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestAutoMemoryCrossInstanceConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	const writers = 4
	const perWriter = 10
	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			am := &AutoMemory{dir: dir}
			for i := 0; i < perWriter; i++ {
				if err := am.SaveMemory("r401-key", "w"); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent save failed: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "r401-key.md"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(got) == 0 || strings.TrimSpace(string(got)) == "" {
		t.Fatalf("torn/empty write: %q", got)
	}
}

func TestAutoMemoryLockFileCreatedAndReleased(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	if err := am.SaveMemory("r401-lock", "v1"); err != nil {
		t.Fatal(err)
	}

	lock := dir + ".lock"
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("expected lock file at %s: %v", lock, err)
	}

	if err := am.SaveMemory("r401-lock2", "v2"); err != nil {
		t.Fatalf("second save after unlock: %v", err)
	}
}
