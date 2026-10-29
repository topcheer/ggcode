package agent

// sa-112 weakness-signal router acceptance tests (CoEvolve-inspired
// forgetting/boundary/rare taxonomy -> cross-run store -> project memory).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func weaknessTestAgent(t *testing.T) (*Agent, string) {
	t.Helper()
	dir := t.TempDir()
	a := &Agent{
		workingDir:        dir,
		driftRecurrence:   newDriftRecurrenceState(),
		constraintAmnesia: newConstraintAmnesiaState(),
		repetition:        newRepetitionTracker(),
		errorClassifier:   NewErrorClassifier(),
	}
	return a, dir
}

func weaknessStorePath(dir string) string {
	return filepath.Join(dir, ".ggcode", weaknessStoreFile)
}

func readWeaknessMemory(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".ggcode", "memory", weaknessMemoryKey+".md"))
	if err != nil {
		return ""
	}
	return string(b)
}

// Acceptance 1: same-fingerprint failure across two runs classifies as
// forgetting and routes an "enforce:" line into project memory on the
// second run.
func TestWeaknessForgetTwoRunsRoutesEnforce(t *testing.T) {
	a, dir := weaknessTestAgent(t)
	for run := 0; run < 2; run++ {
		a.driftRecurrence.fired = true
		a.routeWeaknessSignals()
	}
	mem := readWeaknessMemory(t, dir)
	if !strings.Contains(mem, "enforce: drift-recurrence (2 runs)") {
		t.Fatalf("expected enforce line after two runs, got:\n%s", mem)
	}
	store := loadWeaknessStore(weaknessStorePath(dir))
	if rec := store["drift-recurrence"]; rec.Count != 2 || rec.Class != WeakForgetting || !rec.Routed {
		t.Fatalf("store record = %+v, want Count=2 WeakForgetting Routed", rec)
	}
	// Third run must NOT duplicate the line (Routed flag sticky).
	a.driftRecurrence.fired = true
	a.routeWeaknessSignals()
	if strings.Count(readWeaknessMemory(t, dir), "enforce: drift-recurrence") != 1 {
		t.Fatal("routed line duplicated across runs")
	}
}

// Acceptance 2: the same error-classifier category firing in two runs
// classifies as boundary and routes a skill-candidate line.
func TestWeaknessBoundaryByCategory(t *testing.T) {
	a, dir := weaknessTestAgent(t)
	for run := 0; run < 2; run++ {
		a.errorClassifier.mu.Lock()
		a.errorClassifier.fired["shell_compat"] = true
		a.errorClassifier.mu.Unlock()
		a.routeWeaknessSignals()
	}
	mem := readWeaknessMemory(t, dir)
	if !strings.Contains(mem, "boundary: errcat:shell_compat (2 runs)") {
		t.Fatalf("expected boundary line, got:\n%s", mem)
	}
}

// Acceptance 3: an isolated single failed edit is rare - counted in the
// store, never routed to memory.
func TestWeaknessRareArchiveOnly(t *testing.T) {
	a, dir := weaknessTestAgent(t)
	a.repetition.mu.Lock()
	a.repetition.failedEditsByFile["main.go"] = 1
	a.repetition.mu.Unlock()
	a.routeWeaknessSignals()

	store := loadWeaknessStore(weaknessStorePath(dir))
	fp := "edit-fail:main.go"
	rec, ok := store[fp]
	if !ok || rec.Class != WeakRare || rec.Count != 1 {
		t.Fatalf("rare record missing/wrong: %+v ok=%v", rec, ok)
	}
	if mem := readWeaknessMemory(t, dir); mem != "" {
		t.Fatalf("rare must not route, got memory:\n%s", mem)
	}

	// Recurrence upgrades rare -> forgetting (a rare failure that repeats
	// is no longer rare) and then routes.
	a.repetition.mu.Lock()
	a.repetition.failedEditsByFile["main.go"] = 2
	a.repetition.mu.Unlock()
	a.routeWeaknessSignals()
	store = loadWeaknessStore(weaknessStorePath(dir))
	if rec := store[fp]; rec.Class != WeakForgetting || !rec.Routed {
		t.Fatalf("expected upgrade to forgetting+Routed, got %+v", rec)
	}
}

// Acceptance 4: corrupt store degrades gracefully - empty store, no panic,
// routing still works.
func TestWeaknessStoreCorruptFallback(t *testing.T) {
	a, dir := weaknessTestAgent(t)
	if err := os.MkdirAll(filepath.Dir(weaknessStorePath(dir)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(weaknessStorePath(dir), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	a.driftRecurrence.fired = true
	a.routeWeaknessSignals() // must not panic

	store := loadWeaknessStore(weaknessStorePath(dir))
	if store["drift-recurrence"].Count != 1 {
		t.Fatalf("fresh count expected after corrupt store, got %+v", store)
	}
}

// Acceptance 5: stale fingerprints age out at load; over-cap stores evict
// oldest first.
func TestWeaknessAgingAndEviction(t *testing.T) {
	dir := t.TempDir()
	path := weaknessStorePath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	stale := weaknessRecord{Class: WeakForgetting, Count: 5, LastTS: time.Now().Add(-40 * 24 * time.Hour), Routed: true}
	fresh := weaknessRecord{Class: WeakRare, Count: 1, LastTS: time.Now()}
	b, _ := json.Marshal(weaknessSignalStore{"stale-fp": stale, "fresh-fp": fresh})
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Fatal(err)
	}
	store := loadWeaknessStore(path)
	if _, ok := store["stale-fp"]; ok {
		t.Fatal("stale fingerprint (40d) must age out at load")
	}
	if _, ok := store["fresh-fp"]; !ok {
		t.Fatal("fresh fingerprint must survive")
	}

	// Eviction: 3 entries, cap 2 -> oldest dropped.
	big := weaknessSignalStore{}
	base := time.Now()
	for i, fp := range []string{"a", "b", "c"} {
		big[fp] = weaknessRecord{Class: WeakRare, Count: 1, LastTS: base.Add(time.Duration(i) * time.Hour)}
	}
	evictWeaknessStore(big, 2)
	if len(big) != 2 {
		t.Fatalf("eviction to cap failed: %d entries", len(big))
	}
	if _, ok := big["a"]; ok {
		t.Fatal("oldest entry must be evicted first")
	}
}

// r17: one go-test failure sighting in a run seeds WeakRare; rerunning the
// same failing test within one run (2 sightings) is WeakForgetting - the
// agent was told and did not adapt.
func TestTestFailSightingClassification(t *testing.T) {
	out := "--- PASS: TestOther (0.00s)\n--- FAIL: TestWidget (0.01s)\nFAIL\n"
	a, dir := weaknessTestAgent(t)
	a.testFails = newTestFailCollector()
	a.testFails.record("go test ./...", out) // single sighting
	a.routeWeaknessSignals()
	store := loadWeaknessStore(weaknessStorePath(dir))
	if rec := store["test-fail:TestWidget"]; rec.Count != 1 || rec.Class != WeakRare {
		t.Fatalf("single sighting: store record = %+v, want Count=1 WeakRare", rec)
	}
	// Route-end reset: collector must be empty for the next run.
	if n := len(a.testFails.snapshot()); n != 0 {
		t.Fatalf("collector not reset after route: %d entries", n)
	}
	// Rerun in one run: same test fails again -> WeakForgetting + enforce line.
	a.testFails.record("go test ./...", out)
	a.testFails.record("go test ./...", out)
	a.routeWeaknessSignals()
	store = loadWeaknessStore(weaknessStorePath(dir))
	rec := store["test-fail:TestWidget"]
	if rec.Count != 2 || rec.Class != WeakForgetting || !rec.Routed {
		t.Fatalf("rerun sighting: store record = %+v, want Count=2 WeakForgetting Routed", rec)
	}
	if mem := readWeaknessMemory(t, dir); !strings.Contains(mem, "enforce: test-fail:TestWidget") {
		t.Fatalf("expected enforce line for rerun test failure, got:\n%s", mem)
	}
}
