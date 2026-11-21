package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// #5842 mid-tool interruption checkpoints. injectPendingInterruptions used
// to run only at LLM iteration boundaries, so an interjection typed while a
// single long tool executed sat in the channel buffer until the batch
// finished - tens of minutes for long run_command/generate chains. These
// batteries pin the three observable guarantees of the progress-side
// checkpoint: the handler is drained while the tool still runs, a
// retraction-stop cancels the running tool and aborts the remaining batch,
// and the interjection is acknowledged on the run stream immediately.

type issue5842State struct {
	mu             sync.Mutex
	available      bool
	blocks         []provider.ContentBlock
	handlerCalls   int
	handlerMidTool int
	streamRunning  bool
}

func (s *issue5842State) setAvailable(blocks []provider.ContentBlock) {
	s.mu.Lock()
	s.available = true
	s.blocks = blocks
	s.mu.Unlock()
}

func (s *issue5842State) setStreamRunning(v bool) {
	s.mu.Lock()
	s.streamRunning = v
	s.mu.Unlock()
}

func (s *issue5842State) handler() []provider.ContentBlock {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlerCalls++
	if s.streamRunning {
		s.handlerMidTool++
	}
	if !s.available || len(s.blocks) == 0 {
		return nil
	}
	b := s.blocks
	s.blocks = nil
	return b
}

func (s *issue5842State) midToolDrains() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handlerMidTool
}

type issue5842StreamTool struct {
	name    string
	state   *issue5842State
	selfEnd time.Duration

	mu           sync.Mutex
	execCount    int
	ctxCancelled bool
}

func (t *issue5842StreamTool) Name() string        { return t.name }
func (t *issue5842StreamTool) Description() string { return "streams progress while running" }
func (t *issue5842StreamTool) Parameters() json.RawMessage {
	return json.RawMessage(`{}`)
}

func (t *issue5842StreamTool) Execute(ctx context.Context, _ json.RawMessage) (tool.Result, error) {
	t.mu.Lock()
	t.execCount++
	t.mu.Unlock()
	if t.state != nil {
		t.state.setStreamRunning(true)
		defer t.state.setStreamRunning(false)
	}
	progress, _ := ctx.Value(tool.ToolProgressKey{}).(tool.ToolProgressFunc)
	deadline := time.After(t.selfEnd)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			t.mu.Lock()
			t.ctxCancelled = true
			t.mu.Unlock()
			return tool.Result{Content: "cancelled mid-stream", IsError: true}, nil
		case <-deadline:
			return tool.Result{Content: "stream finished"}, nil
		case <-tick.C:
			if progress != nil {
				progress("t5842", t.name, "chunk")
			}
		}
	}
}

func (t *issue5842StreamTool) wasCtxCancelled() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ctxCancelled
}

type issue5842MarkerTool struct {
	mu       sync.Mutex
	executed int
}

func (t *issue5842MarkerTool) Name() string        { return "midtool_marker" }
func (t *issue5842MarkerTool) Description() string { return "records execution" }
func (t *issue5842MarkerTool) Parameters() json.RawMessage {
	return json.RawMessage(`{}`)
}

func (t *issue5842MarkerTool) Execute(context.Context, json.RawMessage) (tool.Result, error) {
	t.mu.Lock()
	t.executed++
	t.mu.Unlock()
	return tool.Result{Content: "marker ran"}, nil
}

func (t *issue5842MarkerTool) executions() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.executed
}

func issue5842RunDone() chan error {
	return make(chan error, 1)
}

// TestIssue5842MidToolDrain pins the core perception fix: the interruption
// handler is drained WHILE the streaming tool still executes, so the
// interjection leaves the channel-side buffer immediately instead of
// waiting for the iteration boundary after the tool finishes.
func TestIssue5842MidToolDrain(t *testing.T) {
	state := &issue5842State{}
	stream := &issue5842StreamTool{name: "midtool_stream", state: state, selfEnd: 600 * time.Millisecond}

	p := &mockProvider{chatResponses: []*provider.ChatResponse{
		{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{
			provider.ToolUseBlock("t5842-a", "midtool_stream", json.RawMessage("{}")),
		}}},
		{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{
			provider.TextBlock("done"),
		}}},
	}}
	registry := tool.NewRegistry()
	if err := registry.Register(stream); err != nil {
		t.Fatalf("register stream tool: %v", err)
	}
	a := NewAgent(p, registry, "", 6)
	a.SetInterruptionHandler(state.handler)
	a.SetToolProgressCallback(func(string, string, string) {})

	done := issue5842RunDone()
	go func() {
		done <- a.RunStream(context.Background(), "run the stream tool", func(provider.StreamEvent) {})
	}()

	time.Sleep(150 * time.Millisecond)
	state.setAvailable([]provider.ContentBlock{provider.TextBlock("switch to approach B while you work")})

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunStream failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunStream did not finish")
	}

	if state.midToolDrains() == 0 {
		t.Fatal("interruption handler was never drained while the streaming tool was still running - interjections sit in the channel buffer for the whole tool duration (#5842)")
	}

	p.mu.Lock()
	captured := p.capturedMsgs
	p.mu.Unlock()
	if len(captured) < 2 {
		t.Fatalf("expected at least 2 provider calls, got %d", len(captured))
	}
	var sawInterjection, sawPreamble bool
	for _, m := range captured[1] {
		for _, b := range m.Content {
			if b.Type == "text" && strings.Contains(b.Text, "switch to approach B while you work") {
				sawInterjection = true
			}
			if b.Type == "text" && strings.Contains(b.Text, "New user guidance arrived") {
				sawPreamble = true
			}
		}
	}
	if !sawInterjection || !sawPreamble {
		t.Fatalf("second provider call missing interjection/preamble: interjection=%v preamble=%v", sawInterjection, sawPreamble)
	}
}

// TestIssue5842RetractStopCancelsLongTool pins the retraction path: a
// mid-tool "stop" must cancel the executing tool at the next progress
// event and must not execute the remaining tools of the batch.
func TestIssue5842RetractStopCancelsLongTool(t *testing.T) {
	state := &issue5842State{}
	blocker := &issue5842StreamTool{name: "midtool_blocker", state: state, selfEnd: 2 * time.Second}
	marker := &issue5842MarkerTool{}

	p := &mockProvider{chatResponses: []*provider.ChatResponse{
		{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{
			provider.ToolUseBlock("t5842-b1", "midtool_blocker", json.RawMessage("{}")),
			provider.ToolUseBlock("t5842-b2", "midtool_marker", json.RawMessage("{}")),
		}}},
		{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{
			provider.TextBlock("done"),
		}}},
	}}
	registry := tool.NewRegistry()
	if err := registry.Register(blocker); err != nil {
		t.Fatalf("register blocker: %v", err)
	}
	if err := registry.Register(marker); err != nil {
		t.Fatalf("register marker: %v", err)
	}
	a := NewAgent(p, registry, "", 6)
	a.SetInterruptionHandler(state.handler)
	a.SetToolProgressCallback(func(string, string, string) {})

	done := issue5842RunDone()
	go func() {
		done <- a.RunStream(context.Background(), "run the blocker then the marker", func(provider.StreamEvent) {})
	}()

	time.Sleep(150 * time.Millisecond)
	state.setAvailable([]provider.ContentBlock{provider.TextBlock("stop")})

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("RunStream did not finish")
	}

	if !blocker.wasCtxCancelled() {
		t.Fatal("mid-tool retraction-stop did not cancel the executing tool - a 30-minute command would keep running to completion (#5842)")
	}
	if n := marker.executions(); n != 0 {
		t.Fatalf("remaining batch tool executed after mid-tool retraction-stop (%d executions)", n)
	}
}

// TestIssue5842MidToolAckEvent pins the perception feedback: a system
// stream event acknowledges the interjection while the tool still runs.
func TestIssue5842MidToolAckEvent(t *testing.T) {
	state := &issue5842State{}
	stream := &issue5842StreamTool{name: "midtool_stream", state: state, selfEnd: 600 * time.Millisecond}

	p := &mockProvider{chatResponses: []*provider.ChatResponse{
		{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{
			provider.ToolUseBlock("t5842-c", "midtool_stream", json.RawMessage("{}")),
		}}},
		{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{
			provider.TextBlock("done"),
		}}},
	}}
	registry := tool.NewRegistry()
	if err := registry.Register(stream); err != nil {
		t.Fatalf("register stream tool: %v", err)
	}
	a := NewAgent(p, registry, "", 6)
	a.SetInterruptionHandler(state.handler)
	a.SetToolProgressCallback(func(string, string, string) {})

	var evMu sync.Mutex
	var events []provider.StreamEvent
	done := issue5842RunDone()
	go func() {
		done <- a.RunStream(context.Background(), "run the stream tool", func(ev provider.StreamEvent) {
			evMu.Lock()
			events = append(events, ev)
			evMu.Unlock()
		})
	}()

	time.Sleep(150 * time.Millisecond)
	state.setAvailable([]provider.ContentBlock{provider.TextBlock("note this while you work")})

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("RunStream did not finish")
	}

	evMu.Lock()
	defer evMu.Unlock()
	for _, ev := range events {
		if ev.Type == provider.StreamEventSystem && strings.Contains(ev.Text, "mid-run") {
			return
		}
	}
	t.Fatal("no mid-run acknowledgment system event was emitted while the long tool executed - the user gets zero feedback that the interjection was perceived (#5842)")
}

// TestIssue5842InterjectionLandsAfterToolResult pins the protocol-order
// invariant that makes mid-tool staging safe on every provider: the staged
// interjection message lands AFTER the tool_result message, never between
// tool_use and tool_result.
func TestIssue5842InterjectionLandsAfterToolResult(t *testing.T) {
	state := &issue5842State{}
	stream := &issue5842StreamTool{name: "midtool_stream", state: state, selfEnd: 600 * time.Millisecond}

	p := &mockProvider{chatResponses: []*provider.ChatResponse{
		{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{
			provider.ToolUseBlock("t5842-d", "midtool_stream", json.RawMessage("{}")),
		}}},
		{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{
			provider.TextBlock("done"),
		}}},
	}}
	registry := tool.NewRegistry()
	if err := registry.Register(stream); err != nil {
		t.Fatalf("register stream tool: %v", err)
	}
	a := NewAgent(p, registry, "", 6)
	a.SetInterruptionHandler(state.handler)
	a.SetToolProgressCallback(func(string, string, string) {})

	done := issue5842RunDone()
	go func() {
		done <- a.RunStream(context.Background(), "run the stream tool", func(provider.StreamEvent) {})
	}()

	time.Sleep(150 * time.Millisecond)
	state.setAvailable([]provider.ContentBlock{provider.TextBlock("land after the result please")})

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunStream failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunStream did not finish")
	}

	p.mu.Lock()
	captured := p.capturedMsgs
	p.mu.Unlock()
	if len(captured) < 2 {
		t.Fatalf("expected at least 2 provider calls, got %d", len(captured))
	}
	toolResultIdx, interjectionIdx := -1, -1
	for i, m := range captured[1] {
		for _, b := range m.Content {
			if b.Type == "tool_result" && b.ToolID == "t5842-d" && toolResultIdx < 0 {
				toolResultIdx = i
			}
			if b.Type == "text" && strings.Contains(b.Text, "land after the result please") && interjectionIdx < 0 {
				interjectionIdx = i
			}
		}
	}
	if toolResultIdx < 0 || interjectionIdx < 0 {
		t.Fatalf("tool_result idx=%d interjection idx=%d (message missing)", toolResultIdx, interjectionIdx)
	}
	if interjectionIdx < toolResultIdx {
		t.Fatalf("interjection (msg %d) landed BEFORE the tool_result message (msg %d) - violates the tool_use/tool_result adjacency contract on strict providers", interjectionIdx, toolResultIdx)
	}
}
