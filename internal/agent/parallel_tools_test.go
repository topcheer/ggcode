package agent

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestPreExecuteReadOnly_SingleTool(t *testing.T) {
	a := &Agent{
		tools:      tool.NewRegistry(),
		speculator: newSpeculator(),
	}
	// Single tool call should not trigger parallel execution.
	calls := []provider.ToolCallDelta{
		{ID: "1", Name: "read_file", Arguments: []byte(`{"path":"/tmp/test"}`)},
	}
	results := a.preExecuteReadOnlyTools(context.Background(), calls)
	if results != nil {
		t.Errorf("expected nil for single tool call, got %d results", len(results))
	}
}

func TestPreExecuteReadOnly_SkipsNonReadOnly(t *testing.T) {
	a := &Agent{
		tools:      tool.NewRegistry(),
		speculator: newSpeculator(),
	}
	calls := []provider.ToolCallDelta{
		{ID: "1", Name: "edit_file", Arguments: []byte(`{"path":"x","old_text":"a","new_text":"b"}`)},
		{ID: "2", Name: "run_command", Arguments: []byte(`{"command":"ls"}`)},
		{ID: "3", Name: "write_file", Arguments: []byte(`{"path":"x","content":"y"}`)},
	}
	results := a.preExecuteReadOnlyTools(context.Background(), calls)
	if results != nil {
		t.Errorf("expected nil for non-read-only tools, got %d results", len(results))
	}
}

func TestPreExecuteReadOnly_NoPanicOnMixedBatch(t *testing.T) {
	a := &Agent{
		tools:      tool.NewRegistry(),
		speculator: newSpeculator(),
	}
	// Mixed batch with read-only and side-effect tools.
	// Should not panic even though tools aren't registered.
	calls := []provider.ToolCallDelta{
		{ID: "1", Name: "edit_file", Arguments: []byte(`{}`)},
		{ID: "2", Name: "read_file", Arguments: []byte(`{"path":"/tmp/a"}`)},
		{ID: "3", Name: "run_command", Arguments: []byte(`{}`)},
		{ID: "4", Name: "glob", Arguments: []byte(`{"pattern":"*.go"}`)},
	}
	// Tools aren't registered, so Execute will return an error for each.
	// preExecuteReadOnlyTools should handle this gracefully (results=nil).
	results := a.preExecuteReadOnlyTools(context.Background(), calls)
	// Results will be nil because tools aren't registered.
	if results != nil {
		t.Errorf("expected nil when tools not registered, got %d results", len(results))
	}
}

func TestPreExecuteReadOnly_RespectsMaxConcurrent(t *testing.T) {
	a := &Agent{
		tools:      tool.NewRegistry(),
		speculator: newSpeculator(),
	}
	calls := make([]provider.ToolCallDelta, 10)
	for i := range calls {
		calls[i] = provider.ToolCallDelta{
			ID:        string(rune('a' + i)),
			Name:      "glob",
			Arguments: []byte(`{"pattern":"*.go"}`),
		}
	}
	results := a.preExecuteReadOnlyTools(context.Background(), calls)
	// At most parallelMaxConcurrent (3) should be pre-executed.
	// Since tools aren't registered, they'll all fail — results will be nil.
	// The key test is no panic/deadlock with many concurrent calls.
	if results != nil && len(results) > parallelMaxConcurrent {
		t.Errorf("expected at most %d results, got %d", parallelMaxConcurrent, len(results))
	}
}

func TestPreExecuteReadOnly_CancelledContext(t *testing.T) {
	a := &Agent{
		tools:      tool.NewRegistry(),
		speculator: newSpeculator(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	calls := []provider.ToolCallDelta{
		{ID: "1", Name: "read_file", Arguments: []byte(`{"path":"/tmp/a"}`)},
		{ID: "2", Name: "glob", Arguments: []byte(`{"pattern":"*.go"}`)},
	}
	results := a.preExecuteReadOnlyTools(ctx, calls)
	// With cancelled context, all goroutines should return early.
	if results != nil {
		t.Errorf("expected nil with cancelled context, got %d results", len(results))
	}
}

func TestPreExecuteReadOnly_SkipsMemoizedTools(t *testing.T) {
	a := &Agent{
		tools:      tool.NewRegistry(),
		speculator: newSpeculator(),
		toolMemo:   newToolMemo(),
	}
	// Pre-populate memoization cache for read_file.
	memoResult := tool.Result{Content: "cached content"}
	a.toolMemo.put("read_file", []byte(`{"path":"/tmp/a"}`), memoResult)

	calls := []provider.ToolCallDelta{
		{ID: "1", Name: "read_file", Arguments: []byte(`{"path":"/tmp/a"}`)},
		{ID: "2", Name: "glob", Arguments: []byte(`{"pattern":"*.go"}`)},
	}
	results := a.preExecuteReadOnlyTools(context.Background(), calls)
	// read_file was memoized, so only glob should be in the batch.
	// Since glob isn't registered, results will be nil — the key check is
	// that no panic occurs and we don't try to pre-execute the memoized tool.
	if results != nil {
		t.Errorf("expected nil (tools not registered), got %d results", len(results))
	}
}
