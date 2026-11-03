package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

func TestSpeculator_BigramLearning(t *testing.T) {
	s := newSpeculator()

	for i := 0; i < 3; i++ {
		s.recordObservation("edit_file")
		s.recordObservation("read_file")
	}

	preds := s.predictNext("edit_file")
	if len(preds) == 0 {
		t.Fatal("expected prediction after 3 observations of edit_file → read_file")
	}
	if preds[0] != "read_file" {
		t.Errorf("expected first prediction to be read_file, got %s", preds[0])
	}
}

func TestSpeculator_NoPredictionWithInsufficientData(t *testing.T) {
	s := newSpeculator()

	s.recordObservation("edit_file")
	s.recordObservation("read_file")

	preds := s.predictNext("edit_file")
	if len(preds) != 0 {
		t.Fatalf("expected no predictions with 1 observation, got %v", preds)
	}
}

func TestSpeculator_OnlyReadOnlyPredictions(t *testing.T) {
	s := newSpeculator()

	for i := 0; i < 5; i++ {
		s.recordObservation("edit_file")
		s.recordObservation("write_file")
	}

	preds := s.predictNext("edit_file")

	for _, p := range preds {
		if p == "write_file" {
			t.Fatal("write_file should not be predicted (has side effects)")
		}
	}
}

func TestSpeculator_PredictionOrdering(t *testing.T) {
	s := newSpeculator()

	for i := 0; i < 5; i++ {
		s.recordObservation("edit_file")
		s.recordObservation("grep")
	}
	s.lastTool = ""
	for i := 0; i < 3; i++ {
		s.recordObservation("edit_file")
		s.recordObservation("read_file")
	}

	preds := s.predictNext("edit_file")
	if len(preds) < 2 {
		t.Fatalf("expected at least 2 predictions, got %d", len(preds))
	}

	if preds[0] != "grep" {
		t.Errorf("expected grep first (count=5), got %s", preds[0])
	}
}

func TestSpeculator_CacheStoreAndGet(t *testing.T) {
	s := newSpeculator()

	args := json.RawMessage(`{"path":"/test/file.go"}`)
	s.store("read_file", args, mockToolResult("file content"), "", 0, 0)

	result, hit := s.getCached("read_file", args)
	if !hit {
		t.Fatal("expected cache hit after store")
	}
	if result.Content != "file content" {
		t.Errorf("expected 'file content', got '%s'", result.Content)
	}
}

func TestSpeculator_CacheMissOnDifferentArgs(t *testing.T) {
	s := newSpeculator()

	s.store("read_file", json.RawMessage(`{"path":"/a.go"}`), mockToolResult("a"), "", 0, 0)

	_, hit := s.getCached("read_file", json.RawMessage(`{"path":"/b.go"}`))
	if hit {
		t.Fatal("expected cache miss with different args")
	}
}

func TestSpeculator_CacheExpiry(t *testing.T) {
	s := newSpeculator()
	s.ttl = 50 * time.Millisecond

	args := json.RawMessage(`{"path":"/test.go"}`)
	s.store("read_file", args, mockToolResult("content"), "", 0, 0)

	_, hit := s.getCached("read_file", args)
	if !hit {
		t.Fatal("expected cache hit immediately after store")
	}

	time.Sleep(100 * time.Millisecond)

	_, hit = s.getCached("read_file", args)
	if hit {
		t.Fatal("expected cache miss after TTL expiry")
	}
}

func TestSpeculator_ResetSequence(t *testing.T) {
	s := newSpeculator()

	s.recordObservation("edit_file")
	s.recordObservation("read_file")

	s.resetSequence()

	s.recordObservation("grep")

	preds := s.predictNext("edit_file")
	for _, p := range preds {
		if p == "grep" {
			t.Fatal("grep should not be predicted after sequence reset")
		}
	}
}

func TestPredictArgs_EditFileToReadFile(t *testing.T) {
	prevArgs := json.RawMessage(`{"file_path":"/path/to/file.go","old_text":"a","new_text":"b"}`)

	result := predictArgs("read_file", "edit_file", prevArgs)
	if result == nil {
		t.Fatal("expected predicted args for read_file after edit_file")
	}

	var fields map[string]string
	if err := json.Unmarshal(result, &fields); err != nil {
		t.Fatalf("failed to unmarshal predicted args: %v", err)
	}
	if fields["path"] != "/path/to/file.go" {
		t.Errorf("expected path=/path/to/file.go, got %s", fields["path"])
	}
}

func TestPredictArgs_NoLinkForUnlinkedTools(t *testing.T) {
	prevArgs := json.RawMessage(`{"path":"/test.go"}`)

	result := predictArgs("read_file", "run_command", prevArgs)
	if result != nil {
		t.Fatal("expected nil args for non-linked tool transition")
	}
}

func TestPredictArgs_MultiEditFileToReadFile(t *testing.T) {
	prevArgs := json.RawMessage(`{"file_path":"/path/edit.go"}`)

	result := predictArgs("read_file", "multi_edit_file", prevArgs)
	if result == nil {
		t.Fatal("expected predicted args for read_file after multi_edit_file")
	}

	var fields map[string]string
	if err := json.Unmarshal(result, &fields); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if fields["path"] != "/path/edit.go" {
		t.Errorf("expected path=/path/edit.go, got %s", fields["path"])
	}
}

func TestSpeculator_SpeculateWithRealPattern(t *testing.T) {
	s := newSpeculator()
	s.ttl = 5 * time.Second

	for i := 0; i < 3; i++ {
		s.recordObservation("edit_file")
		s.recordObservation("read_file")
	}

	registry := tool.NewRegistry()
	s.speculate(context.Background(), registry, "edit_file", json.RawMessage(`{"file_path":"/test.go"}`))

	time.Sleep(100 * time.Millisecond)

	s.mu.Lock()
	got := s.speculations
	s.mu.Unlock()
	if got == 0 {
		t.Fatal("expected speculations > 0")
	}
}

func TestSpeculator_CloseClearsCache(t *testing.T) {
	s := newSpeculator()

	args := json.RawMessage(`{"path":"/test.go"}`)
	s.store("read_file", args, mockToolResult("content"), "", 0, 0)

	s.Close()

	_, hit := s.getCached("read_file", args)
	if hit {
		t.Fatal("expected cache miss after Close()")
	}
}

func TestSpeculator_CacheEvictionLRU(t *testing.T) {
	s := newSpeculator()

	for i := 0; i < specMaxCacheSize+5; i++ {
		path := "/file" + string(rune('a'+i)) + ".go"
		s.store("read_file", json.RawMessage(`{"path":"`+path+`"}`), mockToolResult(path), "", 0, 0)
	}

	s.mu.Lock()
	cacheSize := len(s.cache)
	s.mu.Unlock()
	if cacheSize > specMaxCacheSize {
		t.Fatalf("cache size %d exceeds max %d", cacheSize, specMaxCacheSize)
	}

	_, hit := s.getCached("read_file", json.RawMessage(`{"path":"/filea.go"}`))
	if hit {
		t.Fatal("expected oldest entry to be evicted")
	}

	latestPath := "/file" + string(rune('a'+specMaxCacheSize+4)) + ".go"
	_, hit = s.getCached("read_file", json.RawMessage(`{"path":"`+latestPath+`"}`))
	if !hit {
		t.Fatal("expected newest entry to be present")
	}
}

func TestSpeculator_AdaptiveThresholdLowHitRate(t *testing.T) {
	s := newSpeculator()

	args := json.RawMessage(`{"path":"/test.go"}`)
	s.store("read_file", args, mockToolResult("content"), "", 0, 0)

	for i := 0; i < specAdaptiveWindow; i++ {
		s.getCached("read_file", json.RawMessage(`{"path":"/miss`+string(rune('a'+i%26))+`.go"}`))
	}

	s.mu.Lock()
	adaptiveMin := s.adaptiveMinCount
	s.mu.Unlock()
	if adaptiveMin <= 2 {
		t.Fatalf("expected adaptive threshold to increase above 2 with low hit rate, got %d", adaptiveMin)
	}
}

func TestSpeculator_AdaptiveThresholdHighHitRate(t *testing.T) {
	s := newSpeculator()

	args := json.RawMessage(`{"path":"/test.go"}`)
	s.store("read_file", args, mockToolResult("content"), "", 0, 0)

	for i := 0; i < specAdaptiveWindow; i++ {
		s.getCached("read_file", args)
	}

	s.mu.Lock()
	adaptiveMin := s.adaptiveMinCount
	s.mu.Unlock()
	if adaptiveMin < specAdaptiveFloor {
		t.Fatalf("adaptive threshold below floor: %d", adaptiveMin)
	}

	if adaptiveMin > 1 {

		if adaptiveMin > 2 {
			t.Fatalf("adaptive threshold should not increase with high hit rate, got %d", adaptiveMin)
		}
	}
}

func TestSpeculator_AdaptiveThresholdAffectsPrediction(t *testing.T) {
	s := newSpeculator()

	s.recordObservation("edit_file")
	s.recordObservation("read_file")
	s.recordObservation("edit_file")
	s.recordObservation("read_file")

	preds := s.predictNext("edit_file")
	if len(preds) == 0 {
		t.Fatal("expected prediction with default threshold=2")
	}

	s.mu.Lock()
	s.adaptiveMinCount = 5
	s.mu.Unlock()

	preds = s.predictNext("edit_file")
	if len(preds) != 0 {
		t.Fatalf("expected no prediction with threshold=5 (only 2 observations), got %v", preds)
	}
}

func TestSpeculator_ConcurrencyLimit(t *testing.T) {
	s := newSpeculator()

	s.mu.Lock()
	s.activeSpeculations = specMaxConcurrent
	s.mu.Unlock()

	s.recordObservation("edit_file")
	s.recordObservation("read_file")
	s.recordObservation("edit_file")
	s.recordObservation("read_file")

	s.mu.Lock()
	beforeSpecs := s.speculations
	s.mu.Unlock()
	s.speculate(context.Background(), nil, "edit_file", json.RawMessage(`{"file_path":"/test.go"}`))
	s.mu.Lock()
	afterSpecs := s.speculations
	s.mu.Unlock()

	if afterSpecs != beforeSpecs {
		t.Fatalf("expected no new speculations with max concurrency reached, got delta %d",
			afterSpecs-beforeSpecs)
	}
}

func mockToolResult(content string) tool.Result {
	return tool.Result{Content: content}
}
