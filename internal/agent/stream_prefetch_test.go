package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// slowReadTool blocks Execute until release is closed, then returns content.
type slowReadTool struct {
	name    string
	release chan struct{}
	content string
}

func (s slowReadTool) Name() string        { return s.name }
func (s slowReadTool) Description() string { return "stub" }
func (s slowReadTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}

func (s slowReadTool) Execute(_ context.Context, _ json.RawMessage) (tool.Result, error) {
	<-s.release
	return tool.Result{Content: s.content}, nil
}

func newStreamPrefetchTestAgent(release chan struct{}, content string) *Agent {
	a := &Agent{
		tools:      tool.NewRegistry(),
		speculator: newSpeculator(),
	}
	_ = a.tools.Register(slowReadTool{name: "read_file", release: release, content: content})
	a.streamPrefetch = newStreamPrefetcher()
	return a
}

// The core PASTE property: a read-only call whose JSON closes mid-stream is
// executed while generation continues; harvest blocks until it completes and
// commits its result.
func TestStreamPrefetchOverlapsGeneration(t *testing.T) {
	release := make(chan struct{})
	a := newStreamPrefetchTestAgent(release, "overlapped")
	tc := provider.ToolCallDelta{ID: "t1", Name: "read_file", Arguments: []byte(`{"path":"/x/a.go"}`)}
	if !a.streamPrefetch.mayStart(context.Background(), a, tc, 0) {
		t.Fatal("read_file should be dispatchable during streaming")
	}
	done := make(chan map[int]preExecutedResult, 1)
	go func() {
		done <- a.harvestStreamPrefetch(context.Background(), []provider.ToolCallDelta{tc})
	}()
	select {
	case res := <-done:
		t.Fatalf("harvest returned before tool completed: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	var res map[int]preExecutedResult
	select {
	case res = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("harvest did not return after tool release")
	}
	if len(res) != 1 || res[0].result.Content != "overlapped" {
		t.Fatalf("want committed result for index 0, got %+v", res)
	}
	if !a.streamPrefetch.has(0) {
		t.Fatal("has(0) must be true so batch pre-exec skips the index")
	}
}

// Commit-on-match (#1475-A race guard): a read that collides with a same-batch
// mutation - unknown while streaming - is discarded so the sequential loop
// re-executes it post-mutation with emitted-order semantics.
func TestStreamPrefetchCollisionDiscarded(t *testing.T) {
	a := newStreamPrefetchTestAgent(make(chan struct{}), "")
	read := provider.ToolCallDelta{ID: "r", Name: "read_file", Arguments: []byte(`{"path":"/x/a.go"}`)}
	edit := provider.ToolCallDelta{ID: "e", Name: "edit_file", Arguments: []byte(`{"file_path":"/x/a.go","old_text":"o","new_text":"n"}`)}
	if !a.streamPrefetch.mayStart(context.Background(), a, read, 0) {
		t.Fatal("read should dispatch during stream")
	}
	res := a.harvestStreamPrefetch(context.Background(), []provider.ToolCallDelta{read, edit})
	if len(res) != 0 {
		t.Fatalf("colliding read must be discarded for sequential re-execution, got %+v", res)
	}
}

// A mutation with an unknowable write set (shell command) makes the batch
// unschedulable; every overlapped result is discarded without blocking.
func TestStreamPrefetchUnschedulableBatchDiscard(t *testing.T) {
	release := make(chan struct{})
	a := newStreamPrefetchTestAgent(release, "")
	read := provider.ToolCallDelta{ID: "r", Name: "read_file", Arguments: []byte(`{"path":"/x/a.go"}`)}
	if !a.streamPrefetch.mayStart(context.Background(), a, read, 0) {
		t.Fatal("read should dispatch during stream")
	}
	cmd := provider.ToolCallDelta{ID: "c", Name: "run_command", Arguments: []byte(`{"command":"make all"}`)}
	done := make(chan map[int]preExecutedResult, 1)
	go func() {
		done <- a.harvestStreamPrefetch(context.Background(), []provider.ToolCallDelta{read, cmd})
	}()
	select {
	case res := <-done:
		if len(res) != 0 {
			t.Fatalf("unschedulable batch must discard all overlapped results, got %+v", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("harvest must not block on an unschedulable batch")
	}
	close(release)
}

func TestStreamPrefetchUnsafeToolAndCap(t *testing.T) {
	a := newStreamPrefetchTestAgent(make(chan struct{}), "")
	cmd := provider.ToolCallDelta{ID: "c", Name: "run_command", Arguments: []byte(`{"command":"ls"}`)}
	if a.streamPrefetch.mayStart(context.Background(), a, cmd, 0) {
		t.Fatal("run_command must never be stream-dispatched")
	}
	for i := 0; i < streamPrefetchMaxConcurrent; i++ {
		tc := provider.ToolCallDelta{ID: fmt.Sprintf("t%d", i), Name: "read_file", Arguments: []byte(`{"path":"/x/a.go"}`)}
		if !a.streamPrefetch.mayStart(context.Background(), a, tc, i) {
			t.Fatalf("dispatch %d should fit under cap", i)
		}
	}
	extra := provider.ToolCallDelta{ID: "tx", Name: "read_file", Arguments: []byte(`{"path":"/y/b.go"}`)}
	if a.streamPrefetch.mayStart(context.Background(), a, extra, streamPrefetchMaxConcurrent) {
		t.Fatal("dispatch beyond in-flight cap must be refused")
	}
}

func TestStreamPrefetchNilPrefetcherAndServerTool(t *testing.T) {
	a := &Agent{tools: tool.NewRegistry(), speculator: newSpeculator()}
	a.streamPrefetch = nil
	tc := provider.ToolCallDelta{ID: "t", Name: "read_file", Arguments: []byte(`{}`)}
	if a.streamPrefetch.mayStart(context.Background(), a, tc, 0) {
		t.Fatal("nil prefetcher must not dispatch")
	}
	if res := a.harvestStreamPrefetch(context.Background(), nil); res != nil {
		t.Fatalf("nil prefetcher harvest must be nil, got %+v", res)
	}
	a.streamPrefetch = newStreamPrefetcher()
	st := provider.ToolCallDelta{ID: "s", Name: "web_search", ServerTool: true}
	if a.streamPrefetch.mayStart(context.Background(), a, st, 0) {
		t.Fatal("server tools must not be dispatched client-side")
	}
}
