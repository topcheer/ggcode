package agent

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// #1512 case C pin: concurrent persistLocked from two states (simulating
// two Agent instances on one workspace) must not lose entries.
func Test1512TrajConcurrentPersistNoLostUpdate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trajectory-learnings.jsonl")

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			s := &trajIntelState{filePath: path}
			s.learnings = []trajectoryLearning{{Insight: "learning"}}
			if err := s.persistLocked(); err != nil {
				t.Errorf("worker %d persist: %v", w, err)
			}
		}(w)
	}
	wg.Wait()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines != 4 {
		t.Fatalf("lost update: 4 concurrent persists must yield 4 entries, got %d", lines)
	}
	// No residual tmp files from interleaved writers.
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("residual tmp file leaked: %s", e.Name())
		}
	}
}
