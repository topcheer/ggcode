package memory

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// #1752 case 2: SaveMemory must be atomic (temp+rename) and safe under
// concurrent writers - no torn files, no lost final state.
func Test1752SaveMemoryAtomicConcurrent(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	const writers = 16
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			content := "writer-payload-"
			for j := 0; j < 200; j++ {
				content += "x"
			}
			_ = am.SaveMemory("race-key", content)
		}(i)
	}
	wg.Wait()

	// Final read: a complete payload, never a torn/partial file.
	data, err := os.ReadFile(filepath.Join(dir, "race-key.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || len(data) != len("writer-payload-")+200 {
		t.Fatalf("torn write: got %d bytes", len(data))
	}

	// No temp residue: the directory holds only the final file.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "race-key.md" {
			t.Fatalf("temp residue left behind: %s", e.Name())
		}
	}
}
