package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newProvTestMemory(t *testing.T) *AutoMemory {
	t.Helper()
	return &AutoMemory{dir: t.TempDir()}
}

// Provenance: the source label is recorded on first write and survives
// later overwrites of the same key.
func TestRecordProvenanceFirstWriteWins(t *testing.T) {
	am := newProvTestMemory(t)
	am.RecordProvenance("alpha", "save_memory:project")
	am.RecordProvenance("alpha", "run-reflection") // later edit must not move origin
	rec, ok := am.UsageOf("alpha")
	if !ok {
		t.Fatal("expected usage record for alpha")
	}
	if rec.Source != "save_memory:project" {
		t.Fatalf("provenance moved on overwrite: %q", rec.Source)
	}
	if rec.FirstSeen.IsZero() {
		t.Fatal("FirstSeen not set")
	}
}

// SaveMemoryWithSource persists the sidecar; source label flows through.
func TestSaveMemoryWithSourceWritesSidecar(t *testing.T) {
	am := newProvTestMemory(t)
	if err := am.SaveMemoryWithSource("build-process", "content", "save_memory:project"); err != nil {
		t.Fatalf("SaveMemoryWithSource: %v", err)
	}
	rec, ok := am.UsageOf("build-process")
	if !ok || rec.Source != "save_memory:project" {
		t.Fatalf("sidecar source missing: ok=%v rec=%+v", ok, rec)
	}
	// Sidecar must not be picked up as a memory key.
	keys, _ := am.List()
	for _, k := range keys {
		if strings.Contains(k, "usage") {
			t.Fatalf("sidecar leaked into key list: %s", k)
		}
	}
}

// Usage: RecordUse increments Uses; provenanceSuffix only renders once
// there is at least one use.
func TestRecordUseAndSuffix(t *testing.T) {
	am := newProvTestMemory(t)
	am.RecordProvenance("k1", "save_memory:project")
	am.RecordUse([]string{"k1"}, "inline")
	am.RecordUse([]string{"k1", "k1"}, "inline") // same call: one key, one increment
	rec, _ := am.UsageOf("k1")
	if rec.Uses != 1 {
		t.Fatalf("Uses = %d, want 1", rec.Uses)
	}
	if got := provenanceSuffix(rec); got != " [uses=1 src=save_memory:project]" {
		t.Fatalf("suffix = %q", got)
	}
	if got := provenanceSuffix(usageInfo{Uses: 0}); got != "" {
		t.Fatalf("unused entry must render no suffix, got %q", got)
	}
}

// RecordUse debounce: repeated recordings inside usageDebounce are
// suppressed; forgetting the debounce entry lets a new use register.
func TestRecordUseDebounce(t *testing.T) {
	am := newProvTestMemory(t)
	am.RecordUse([]string{"dk"}, "index")
	am.RecordUse([]string{"dk"}, "index") // debounced
	rec, _ := am.UsageOf("dk")
	if rec.Uses != 1 {
		t.Fatalf("debounce failed: Uses = %d, want 1", rec.Uses)
	}
	am.useOnce.Delete("dk")
	am.RecordUse([]string{"dk"}, "index")
	rec, _ = am.UsageOf("dk")
	if rec.Uses != 2 {
		t.Fatalf("post-debounce record failed: Uses = %d, want 2", rec.Uses)
	}
}

// Empty RecordUse / unknown keys are safe no-ops that still create the
// record lazily.
func TestRecordUseEdgeCases(t *testing.T) {
	am := newProvTestMemory(t)
	am.RecordUse(nil, "index")    // no panic
	am.RecordUse([]string{}, "x") // no panic
	rec, ok := am.UsageOf("ghost")
	if ok {
		t.Fatalf("ghost record should not exist: %+v", rec)
	}
}

// ForgetUsage removes the record; DeleteMemory clears provenance too.
func TestForgetUsageViaDeleteMemory(t *testing.T) {
	am := newProvTestMemory(t)
	am.SaveMemoryWithSource("gone", "c", "save_memory:project")
	if _, ok := am.UsageOf("gone"); !ok {
		t.Fatal("precondition: record should exist")
	}
	if err := am.DeleteMemory("gone"); err != nil {
		t.Fatalf("DeleteMemory: %v", err)
	}
	if _, ok := am.UsageOf("gone"); ok {
		t.Fatal("ghost provenance survived DeleteMemory")
	}
}

// Corrupt sidecar resets gracefully instead of erroring out every call.
func TestLoadUsageCorruptFile(t *testing.T) {
	am := newProvTestMemory(t)
	if err := os.WriteFile(filepath.Join(am.dir, usageFileName), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	am.RecordProvenance("fresh", "save_memory:global") // must not panic, must overwrite
	if rec, ok := am.UsageOf("fresh"); !ok || rec.Source != "save_memory:global" {
		t.Fatalf("corrupt sidecar not reset: ok=%v", ok)
	}
}

// LoadIndex surfaces the provenance marker for used keys and counts the
// injection as a use.
func TestLoadIndexProvenanceLine(t *testing.T) {
	am := newProvTestMemory(t)
	am.SaveMemoryWithSource("build", "x", "save_memory:project")
	am.SaveMemoryWithSource("fresh", "y", "save_memory:project")
	// First index build: no prior uses -> bare lines.
	idx, _, err := am.LoadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(idx, "[uses=") {
		t.Fatalf("first index should be bare: %q", idx)
	}
	// Second build: usage from the first injection becomes visible.
	idx, _, err = am.LoadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(idx, "[uses=1 src=save_memory:project]") {
		t.Fatalf("provenance marker missing after injection: %q", idx)
	}
}

// Sidecar JSON round-trips with the version marker.
func TestUsageSidecarSchema(t *testing.T) {
	am := newProvTestMemory(t)
	am.RecordProvenance("s1", "run-reflection")
	am.RecordUse([]string{"s1"}, "inline")
	data, err := os.ReadFile(filepath.Join(am.dir, usageFileName))
	if err != nil {
		t.Fatal(err)
	}
	var idx usageIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatalf("sidecar invalid json: %v", err)
	}
	if idx.Version != usageVersion {
		t.Fatalf("version = %d, want %d", idx.Version, usageVersion)
	}
	if idx.Entries["s1"].Uses != 1 {
		t.Fatalf("uses = %d, want 1", idx.Entries["s1"].Uses)
	}
}

// Concurrent SaveMemoryWithSource + RecordUse + LoadIndex must not race
// or tear the sidecar (complements Test1752SaveMemoryAtomicConcurrent).
func TestUsageConcurrentAccess(t *testing.T) {
	am := newProvTestMemory(t)
	var wg sync.WaitGroup
	deadline := time.Now().Add(2 * time.Second)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				am.SaveMemoryWithSource("shared", "payload", "save_memory:project")
				am.RecordUse([]string{"shared"}, "inline")
				_, _, _ = am.LoadIndex()
			}
		}(g)
	}
	wg.Wait()
	if time.Now().After(deadline.Add(30 * time.Second)) {
		t.Fatal("deadline guard")
	}
	rec, ok := am.UsageOf("shared")
	if !ok || rec.Uses == 0 {
		t.Fatalf("concurrent usage lost: ok=%v rec=%+v", ok, rec)
	}
	if _, err := os.Stat(filepath.Join(am.dir, "shared.md")); err != nil {
		t.Fatalf("memory file missing after concurrent writes: %v", err)
	}
}
