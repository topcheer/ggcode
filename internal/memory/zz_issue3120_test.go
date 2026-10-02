package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// #3120 probe: two AutoMemory instances on the SAME dir (a parent agent
// and a subagent in another runtime, or two ggcode processes on one
// checkout) interleave RecordUse and SaveMemory+RecordProvenance. Before
// the fix, RecordUse held only the in-process am.mu, so its
// load-modify-save of .usage.json raced the locked save path's
// RecordProvenance last-write-wins: use counts or provenance entries
// silently vanished. The per-path file lock must serialize both.

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
	b := newSidecarTestMemory(t, dir) // same dir, independent am.mu

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

	// Both keys must survive. RecordUse's useOnce debounce is
	// per-INSTANCE, so each instance contributes exactly 1 use for its
	// key; the probe's power is EXISTENCE: last-write-wins interleaving
	// (the #3120 bug) drops one instance's whole entry because each
	// load-modify-save carried only its own writer's view.
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
			// The real locked save path (SaveMemoryWithSource acquires the
			// dir FileLock, then calls RecordProvenance inside it).
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

// TestIssue3120_SidecarIsAtomicJSON pins the sidecar file is always valid
// JSON even mid-race (atomic rename underneath the lock).
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
