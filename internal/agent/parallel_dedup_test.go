package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// TestBuildPreExecBatch_DeduplicatesIdenticalCalls guards the in-batch dedup:
// an identical (name, args) pair must be pre-executed once (the first
// occurrence); later duplicates are withheld so the sequential loop resolves
// them from the speculator cache. Without dedup, duplicate speculative calls
// doubled I/O for repeated reads in the same batch.
func TestBuildPreExecBatch_DeduplicatesIdenticalCalls(t *testing.T) {
	a := &Agent{
		tools:      tool.NewRegistry(),
		speculator: newSpeculator(),
	}
	sameArgs := `{"path":"/tmp/dup.go"}`
	calls := []provider.ToolCallDelta{
		{ID: "1", Name: "read_file", Arguments: []byte(sameArgs)},
		{ID: "2", Name: "read_file", Arguments: []byte(sameArgs)},
		{ID: "3", Name: "glob", Arguments: []byte(`{"pattern":"*.go"}`)},
		{ID: "4", Name: "read_file", Arguments: []byte(sameArgs)},
	}
	batch, ok := a.buildPreExecBatch(calls)
	if !ok {
		t.Fatal("expected batch to be schedulable")
	}
	if len(batch) != 2 {
		t.Fatalf("expected 2 pre-executed calls (read_file once + glob), got %d: %+v", len(batch), batch)
	}
	if batch[0].name != "read_file" || batch[0].index != 0 {
		t.Errorf("first pending should be read_file at index 0, got %s@%d", batch[0].name, batch[0].index)
	}
	if batch[1].name != "glob" || batch[1].index != 2 {
		t.Errorf("second pending should be glob at index 2, got %s@%d", batch[1].name, batch[1].index)
	}
}

// Distinct args on the same tool must all stay in the batch.
func TestBuildPreExecBatch_KeepsDistinctCalls(t *testing.T) {
	a := &Agent{
		tools:      tool.NewRegistry(),
		speculator: newSpeculator(),
	}
	calls := []provider.ToolCallDelta{
		{ID: "1", Name: "read_file", Arguments: []byte(`{"path":"/tmp/a.go"}`)},
		{ID: "2", Name: "read_file", Arguments: []byte(`{"path":"/tmp/b.go"}`)},
	}
	batch, ok := a.buildPreExecBatch(calls)
	if !ok {
		t.Fatal("expected batch to be schedulable")
	}
	if len(batch) != 2 {
		t.Fatalf("expected 2 distinct reads kept, got %d: %+v", len(batch), batch)
	}
}
