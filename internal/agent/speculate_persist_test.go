package agent

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

	s.recordObservation("glob")
	maybePersistSpecPatterns(s)
	data2, _ := os.ReadFile(path)
	if string(data2) != string(data) {
		t.Fatal("save must be throttled inside the interval")
	}

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
		model[root] = map[string]int{"next": i + 1}
	}
	trimmed := topTransitions(model, 10)
	if len(trimmed) != 10 {
		t.Fatalf("expected 10 roots after trim, got %d", len(trimmed))
	}

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

	small := map[string]map[string]int{"a": {"b": 1}}
	if got := topTransitions(small, 500); len(got) != 1 {
		t.Fatal("small model must pass through untrimmed")
	}
}

func TestSpecPersist_ConcurrentSaves(t *testing.T) {
	isolateSpecHome(t)
	specLastPersist = time.Time{}

	s := newSpeculator()
	s.recordObservation("grep")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			maybePersistSpecPatterns(s)
			_ = s.predictNext("grep")
		}()
	}
	wg.Wait()

	if data, err := os.ReadFile(specPatternsPath()); err == nil {
		var m map[string]map[string]int
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("racing save produced invalid JSON: %v", err)
		}
	}
}

var _ = config.ConfigDir

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

}
