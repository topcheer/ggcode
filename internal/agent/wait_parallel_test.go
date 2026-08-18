package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// blockingWaitTool simulates wait_agent: blocks for the given duration, then
// returns a marker result. Records its start time so tests can prove
// concurrent execution via overlap.
type blockingWaitTool struct {
	name     string
	blockFor time.Duration
	marker   string

	mu       sync.Mutex
	started  []time.Time
	finished []time.Time
}

func (b *blockingWaitTool) Name() string { return b.name }
func (b *blockingWaitTool) Description() string {
	return "blocking wait tool for tests"
}
func (b *blockingWaitTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"marker":{"type":"string"}}}`)
}
func (b *blockingWaitTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	b.mu.Lock()
	b.started = append(b.started, time.Now())
	b.mu.Unlock()

	select {
	case <-time.After(b.blockFor):
	case <-ctx.Done():
		return tool.Result{}, ctx.Err()
	}

	b.mu.Lock()
	b.finished = append(b.finished, time.Now())
	b.mu.Unlock()
	return tool.Result{Content: b.marker}, nil
}

func newWaitTestAgent(tools ...tool.Tool) *Agent {
	reg := tool.NewRegistry()
	for _, t := range tools {
		reg.Register(t)
	}
	return &Agent{
		tools:      reg,
		speculator: newSpeculator(),
	}
}

// TestPreExecuteWaitTools_ConcurrentExecution proves multiple wait_agent
// calls in one batch execute concurrently: three 150ms waits must complete
// in well under 3*150ms (sequential would be >=450ms).
func TestPreExecuteWaitTools_ConcurrentExecution(t *testing.T) {
	w := &blockingWaitTool{name: "wait_agent", blockFor: 150 * time.Millisecond, marker: "done"}
	a := newWaitTestAgent(w)
	calls := []provider.ToolCallDelta{
		{ID: "w1", Name: "wait_agent", Arguments: []byte(`{"agent_id":"a1","description":"x"}`)},
		{ID: "w2", Name: "wait_agent", Arguments: []byte(`{"agent_id":"a2","description":"x"}`)},
		{ID: "w3", Name: "wait_agent", Arguments: []byte(`{"agent_id":"a3","description":"x"}`)},
	}

	start := time.Now()
	results := a.preExecuteWaitTools(context.Background(), calls)
	elapsed := time.Since(start)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for idx, want := range map[int]string{0: "done", 1: "done", 2: "done"} {
		if got := results[idx].result.Content; got != want {
			t.Errorf("results[%d].Content = %q, want %q", idx, got, want)
		}
	}
	// Concurrent: total elapsed must be far below the sequential 450ms.
	if elapsed >= 400*time.Millisecond {
		t.Fatalf("waits appear sequential: elapsed %v (sequential would be >=450ms)", elapsed)
	}
	// Overlap proof: all three started before the first finished.
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.started) != 3 || len(w.finished) != 3 {
		t.Fatalf("expected 3 starts/finishes, got %d/%d", len(w.started), len(w.finished))
	}
	firstFinish := w.finished[0]
	for _, s := range w.started {
		if s.After(firstFinish) {
			t.Fatalf("execution not overlapped: start %v after first finish %v", s, firstFinish)
		}
	}
}

// TestPreExecuteWaitTools_SingleWaitNotParallelized: a lone wait call must
// keep the plain sequential path (nil result map).
func TestPreExecuteWaitTools_SingleWaitNotParallelized(t *testing.T) {
	w := &blockingWaitTool{name: "wait_agent", blockFor: 10 * time.Millisecond}
	a := newWaitTestAgent(w)
	calls := []provider.ToolCallDelta{
		{ID: "1", Name: "read_file", Arguments: []byte(`{"path":"x"}`)},
		{ID: "2", Name: "wait_agent", Arguments: []byte(`{"agent_id":"a1","description":"x"}`)},
	}
	if got := a.preExecuteWaitTools(context.Background(), calls); got != nil {
		t.Fatalf("expected nil for single wait call, got %d results", len(got))
	}
}

// TestPreExecuteWaitTools_NonWaitToolsIgnored: mixed batches only pick up
// wait-family calls.
func TestPreExecuteWaitTools_NonWaitToolsIgnored(t *testing.T) {
	w := &blockingWaitTool{name: "wait_agent", blockFor: 10 * time.Millisecond}
	a := newWaitTestAgent(w)
	calls := []provider.ToolCallDelta{
		{ID: "1", Name: "edit_file", Arguments: []byte(`{}`)},
		{ID: "2", Name: "run_command", Arguments: []byte(`{}`)},
	}
	if got := a.preExecuteWaitTools(context.Background(), calls); got != nil {
		t.Fatalf("expected nil for non-wait batch, got %d results", len(got))
	}
}

// TestPreExecuteWaitTools_CancelPropagates: cancelling the context cancels
// in-flight waits (they return no results instead of hanging).
func TestPreExecuteWaitTools_CancelPropagates(t *testing.T) {
	w := &blockingWaitTool{name: "wait_agent", blockFor: 5 * time.Second}
	a := newWaitTestAgent(w)
	ctx, cancel := context.WithCancel(context.Background())
	calls := []provider.ToolCallDelta{
		{ID: "1", Name: "wait_agent", Arguments: []byte(`{"agent_id":"a1","description":"x"}`)},
		{ID: "2", Name: "wait_agent", Arguments: []byte(`{"agent_id":"a2","description":"x"}`)},
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	results := a.preExecuteWaitTools(ctx, calls)
	elapsed := time.Since(start)
	if len(results) != 0 {
		t.Fatalf("expected 0 results after cancel, got %d", len(results))
	}
	if elapsed >= time.Second {
		t.Fatalf("cancel did not propagate: elapsed %v", elapsed)
	}
}

// progressEmittingWaitTool blocks briefly while emitting progress through
// the ctx-injected callback (mirrors WaitForSnapshotWithProgress).
type progressEmittingWaitTool struct{ name string }

func (p *progressEmittingWaitTool) Name() string { return p.name }
func (p *progressEmittingWaitTool) Description() string {
	return "progress-emitting wait tool for tests"
}
func (p *progressEmittingWaitTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (p *progressEmittingWaitTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if tpf, ok := ctx.Value(tool.ToolProgressKey{}).(tool.ToolProgressFunc); ok {
		tpf("", p.name, "progress ping")
	}
	time.Sleep(30 * time.Millisecond)
	return tool.Result{Content: "ok"}, nil
}

// TestPreExecuteWaitTools_ProgressBoundPerToolID: concurrent waits must
// stream progress tagged with their own tool ID (per-slot TUI rendering).
func TestPreExecuteWaitTools_ProgressBoundPerToolID(t *testing.T) {
	w := &progressEmittingWaitTool{name: "wait_agent"}
	a := newWaitTestAgent(w)

	var mu sync.Mutex
	seen := map[string]bool{}
	a.onToolProgress = func(toolID, toolName, output string) {
		mu.Lock()
		seen[toolID] = true
		mu.Unlock()
	}

	calls := []provider.ToolCallDelta{
		{ID: "wA", Name: "wait_agent", Arguments: []byte(`{"agent_id":"a1","description":"x"}`)},
		{ID: "wB", Name: "wait_agent", Arguments: []byte(`{"agent_id":"a2","description":"x"}`)},
	}
	results := a.preExecuteWaitTools(context.Background(), calls)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	mu.Lock()
	defer mu.Unlock()
	if !seen["wA"] || !seen["wB"] {
		t.Fatalf("progress not bound per toolID: seen=%v (want both wA and wB)", seen)
	}
}
