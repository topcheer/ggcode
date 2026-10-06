package agent

// speculate_persist_test.go -- r486 persistence probes.
//
// Covers: disk model seeds a fresh speculator (cold-start warmup), corrupt
// JSON degrades to empty, merge never invents zero/negative counts, save
// throttling coalesces bursts, and top-N trimming keeps the highest-count
// edges. HOME is isolated because ConfigDir() is testguarded.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func isolateSpecHome(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	// specLoadOnce is process-wide; re-arm per test so on-disk variations
	// are not order-dependent, and restore the armed state afterwards.
	resetSpecPersistForTest()
	t.Cleanup(resetSpecPersistForTest)
	return specPatternsPath()
}

func writeSpecPatterns(t *testing.T, path string, model map[string]map[string]int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSpecPersist_LoadSeedsNewSpeculator(t *testing.T) {
	path := isolateSpecHome(t)
	writeSpecPatterns(t, path, map[string]map[string]int{
		"grep": {"read_file": 7},
	})

	s := newSpeculator()
	s.mu.Lock()
	got := s.patterns["grep"]["read_file"]
	s.mu.Unlock()
	if got != 7 {
		t.Fatalf("persisted count not merged: got %d, want 7", got)
	}
	// Cold-start prediction must be immediately actionable: adaptiveMinCount
	// starts at 2 and the seeded count exceeds it.
	nexts := s.predictNext("grep")
	if len(nexts) == 0 || nexts[0] != "read_file" {
		t.Fatalf("seeded model must predict read_file after grep, got %v", nexts)
	}
}

func TestSpecPersist_CorruptJSONDegradesToEmpty(t *testing.T) {
	path := isolateSpecHome(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newSpeculator()
	s.mu.Lock()
	n := len(s.patterns)
	s.mu.Unlock()
	if n != 0 {
		t.Fatalf("corrupt file must degrade to empty model, got %d roots", n)
	}
	// recordObservation on the degraded model still works (fresh start).
	// Two rounds clear the conservative adaptiveMinCount=2 threshold.
	for i := 0; i < 2; i++ {
		s.recordObservation("grep")
		s.recordObservation("read_file")
	}
	nexts := s.predictNext("grep")
	if len(nexts) == 0 {
		t.Fatal("degraded model must keep learning normally")
	}
}

func TestSpecPersist_MergeSkipsNonPositiveCounts(t *testing.T) {
	path := isolateSpecHome(t)
	writeSpecPatterns(t, path, map[string]map[string]int{
		"grep": {"read_file": 3, "edit_file": 0},
	})

	s := newSpeculator()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.patterns["grep"]["edit_file"]; ok {
		t.Fatal("zero counts must not be merged")
	}
	if s.patterns["grep"]["read_file"] != 3 {
		t.Fatal("positive counts must be merged")
	}
}

func TestSpecPersist_SaveThrottleAndRoundTrip(t *testing.T) {
	path := isolateSpecHome(t)
	reset := func() {
		specLastPersist = time.Time{}
	}
	reset()

	s := newSpeculator()
	s.recordObservation("grep")
	s.recordObservation("read_file")

	maybePersistSpecPatterns(s)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("first save must write the file: %v", err)
	}
	var onDisk map[string]map[string]int
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatalf("saved file must be valid JSON: %v", err)
	}
	if onDisk["grep"]["read_file"] != 1 {
		t.Fatalf("round trip lost counts: %v", onDisk)
	}

	// Throttled: mutate the model, persist again immediately - the disk
	// must still hold the FIRST snapshot (within the same interval).
	s.recordObservation("glob")
	maybePersistSpecPatterns(s)
	data2, _ := os.ReadFile(path)
	if string(data2) != string(data) {
		t.Fatal("save must be throttled inside the interval")
	}

	// Outside the interval it saves again. The new observation chains from
	// the LAST observed tool (bigram), so glob follows read_file.
	reset()
	maybePersistSpecPatterns(s)
	data3, _ := os.ReadFile(path)
	var onDisk3 map[string]map[string]int
	_ = json.Unmarshal(data3, &onDisk3)
	if onDisk3["read_file"]["glob"] != 1 {
		t.Fatalf("post-interval save must include new counts: %v", onDisk3)
	}
}

func TestSpecPersist_TopTransitionsTrims(t *testing.T) {
	model := map[string]map[string]int{}
	for i := 0; i < 40; i++ {
		root := "t" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		model[root] = map[string]int{"next": i + 1} // counts 1..40
	}
	trimmed := topTransitions(model, 10)
	if len(trimmed) != 10 {
		t.Fatalf("expected 10 roots after trim, got %d", len(trimmed))
	}
	// Highest counts survive: root with count 40 must be present.
	found := false
	for _, nexts := range trimmed {
		for _, c := range nexts {
			if c == 40 {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("highest-count edge must survive trimming")
	}
	// Below the limit the map is returned untouched.
	small := map[string]map[string]int{"a": {"b": 1}}
	if got := topTransitions(small, 500); len(got) != 1 {
		t.Fatal("small model must pass through untrimmed")
	}
}

func TestSpecPersist_ConcurrentSaves(t *testing.T) {
	isolateSpecHome(t)
	specLastPersist = time.Time{} // force eligibility

	s := newSpeculator()
	s.recordObservation("grep")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			maybePersistSpecPatterns(s) // first wins, rest throttled
			_ = s.predictNext("grep")   // concurrent reader
		}()
	}
	wg.Wait()
	// No race under -race and the file (if written) is valid JSON.
	if data, err := os.ReadFile(specPatternsPath()); err == nil {
		var m map[string]map[string]int
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("racing save produced invalid JSON: %v", err)
		}
	}
}

// ConfigDir reference keeps the config import honest if assertions change.
var _ = config.ConfigDir

// Run-end save wiring (r486): the maybePersistSpecPatterns defer mounted in
// RunStreamWithContent must fire after a real run completes. Mirrors the
// r485 lesson - unit probes cannot see hand-located mount points - so this
// drives an actual RunStream and asserts the model reached the disk.
func TestSpecPersist_RunEndSaveWiring(t *testing.T) {
	path := isolateSpecHome(t)
	mp := &mockProvider{chatResponses: []*provider.ChatResponse{
		readOnlyToolTurn("c1"), textTurn("done"),
	}}
	registry := tool.NewRegistry()
	if err := registry.Register(mockTool{name: "read_file", result: tool.Result{Content: "ok"}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	a := NewAgent(mp, registry, "", 5)
	if err := a.RunStream(context.Background(), "hi", func(provider.StreamEvent) {}); err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("run end must persist the model to disk: %v", err)
	}
	var m map[string]map[string]int
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("persisted model must be valid JSON: %v", err)
	}
	// Load wiring is exercised implicitly: isolateSpecHome re-armed the
	// one-shot load and this test's own NewAgent (via newSpeculator) ran it
	// against the empty file; the save here writes what the run observed.
}
