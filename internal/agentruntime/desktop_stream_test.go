package agentruntime

import (
	"errors"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

type testDesktopMirror struct {
	texts       []string
	reasoning   []string
	toolCalls   []DesktopToolCallEvent
	toolResults []DesktopToolResultEvent
	flushes     []bool
	errors      []string
}

func (m *testDesktopMirror) PushText(text string) { m.texts = append(m.texts, text) }
func (m *testDesktopMirror) PushReasoning(chunk string) {
	m.reasoning = append(m.reasoning, chunk)
}
func (m *testDesktopMirror) PushToolCall(toolID, toolName, displayName, rawArgs, detail string) {
	m.toolCalls = append(m.toolCalls, DesktopToolCallEvent{
		ID:          toolID,
		Name:        toolName,
		DisplayName: displayName,
		RawArgs:     rawArgs,
		Detail:      detail,
	})
}
func (m *testDesktopMirror) PushToolResult(toolID, toolName, result string, isError bool) {
	m.toolResults = append(m.toolResults, DesktopToolResultEvent{
		ID:      toolID,
		Name:    toolName,
		Content: result,
		IsError: isError,
	})
}
func (m *testDesktopMirror) Flush(rotate bool) { m.flushes = append(m.flushes, rotate) }
func (m *testDesktopMirror) PushError(message string) {
	m.errors = append(m.errors, message)
}

type testDesktopEmitter struct {
	triggered      int
	toolResults    []DesktopToolResultEvent
	roundSummaries []struct {
		text          string
		toolCalls     int
		toolSuccesses int
		toolFailures  int
	}
}

func (e *testDesktopEmitter) TriggerTyping() { e.triggered++ }
func (e *testDesktopEmitter) EmitToolResult(toolName, rawArgs, result string, isError bool) {
	e.toolResults = append(e.toolResults, DesktopToolResultEvent{
		Name:    toolName,
		RawArgs: rawArgs,
		Content: result,
		IsError: isError,
	})
}
func (e *testDesktopEmitter) EmitRoundSummary(text string, toolCalls, toolSuccesses, toolFailures int) {
	e.roundSummaries = append(e.roundSummaries, struct {
		text          string
		toolCalls     int
		toolSuccesses int
		toolFailures  int
	}{text: text, toolCalls: toolCalls, toolSuccesses: toolSuccesses, toolFailures: toolFailures})
}

func TestHandleDesktopStreamEventToolResultTruncatesForMirrorAndEmitter(t *testing.T) {
	var round IMRoundState
	mirror := &testDesktopMirror{}
	emitter := &testDesktopEmitter{}
	long := strings.Repeat("x", 2105)

	semantic, ok := HandleDesktopStreamEvent(provider.StreamEvent{
		Type:    provider.StreamEventToolResult,
		Tool:    provider.ToolCallDelta{ID: "tool-1", Name: "read_file", Arguments: []byte(`{"path":"/tmp/test.txt"}`)},
		Result:  long,
		IsError: false,
	}, &round, emitter, mirror)
	if !ok {
		t.Fatal("expected tool result event to be handled")
	}
	if semantic.ToolResult == nil {
		t.Fatal("expected tool result payload")
	}
	if !strings.HasSuffix(semantic.ToolResult.Content, "\n...(truncated)") {
		t.Fatalf("expected truncated content, got %q", semantic.ToolResult.Content[len(semantic.ToolResult.Content)-20:])
	}
	if len(mirror.toolResults) != 1 || mirror.toolResults[0].Content != semantic.ToolResult.Content {
		t.Fatalf("expected mirrored truncated result, got %+v", mirror.toolResults)
	}
	if len(emitter.toolResults) != 1 || emitter.toolResults[0].Content != semantic.ToolResult.Content {
		t.Fatalf("expected emitted truncated result, got %+v", emitter.toolResults)
	}
	if round.ToolSuccesses != 1 || round.ToolFailures != 0 {
		t.Fatalf("unexpected round counters: %+v", round)
	}
}

func TestHandleDesktopStreamEventReasoningNormalizesChunk(t *testing.T) {
	var round IMRoundState
	mirror := &testDesktopMirror{}

	semantic, ok := HandleDesktopStreamEvent(provider.StreamEvent{
		Type: provider.StreamEventReasoning,
		Text: "__redacted_thinking__",
	}, &round, nil, mirror)
	if !ok {
		t.Fatal("expected redacted reasoning chunk to be preserved as placeholder")
	}
	if semantic.Text != "Reasoning hidden by model." {
		t.Fatalf("unexpected reasoning text: %q", semantic.Text)
	}
	if len(mirror.reasoning) != 1 || mirror.reasoning[0] != semantic.Text {
		t.Fatalf("unexpected mirrored reasoning: %+v", mirror.reasoning)
	}
}

func TestHandleDesktopStreamEventDoneEmitsSummaryAndResetsRound(t *testing.T) {
	round := IMRoundState{
		ToolCalls:     2,
		ToolSuccesses: 1,
		ToolFailures:  1,
	}
	round.AppendText("done text")
	mirror := &testDesktopMirror{}
	emitter := &testDesktopEmitter{}

	semantic, ok := HandleDesktopStreamEvent(provider.StreamEvent{
		Type:  provider.StreamEventDone,
		Usage: &provider.TokenUsage{InputTokens: 10, OutputTokens: 20, CacheRead: 5, CacheWrite: 1},
	}, &round, emitter, mirror)
	if !ok {
		t.Fatal("expected done event to be handled")
	}
	if len(emitter.roundSummaries) != 1 || emitter.roundSummaries[0].text != "done text" {
		t.Fatalf("unexpected round summary: %+v", emitter.roundSummaries)
	}
	if len(mirror.flushes) != 1 || !mirror.flushes[0] {
		t.Fatalf("expected rotate flush on done, got %+v", mirror.flushes)
	}
	if round.Text() != "" || round.ToolCalls != 0 || round.ToolSuccesses != 0 || round.ToolFailures != 0 {
		t.Fatalf("expected round reset, got %+v", round)
	}
	if semantic.UsageData["inputTokens"] != 15 {
		t.Fatalf("expected usage summary to include cache read, got %+v", semantic.UsageData)
	}
}

func TestHandleDesktopStreamEventErrorFlushesAndMirrors(t *testing.T) {
	var round IMRoundState
	mirror := &testDesktopMirror{}

	semantic, ok := HandleDesktopStreamEvent(provider.StreamEvent{
		Type:  provider.StreamEventError,
		Error: errors.New("boom"),
	}, &round, nil, mirror)
	if !ok {
		t.Fatal("expected error event to be handled")
	}
	if semantic.ErrorText != "boom" {
		t.Fatalf("unexpected error text: %q", semantic.ErrorText)
	}
	if len(mirror.flushes) != 1 || !mirror.flushes[0] {
		t.Fatalf("expected rotate flush on error, got %+v", mirror.flushes)
	}
	if len(mirror.errors) != 1 || mirror.errors[0] != "boom" {
		t.Fatalf("unexpected mirrored errors: %+v", mirror.errors)
	}
}

func TestDesktopSemanticPreservesToolCallIDForResult(t *testing.T) {
	var round IMRoundState
	emitter := &testDesktopEmitter{}
	mirror := &testDesktopMirror{}
	callID := "call-same-id"
	tool := provider.ToolCallDelta{ID: callID, Name: "read_file", Arguments: []byte(`{"path":"/tmp/a"}`)}

	callSem, ok := HandleDesktopStreamEvent(provider.StreamEvent{
		Type: provider.StreamEventToolCallDone,
		Tool: tool,
	}, &round, emitter, mirror)
	if !ok || callSem.ToolCall == nil {
		t.Fatalf("expected tool call semantic")
	}
	if callSem.ToolCall.ID != callID {
		t.Fatalf("tool call id = %q, want %q", callSem.ToolCall.ID, callID)
	}

	resultSem, ok := HandleDesktopStreamEvent(provider.StreamEvent{
		Type:    provider.StreamEventToolResult,
		Tool:    tool,
		Result:  "ok",
		IsError: false,
	}, &round, emitter, mirror)
	if !ok || resultSem.ToolResult == nil {
		t.Fatalf("expected tool result semantic")
	}
	if resultSem.ToolResult.ID != callID {
		t.Fatalf("tool result id = %q, want %q", resultSem.ToolResult.ID, callID)
	}
}
