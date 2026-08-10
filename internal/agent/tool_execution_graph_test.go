package agent

import (
	"testing"
	"time"
)

func TestToolExecutionGraph(t *testing.T) {
	graph := getToolExecutionGraph()
	if graph == nil {
		t.Fatal("graph instance is nil")
	}

	// Test recording outcomes
	graph.recordToolOutcome("read_file", true, 100*time.Millisecond, "")
	graph.recordToolOutcome("edit_file", true, 200*time.Millisecond, "read_file")
	graph.recordToolOutcome("edit_file", false, 50*time.Millisecond, "read_file")
	graph.recordToolOutcome("grep", true, 150*time.Millisecond, "edit_file")

	// Test success rate
	if rate := graph.getToolSuccessRate("read_file"); rate != 1.0 {
		t.Errorf("read_file success rate = %v, want 1.0", rate)
	}
	if rate := graph.getToolSuccessRate("edit_file"); rate != 0.5 {
		t.Errorf("edit_file success rate = %v, want 0.5", rate)
	}
	if rate := graph.getToolSuccessRate("unknown"); rate != 0.5 {
		t.Errorf("unknown tool success rate = %v, want 0.5 (neutral)", rate)
	}

	// Test pattern score
	// After recording: read_file -> edit_file (2 times, 1 success, 1 failure) = 0.5
	if score := graph.getPatternScore("read_file", "edit_file"); score != 0.5 {
		t.Errorf("read_file->edit_file pattern score = %v, want 0.5", score)
	}
	// unseen pattern should be neutral
	if score := graph.getPatternScore("read_file", "grep"); score != 0.5 {
		t.Errorf("unseen pattern score = %v, want 0.5 (neutral)", score)
	}

	// Test last used
	if _, ok := graph.getLastUsed("read_file"); !ok {
		t.Error("getLastUsed should return true for known tool")
	}
	if _, ok := graph.getLastUsed("unknown"); ok {
		t.Error("getLastUsed should return false for unknown tool")
	}

	// Test recent failures
	graph.recordToolOutcome("failing_tool", false, 10*time.Millisecond, "")
	graph.recordToolOutcome("failing_tool", false, 10*time.Millisecond, "")
	if count := graph.getRecentFailures("failing_tool"); count != 2 {
		t.Errorf("recent failures = %v, want 2", count)
	}
}

func TestToolExecutionGraphConcurrency(t *testing.T) {
	graph := getToolExecutionGraph()
	done := make(chan bool)

	// Concurrent writes
	for i := 0; i < 10; i++ {
		go func() {
			graph.recordToolOutcome("concurrent_tool", true, 100*time.Millisecond, "")
			done <- true
		}()
	}

	// Concurrent reads
	for i := 0; i < 10; i++ {
		go func() {
			graph.getToolSuccessRate("concurrent_tool")
			graph.getPatternScore("prev", "concurrent_tool")
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 20; i++ {
		<-done
	}

	// Should have no race conditions
	t.Log("Concurrency test passed")
}
