package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func newSidecarTestMemory(t *testing.T, dir string) *AutoMemory {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return &AutoMemory{dir: dir}
}

func TestIssue3120_RecordUseCrossInstanceNoLoss(t *testing.T) {
	dir := t.TempDir()
	a := newSidecarTestMemory(t, dir)
	b := newSidecarTestMemory(t, dir)

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				a.RecordUse([]string{"alpha"}, "index")
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				b.RecordUse([]string{"beta"}, "inline")
			}
		}()
	}
	wg.Wait()

	idx := a.loadUsage()
	if idx.Entries["alpha"] == nil {
		t.Fatal("alpha entry lost to cross-instance last-write-wins (#3120 bug)")
	}
	if idx.Entries["beta"] == nil {
		t.Fatal("beta entry lost to cross-instance last-write-wins (#3120 bug)")
	}
}

func TestIssue3120_RecordUseVsProvenanceNoLoss(t *testing.T) {
	dir := t.TempDir()
	main := newSidecarTestMemory(t, dir)
	sub := newSidecarTestMemory(t, dir)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			main.RecordUse([]string{"shared-key"}, "index")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {

			if err := sub.SaveMemoryWithSource("note-"+string(rune('a'+i)), "body", "test"); err != nil {
				t.Errorf("save: %v", err)
			}
		}
	}()
	wg.Wait()

	idx := main.loadUsage()
	if idx.Entries["shared-key"] == nil {
		t.Fatal("shared-key use entry lost to provenance writes (#3120 bug)")
	}
	for i := 0; i < 20; i++ {
		k := "note-" + string(rune('a'+i))
		rec, ok := idx.Entries[k]
		if !ok || rec.Source == "" {
			t.Fatalf("provenance entry %q lost or sourceless: %+v", k, rec)
		}
	}
}

func TestIssue3120_SidecarIsAtomicJSON(t *testing.T) {
	dir := t.TempDir()
	a := newSidecarTestMemory(t, dir)
	b := newSidecarTestMemory(t, dir)

	var wg sync.WaitGroup
	for w := 0; w < 2; w++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				a.RecordUse([]string{"k1"}, "index")
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				b.RecordUse([]string{"k2"}, "inline")
			}
		}()
	}
	wg.Wait()

	data, err := os.ReadFile(filepath.Join(dir, ".usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("sidecar torn (not valid JSON): %v", err)
	}
}
