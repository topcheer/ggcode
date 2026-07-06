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

// mockToolResult creates a tool.Result for testing.
func mockToolResult(content string) tool.Result {
	return tool.Result{Content: content}
}
