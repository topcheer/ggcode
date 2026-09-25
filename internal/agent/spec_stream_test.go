package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// stubTool is a minimal read-only tool for stream-speculator tests.
type stubTool struct {
	name    string
	calls   atomic.Int32
	mu      sync.Mutex
	started chan struct{} // closed on first Execute
	block   chan struct{} // Execute blocks until closed (nil = no block)
}

func (s *stubTool) Name() string        { return s.name }
func (s *stubTool) Description() string { return "stub " + s.name }
func (s *stubTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}

func (s *stubTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	s.calls.Add(1)
	if s.started != nil {
		select {
		case <-s.started:
		default:
			close(s.started)
		}
	}
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return tool.Result{}, ctx.Err()
		}
	}
	return tool.Result{Content: "stub-result-" + s.name}, nil
}

func newStreamSpecTestAgent(t *testing.T, tools ...tool.Tool) *Agent {
	t.Helper()
	reg := tool.NewRegistry()
	for _, tl := range tools {
		if err := reg.Register(tl); err != nil {
			t.Fatalf("register %s: %v", tl.Name(), err)
		}
	}
	return &Agent{
		tools:      reg,
		speculator: newSpeculator(),
	}
}

// Speculation starts on ToolCallDone and collect() returns the result keyed
// by arrival order (== index in the response toolCalls slice).
func TestStreamSpeculator_CommitsResult(t *testing.T) {
	rt := &stubTool{name: "read_file", started: make(chan struct{})}
	a := newStreamSpecTestAgent(t, rt)
	spec := newStreamSpeculator(a, context.Background())

	spec.onToolCallDone(provider.ToolCallDelta{ID: "t1", Name: "read_file", Arguments: []byte(`{"path":"/tmp/x"}`)})

	select {
	case <-rt.started:
	case <-time.After(2 * time.Second):
		t.Fatal("speculation did not start on ToolCallDone")
	}

	res := spec.collect()
	if len(res) != 1 {
		t.Fatalf("expected 1 committed result, got %d", len(res))
	}
	r, ok := res[0]
	if !ok {
		t.Fatal("expected result at index 0")
	}
	if r.result.Content != "stub-result-read_file" {
		t.Fatalf("unexpected content %q", r.result.Content)
	}
	// Idempotent collect.
	if again := spec.collect(); again != nil {
		t.Fatalf("collect not idempotent: %v", again)
	}
	if got := rt.calls.Load(); got != 1 {
		t.Fatalf("expected 1 execution, got %d", got)
	}
}

// A later file-scoped mutation in the same response invalidates a colliding
// read (the #1475-A stale-read failure mode) but keeps unrelated reads.
func TestStreamSpeculator_MutationInvalidatesCollidingRead(t *testing.T) {
	rt := &stubTool{name: "read_file", started: make(chan struct{})}
	a := newStreamSpecTestAgent(t, rt)
	spec := newStreamSpeculator(a, context.Background())

	spec.onToolCallDone(provider.ToolCallDelta{ID: "t1", Name: "read_file", Arguments: []byte(`{"path":"/w/affected.go"}`)})
	<-rt.started
	// Same file mutation → colliding read invalidated.
	spec.onToolCallDone(provider.ToolCallDelta{ID: "t2", Name: "edit_file", Arguments: []byte(`{"file_path":"/w/affected.go","old_text":"a","new_text":"b"}`)})

	if spec.covers(0) {
		t.Fatal("colliding read should have been invalidated")
	}
	if res := spec.collect(); len(res) != 0 {
		t.Fatalf("expected no committed results after invalidation, got %d", len(res))
	}
}

// A tree-wide/unknown-scope mutation (run_command) invalidates ALL pending
// speculations.
func TestStreamSpeculator_TreeWideMutationInvalidatesAll(t *testing.T) {
	rt := &stubTool{name: "git_status", started: make(chan struct{})}
	a := newStreamSpecTestAgent(t, rt)
	spec := newStreamSpeculator(a, context.Background())

	spec.onToolCallDone(provider.ToolCallDelta{ID: "t1", Name: "git_status", Arguments: []byte(`{}`)})
	<-rt.started
	spec.onToolCallDone(provider.ToolCallDelta{ID: "t2", Name: "run_command", Arguments: []byte(`{"command":"make build"}`)})

	if spec.covers(0) {
		t.Fatal("tree-wide mutation should invalidate all speculations")
	}
}

// Unrelated file-scoped mutations do NOT invalidate unrelated reads.
func TestStreamSpeculator_UnrelatedMutationKeepsRead(t *testing.T) {
	rt := &stubTool{name: "read_file", started: make(chan struct{})}
	a := newStreamSpecTestAgent(t, rt)
	spec := newStreamSpeculator(a, context.Background())

	spec.onToolCallDone(provider.ToolCallDelta{ID: "t1", Name: "read_file", Arguments: []byte(`{"path":"/w/other.go"}`)})
	<-rt.started
	spec.onToolCallDone(provider.ToolCallDelta{ID: "t2", Name: "edit_file", Arguments: []byte(`{"file_path":"/w/unrelated.go","old_text":"a","new_text":"b"}`)})

	if !spec.covers(0) {
		t.Fatal("unrelated read should survive file-scoped mutation elsewhere")
	}
	res := spec.collect()
	if len(res) != 1 {
		t.Fatalf("expected 1 committed result, got %d", len(res))
	}
}

// Non-read-only tools never speculate.
func TestStreamSpeculator_SkipsNonSafeTools(t *testing.T) {
	a := newStreamSpecTestAgent(t)
	spec := newStreamSpeculator(a, context.Background())

	spec.onToolCallDone(provider.ToolCallDelta{ID: "t1", Name: "edit_file", Arguments: []byte(`{"file_path":"/w/a.go"}`)})
	if spec.covers(0) {
		t.Fatal("mutating tool must not speculate")
	}
	// Arrival counter still advances: subsequent read is index 1 and does
	// speculate (even though the tool is unregistered - it fails fast).
	spec.onToolCallDone(provider.ToolCallDelta{ID: "t2", Name: "read_file", Arguments: []byte(`{"path":"/w/b.go"}`)})
	if !spec.covers(1) {
		t.Fatal("read tool should speculate even when unregistered")
	}
	if res := spec.collect(); len(res) != 0 {
		t.Fatalf("unregistered tool must yield no committed result, got %d", len(res))
	}
}

// collect() must not deadlock when the stream aborts mid-speculation.
func TestStreamSpeculator_AbortReleasesCollect(t *testing.T) {
	block := make(chan struct{})
	rt := &stubTool{name: "read_file", started: make(chan struct{}), block: block}
	a := newStreamSpecTestAgent(t, rt)
	spec := newStreamSpeculator(a, context.Background())

	spec.onToolCallDone(provider.ToolCallDelta{ID: "t1", Name: "read_file", Arguments: []byte(`{"path":"/tmp/y"}`)})
	<-rt.started
	spec.abort() // simulate stream error + turn retry
	close(block)

	done := make(chan map[int]preExecutedResult, 1)
	go func() { done <- spec.collect() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("collect deadlocked after abort")
	}
}

// Overlapping timing: the speculation result is available at collect time
// without a second execution even when the decode tail takes a while.
func TestStreamSpeculator_OverlapsDecodeTail(t *testing.T) {
	rt := &stubTool{name: "grep", started: make(chan struct{})}
	a := newStreamSpecTestAgent(t, rt)
	spec := newStreamSpeculator(a, context.Background())

	// ToolCallDone arrives mid-stream; "decode" continues for 100ms after.
	spec.onToolCallDone(provider.ToolCallDelta{ID: "t1", Name: "grep", Arguments: []byte(`{"pattern":"foo","path":"."}`)})
	<-rt.started
	time.Sleep(100 * time.Millisecond) // simulated decode tail

	start := time.Now()
	res := spec.collect()
	elapsed := time.Since(start)
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	if !strings.Contains(res[0].result.Content, "stub-result-grep") {
		t.Fatalf("unexpected content %q", res[0].result.Content)
	}
	if elapsed > 50*time.Millisecond {
		t.Fatalf("collect should reuse completed speculation, took %v", elapsed)
	}
	if got := rt.calls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 execution, got %d", got)
	}
}
