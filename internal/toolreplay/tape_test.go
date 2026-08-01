package toolreplay

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

// fakeTool is a minimal tool.Tool implementation for testing.
type fakeTool struct {
	name   string
	calls  int
	result string
}

func (f *fakeTool) Name() string        { return f.name }
func (f *fakeTool) Description() string { return "fake tool" }
func (f *fakeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (f *fakeTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	f.calls++
	return tool.Result{Content: f.result, IsError: false}, nil
}

func TestHashInput_DifferentKeys(t *testing.T) {
	h1 := HashInput(json.RawMessage(`{"a":1,"b":2}`))
	h2 := HashInput(json.RawMessage(`{"b":2,"a":1}`))
	if h1 != h2 {
		t.Errorf("expected same hash for reordered keys, got %q != %q", h1, h2)
	}
}

func TestHashInput_DifferentValues(t *testing.T) {
	h1 := HashInput(json.RawMessage(`{"a":1}`))
	h2 := HashInput(json.RawMessage(`{"a":2}`))
	if h1 == h2 {
		t.Error("expected different hashes for different values")
	}
}

func TestHashInput_Empty(t *testing.T) {
	h := HashInput(nil)
	if h == "" {
		t.Error("expected non-empty hash for nil input")
	}
}

func TestRecordAndReplay(t *testing.T) {
	tape := NewTape()
	inner := &fakeTool{name: "read_file", result: "hello world"}

	recorder := NewRecorder(inner, tape)
	ctx := context.Background()
	input := json.RawMessage(`{"path":"/foo/bar.go"}`)

	origResult, err := recorder.Execute(ctx, input)
	if err != nil {
		t.Fatalf("record Execute failed: %v", err)
	}
	if origResult.Content != "hello world" {
		t.Fatalf("expected 'hello world', got %q", origResult.Content)
	}
	if inner.calls != 1 {
		t.Errorf("expected 1 real execution, got %d", inner.calls)
	}

	if tape.Len() != 1 {
		t.Fatalf("expected tape length 1, got %d", tape.Len())
	}

	// Now replay — real tool should NOT be called.
	inner2 := &fakeTool{name: "read_file", result: "WRONG - should not be used"}
	replayer := NewReplayer(inner2, tape, false)

	replayedResult, err := replayer.Execute(ctx, input)
	if err != nil {
		t.Fatalf("replay Execute failed: %v", err)
	}
	if replayedResult.Content != "hello world" {
		t.Fatalf("expected replayed 'hello world', got %q", replayedResult.Content)
	}
	if inner2.calls != 0 {
		t.Errorf("expected 0 real executions during replay, got %d", inner2.calls)
	}
}

func TestReplay_SameInputReorderedKeys(t *testing.T) {
	tape := NewTape()
	inner := &fakeTool{name: "grep", result: "found it"}

	recorder := NewRecorder(inner, tape)
	ctx := context.Background()

	_, _ = recorder.Execute(ctx, json.RawMessage(`{"pattern":"foo","path":"/bar"}`))

	// Replay with reordered keys — should still match.
	inner2 := &fakeTool{name: "grep", result: "WRONG"}
	replayer := NewReplayer(inner2, tape, false)
	result, err := replayer.Execute(ctx, json.RawMessage(`{"path":"/bar","pattern":"foo"}`))
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if result.Content != "found it" {
		t.Fatalf("expected 'found it', got %q", result.Content)
	}
}

func TestReplay_StrictFIFO(t *testing.T) {
	tape := NewTape()

	// Record two calls with same input but different results.
	e1 := Entry{ToolName: "cmd", Input: json.RawMessage(`{"x":1}`), Result: Result{Content: "first"}}
	e2 := Entry{ToolName: "cmd", Input: json.RawMessage(`{"x":1}`), Result: Result{Content: "second"}}
	tape.Record(e1)
	tape.Record(e2)

	// Strict FIFO: first call gets "first", second gets "second".
	r1, ok := tape.Lookup("cmd", json.RawMessage(`{"x":1}`), true)
	if !ok || r1.Result.Content != "first" {
		t.Fatalf("expected 'first', got %q (ok=%v)", r1.Result.Content, ok)
	}
	r2, ok := tape.Lookup("cmd", json.RawMessage(`{"x":1}`), true)
	if !ok || r2.Result.Content != "second" {
		t.Fatalf("expected 'second', got %q (ok=%v)", r2.Result.Content, ok)
	}

	// Third call — all consumed, should return false.
	_, ok = tape.Lookup("cmd", json.RawMessage(`{"x":1}`), true)
	if ok {
		t.Error("expected no match on third strict FIFO call")
	}
}

func TestReplay_NonStrictFIFO(t *testing.T) {
	tape := NewTape()
	tape.Record(Entry{ToolName: "t", Input: json.RawMessage(`{}`), Result: Result{Content: "only"}})

	// Non-strict: can look up the same entry multiple times.
	for i := 0; i < 5; i++ {
		e, ok := tape.Lookup("t", json.RawMessage(`{}`), false)
		if !ok || e.Result.Content != "only" {
			t.Fatalf("call %d: expected 'only', got %q (ok=%v)", i, e.Result.Content, ok)
		}
	}
}

func TestReplay_Miss(t *testing.T) {
	tape := NewTape()
	replayer := NewReplayer(&fakeTool{name: "t"}, tape, false)
	result, err := replayer.Execute(context.Background(), json.RawMessage(`{"unmatched":true}`))
	if err != ErrNoReplayEntry {
		t.Fatalf("expected ErrNoReplayEntry, got %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true on replay miss")
	}
}

func TestReplay_Fallback(t *testing.T) {
	tape := NewTape()
	inner := &fakeTool{name: "t", result: "fallback result"}
	replayer := NewReplayerWithFallback(inner, tape, false)

	// No tape entry → fallback to real tool.
	result, err := replayer.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("fallback failed: %v", err)
	}
	if result.Content != "fallback result" {
		t.Fatalf("expected 'fallback result', got %q", result.Content)
	}
	if inner.calls != 1 {
		t.Errorf("expected 1 fallback call, got %d", inner.calls)
	}
}

func TestReplay_WithErrorResult(t *testing.T) {
	tape := NewTape()
	tape.Record(Entry{
		ToolName: "read_file",
		Input:    json.RawMessage(`{"path":"/missing"}`),
		Result:   Result{Content: "file not found", IsError: true},
		Err:      "open /missing: no such file",
	})

	replayer := NewReplayer(&fakeTool{name: "read_file"}, tape, false)
	result, err := replayer.Execute(context.Background(), json.RawMessage(`{"path":"/missing"}`))

	if err == nil || err.Error() != "open /missing: no such file" {
		t.Fatalf("expected error 'open /missing: no such file', got %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true")
	}
	if result.Content != "file not found" {
		t.Fatalf("expected 'file not found', got %q", result.Content)
	}
}

func TestTapeSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.tape.json")

	tape := NewTape()
	tape.Record(Entry{
		ToolName: "grep",
		Input:    json.RawMessage(`{"pattern":"TODO"}`),
		Result:   Result{Content: "3 matches", IsError: false},
	})
	tape.Record(Entry{
		ToolName: "read_file",
		Input:    json.RawMessage(`{"path":"main.go"}`),
		Result:   Result{Content: "package main", IsError: false},
	})

	if err := tape.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("tape file not created: %v", err)
	}

	loaded, err := LoadTape(path)
	if err != nil {
		t.Fatalf("LoadTape failed: %v", err)
	}
	if loaded.Len() != 2 {
		t.Fatalf("expected 2 entries, got %d", loaded.Len())
	}

	// Verify the grep entry can be looked up.
	e, ok := loaded.Lookup("grep", json.RawMessage(`{"pattern":"TODO"}`), true)
	if !ok {
		t.Fatal("grep entry not found after load")
	}
	if e.Result.Content != "3 matches" {
		t.Fatalf("expected '3 matches', got %q", e.Result.Content)
	}
}

func TestWrapRegistry_Record(t *testing.T) {
	reg := tool.NewRegistry()
	inner := &fakeTool{name: "grep", result: "found"}
	reg.Register(inner)

	tape := NewTape()
	wrapped := WrapRegistry(reg, tape, "record", false)

	w, ok := wrapped.Get("grep")
	if !ok {
		t.Fatal("tool not found in wrapped registry")
	}

	result, err := w.Execute(context.Background(), json.RawMessage(`{"pattern":"x"}`))
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if result.Content != "found" {
		t.Fatalf("expected 'found', got %q", result.Content)
	}
	if tape.Len() != 1 {
		t.Fatalf("expected tape length 1, got %d", tape.Len())
	}
	if inner.calls != 1 {
		t.Errorf("expected 1 real call, got %d", inner.calls)
	}
}

func TestWrapRegistry_Replay(t *testing.T) {
	reg := tool.NewRegistry()
	inner := &fakeTool{name: "grep", result: "REAL - should not run"}
	reg.Register(inner)

	tape := NewTape()
	tape.Record(Entry{
		ToolName: "grep",
		Input:    json.RawMessage(`{"pattern":"x"}`),
		Result:   Result{Content: "replayed result"},
	})

	wrapped := WrapRegistry(reg, tape, "replay", false)
	w, _ := wrapped.Get("grep")

	result, err := w.Execute(context.Background(), json.RawMessage(`{"pattern":"x"}`))
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if result.Content != "replayed result" {
		t.Fatalf("expected 'replayed result', got %q", result.Content)
	}
	if inner.calls != 0 {
		t.Errorf("expected 0 real calls, got %d", inner.calls)
	}
}

func TestWrapRegistry_PassThrough(t *testing.T) {
	reg := tool.NewRegistry()
	inner := &fakeTool{name: "grep", result: "passthrough"}
	reg.Register(inner)

	tape := NewTape()
	wrapped := WrapRegistry(reg, tape, "", false) // unknown mode = passthrough

	w, _ := wrapped.Get("grep")
	result, _ := w.Execute(context.Background(), json.RawMessage(`{}`))
	if result.Content != "passthrough" {
		t.Fatalf("expected passthrough, got %q", result.Content)
	}
	if tape.Len() != 0 {
		t.Errorf("expected empty tape in passthrough, got %d", tape.Len())
	}
}

func TestReplay_Images(t *testing.T) {
	tape := NewTape()
	tape.Record(Entry{
		ToolName: "screenshot",
		Input:    json.RawMessage(`{}`),
		Result: Result{
			Content: "captured",
			Images: []ResultImage{
				{MIME: "image/png", Base64: "iVBORw...", Width: 100, Height: 200},
			},
		},
	})

	replayer := NewReplayer(&fakeTool{name: "screenshot"}, tape, false)
	result, err := replayer.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if len(result.Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(result.Images))
	}
	if result.Images[0].MIME != "image/png" {
		t.Errorf("expected image/png, got %q", result.Images[0].MIME)
	}
	if result.Images[0].Width != 100 {
		t.Errorf("expected width 100, got %d", result.Images[0].Width)
	}
}
