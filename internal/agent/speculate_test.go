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

	// Simulate: edit_file → read_file pattern occurring 3 times.
	for i := 0; i < 3; i++ {
		s.recordObservation("edit_file")
		s.recordObservation("read_file")
	}

	// After edit_file, predictNext should return read_file.
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

	// Only 1 observation — should not predict (threshold is 2).
	s.recordObservation("edit_file")
	s.recordObservation("read_file")

	preds := s.predictNext("edit_file")
	if len(preds) != 0 {
		t.Fatalf("expected no predictions with 1 observation, got %v", preds)
	}
}

func TestSpeculator_OnlyReadOnlyPredictions(t *testing.T) {
	s := newSpeculator()

	// edit_file → write_file pattern (write_file is NOT read-only).
	for i := 0; i < 5; i++ {
		s.recordObservation("edit_file")
		s.recordObservation("write_file")
	}

	preds := s.predictNext("edit_file")
	// write_file should not be predicted (not in speculativeSafeTools).
	for _, p := range preds {
		if p == "write_file" {
			t.Fatal("write_file should not be predicted (has side effects)")
		}
	}
}

func TestSpeculator_PredictionOrdering(t *testing.T) {
	s := newSpeculator()

	// edit_file → grep (5 times) and edit_file → read_file (3 times).
	for i := 0; i < 5; i++ {
		s.recordObservation("edit_file")
		s.recordObservation("grep")
	}
	s.lastTool = "" // reset
	for i := 0; i < 3; i++ {
		s.recordObservation("edit_file")
		s.recordObservation("read_file")
	}

	preds := s.predictNext("edit_file")
	if len(preds) < 2 {
		t.Fatalf("expected at least 2 predictions, got %d", len(preds))
	}
	// grep has higher count (5 vs 3), should come first.
	if preds[0] != "grep" {
		t.Errorf("expected grep first (count=5), got %s", preds[0])
	}
}

func TestSpeculator_CacheStoreAndGet(t *testing.T) {
	s := newSpeculator()

	args := json.RawMessage(`{"path":"/test/file.go"}`)
	s.store("read_file", args, mockToolResult("file content"))

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

	s.store("read_file", json.RawMessage(`{"path":"/a.go"}`), mockToolResult("a"))

	_, hit := s.getCached("read_file", json.RawMessage(`{"path":"/b.go"}`))
	if hit {
		t.Fatal("expected cache miss with different args")
	}
}

func TestSpeculator_CacheExpiry(t *testing.T) {
	s := newSpeculator()
	s.ttl = 50 * time.Millisecond // very short TTL for testing

	args := json.RawMessage(`{"path":"/test.go"}`)
	s.store("read_file", args, mockToolResult("content"))

	// Immediately should hit.
	_, hit := s.getCached("read_file", args)
	if !hit {
		t.Fatal("expected cache hit immediately after store")
	}

	// Wait for expiry.
	time.Sleep(100 * time.Millisecond)

	_, hit = s.getCached("read_file", args)
	if hit {
		t.Fatal("expected cache miss after TTL expiry")
	}
}

func TestSpeculator_StatsTracking(t *testing.T) {
	s := newSpeculator()

	args := json.RawMessage(`{"path":"/test.go"}`)
	s.store("read_file", args, mockToolResult("content"))

	// 2 hits.
	s.getCached("read_file", args)
	s.getCached("read_file", args)

	// 1 miss (different args).
	s.getCached("read_file", json.RawMessage(`{"path":"/other.go"}`))

	stats := s.stats()
	if stats.Hits != 2 {
		t.Errorf("expected 2 hits, got %d", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("expected 1 miss, got %d", stats.Misses)
	}
}

func TestSpeculator_ResetSequence(t *testing.T) {
	s := newSpeculator()

	s.recordObservation("edit_file")
	s.recordObservation("read_file")

	s.resetSequence()

	// After reset, lastTool is cleared, so next observation doesn't create a pattern.
	s.recordObservation("grep")

	// edit_file → grep should NOT be a pattern (sequence was reset).
	// Only edit_file → read_file should be in the model.
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

	// run_command is not arg-linked with read_file.
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

	// Learn the pattern: edit_file → read_file.
	for i := 0; i < 3; i++ {
		s.recordObservation("edit_file")
		s.recordObservation("read_file")
	}

	// Now simulate: edit_file just happened, speculate on read_file.
	// Use an empty registry — the goroutine will find no tool and exit cleanly,
	// but the speculations counter should still be incremented.
	registry := tool.NewRegistry()
	s.speculate(context.Background(), registry, "edit_file", json.RawMessage(`{"file_path":"/test.go"}`))
	// Give background goroutine time to run (it will find no tool, which is fine).
	time.Sleep(100 * time.Millisecond)

	// The speculation counter should have been incremented.
	stats := s.stats()
	if stats.Speculations == 0 {
		t.Fatal("expected speculations > 0")
	}
}

func TestSpeculator_CloseClearsCache(t *testing.T) {
	s := newSpeculator()

	args := json.RawMessage(`{"path":"/test.go"}`)
	s.store("read_file", args, mockToolResult("content"))

	s.Close()

	_, hit := s.getCached("read_file", args)
	if hit {
		t.Fatal("expected cache miss after Close()")
	}
}

func TestSpeculator_CacheEvictionLRU(t *testing.T) {
	s := newSpeculator()

	// Fill cache beyond specMaxCacheSize.
	for i := 0; i < specMaxCacheSize+5; i++ {
		path := "/file" + string(rune('a'+i)) + ".go"
		s.store("read_file", json.RawMessage(`{"path":"`+path+`"}`), mockToolResult(path))
	}

	stats := s.stats()
	if stats.CacheSize > specMaxCacheSize {
		t.Fatalf("cache size %d exceeds max %d", stats.CacheSize, specMaxCacheSize)
	}

	// The earliest entries should have been evicted.
	_, hit := s.getCached("read_file", json.RawMessage(`{"path":"/filea.go"}`))
	if hit {
		t.Fatal("expected oldest entry to be evicted")
	}

	// The latest entries should still be present.
	latestPath := "/file" + string(rune('a'+specMaxCacheSize+4)) + ".go"
	_, hit = s.getCached("read_file", json.RawMessage(`{"path":"`+latestPath+`"}`))
	if !hit {
		t.Fatal("expected newest entry to be present")
	}
}

func TestSpeculator_AdaptiveThresholdLowHitRate(t *testing.T) {
	s := newSpeculator()

	// Store one result.
	args := json.RawMessage(`{"path":"/test.go"}`)
	s.store("read_file", args, mockToolResult("content"))

	// Generate many misses to drive hit rate down.
	// Default threshold is 2; with low hit rate it should increase.
	for i := 0; i < specAdaptiveWindow; i++ {
		s.getCached("read_file", json.RawMessage(`{"path":"/miss`+string(rune('a'+i%26))+`.go"}`))
	}

	stats := s.stats()
	if stats.AdaptiveMinCount <= 2 {
		t.Fatalf("expected adaptive threshold to increase above 2 with low hit rate, got %d", stats.AdaptiveMinCount)
	}
}

func TestSpeculator_AdaptiveThresholdHighHitRate(t *testing.T) {
	s := newSpeculator()

	// Generate mostly hits to drive hit rate up.
	args := json.RawMessage(`{"path":"/test.go"}`)
	s.store("read_file", args, mockToolResult("content"))

	for i := 0; i < specAdaptiveWindow; i++ {
		s.getCached("read_file", args) // always hit
	}

	stats := s.stats()
	if stats.AdaptiveMinCount < specAdaptiveFloor {
		t.Fatalf("adaptive threshold below floor: %d", stats.AdaptiveMinCount)
	}
	// With 100% hit rate, threshold should have been lowered.
	if stats.AdaptiveMinCount > 1 {
		// It may not have lowered yet if it started at 2, but let's verify
		// it didn't increase. The key assertion is it should be <= initial 2.
		if stats.AdaptiveMinCount > 2 {
			t.Fatalf("adaptive threshold should not increase with high hit rate, got %d", stats.AdaptiveMinCount)
		}
	}
}

func TestSpeculator_AdaptiveThresholdAffectsPrediction(t *testing.T) {
	s := newSpeculator()

	// Learn pattern with only 2 observations.
	s.recordObservation("edit_file")
	s.recordObservation("read_file")
	s.recordObservation("edit_file")
	s.recordObservation("read_file")

	// With default threshold=2, prediction should work.
	preds := s.predictNext("edit_file")
	if len(preds) == 0 {
		t.Fatal("expected prediction with default threshold=2")
	}

	// Force threshold up to 5.
	s.mu.Lock()
	s.adaptiveMinCount = 5
	s.mu.Unlock()

	// With threshold=5, prediction should not fire (only 2 observations).
	preds = s.predictNext("edit_file")
	if len(preds) != 0 {
		t.Fatalf("expected no prediction with threshold=5 (only 2 observations), got %v", preds)
	}
}

func TestSpeculator_ConcurrencyLimit(t *testing.T) {
	s := newSpeculator()

	// Manually set active speculations to max.
	s.mu.Lock()
	s.activeSpeculations = specMaxConcurrent
	s.mu.Unlock()

	// Speculate should skip due to concurrency limit.
	// (Even with patterns, the concurrency check runs first.)
	s.recordObservation("edit_file")
	s.recordObservation("read_file")
	s.recordObservation("edit_file")
	s.recordObservation("read_file")

	beforeStats := s.stats()
	s.speculate(context.Background(), nil, "edit_file", json.RawMessage(`{"file_path":"/test.go"}`))
	afterStats := s.stats()

	// Speculations count should not increase (skipped due to concurrency).
	if afterStats.Speculations != beforeStats.Speculations {
		t.Fatalf("expected no new speculations with max concurrency reached, got delta %d",
			afterStats.Speculations-beforeStats.Speculations)
	}
}

func TestSpeculator_StatsIncludeNewFields(t *testing.T) {
	s := newSpeculator()

	stats := s.stats()
	if stats.AdaptiveMinCount != 2 {
		t.Errorf("expected initial adaptiveMinCount=2, got %d", stats.AdaptiveMinCount)
	}
	if stats.CacheSize != 0 {
		t.Errorf("expected initial cacheSize=0, got %d", stats.CacheSize)
	}
	if stats.ActiveSpecs != 0 {
		t.Errorf("expected initial activeSpecs=0, got %d", stats.ActiveSpecs)
	}
}

// mockToolResult creates a tool.Result for testing.
func mockToolResult(content string) tool.Result {
	return tool.Result{Content: content}
}
