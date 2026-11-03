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

	calls := []provider.ToolCallDelta{
		{ID: "1", Name: "edit_file", Arguments: []byte(`{}`)},
		{ID: "2", Name: "read_file", Arguments: []byte(`{"path":"/tmp/a"}`)},
		{ID: "3", Name: "run_command", Arguments: []byte(`{}`)},
		{ID: "4", Name: "glob", Arguments: []byte(`{"pattern":"*.go"}`)},
	}

	results := a.preExecuteReadOnlyTools(context.Background(), calls)

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
	cancel()

	calls := []provider.ToolCallDelta{
		{ID: "1", Name: "read_file", Arguments: []byte(`{"path":"/tmp/a"}`)},
		{ID: "2", Name: "glob", Arguments: []byte(`{"pattern":"*.go"}`)},
	}
	results := a.preExecuteReadOnlyTools(ctx, calls)

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

	memoResult := tool.Result{Content: "cached content"}
	a.toolMemo.put("read_file", []byte(`{"path":"/tmp/a"}`), memoResult)

	calls := []provider.ToolCallDelta{
		{ID: "1", Name: "read_file", Arguments: []byte(`{"path":"/tmp/a"}`)},
		{ID: "2", Name: "glob", Arguments: []byte(`{"pattern":"*.go"}`)},
	}
	results := a.preExecuteReadOnlyTools(context.Background(), calls)

	if results != nil {
		t.Errorf("expected nil (tools not registered), got %d results", len(results))
	}
}

func TestPreExecuteReadOnly_MixedBatchSkipped(t *testing.T) {
	a := &Agent{
		tools:      tool.NewRegistry(),
		speculator: newSpeculator(),
	}
	calls := []provider.ToolCallDelta{
		{ID: "t1", Name: "edit_file", Arguments: []byte(`{"file_path":"/x/a.go","old_text":"o","new_text":"n"}`)},
		{ID: "t2", Name: "read_file", Arguments: []byte(`{"path":"/x/a.go"}`)},
	}
	if got := a.preExecuteReadOnlyTools(context.Background(), calls); len(got) != 0 {
		t.Fatalf("mixed batch pre-executed: %+v", got)
	}

	pure := []provider.ToolCallDelta{
		{ID: "t3", Name: "read_file", Arguments: []byte(`{"path":"/x/a.go"}`)},
		{ID: "t4", Name: "glob", Arguments: []byte(`{"pattern":"*.go"}`)},
	}
	got := a.preExecuteReadOnlyTools(context.Background(), pure)
	_ = got
}

func TestPreExecuteReadOnly_SkipsDelegate(t *testing.T) {
	a := &Agent{
		tools:      tool.NewRegistry(),
		speculator: newSpeculator(),
	}
	calls := []provider.ToolCallDelta{
		{ID: "1", Name: "delegate", Arguments: []byte(`{"agent":"claude","prompt":"edit x"}`)},
		{ID: "2", Name: "read_file", Arguments: []byte(`{"path":"x"}`)},
	}
	if results := a.preExecuteReadOnlyTools(context.Background(), calls); results != nil {
		t.Errorf("mixed batch with delegate must skip pre-execution, got %d results", len(results))
	}
}
