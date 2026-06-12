package wailskit

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

func TestEmitNormalizesReasoningForFrontend(t *testing.T) {
	var (
		eventType string
		payload   map[string]string
	)
	bridge := &ChatBridge{
		OnStreamEvent: func(kind string, raw json.RawMessage) {
			eventType = kind
			_ = json.Unmarshal(raw, &payload)
		},
	}

	bridge.emit(provider.StreamEvent{
		Type: provider.StreamEventReasoning,
		Text: "__redacted_thinking__",
	})

	if eventType != "reasoning" {
		t.Fatalf("expected reasoning event, got %q", eventType)
	}
	if payload["content"] != "Reasoning hidden by model." {
		t.Fatalf("expected normalized reasoning placeholder, got %+v", payload)
	}
}

func TestEmitToolResultUsesPreviewPayload(t *testing.T) {
	var (
		eventType string
		payload   map[string]interface{}
	)
	bridge := &ChatBridge{
		OnStreamEvent: func(kind string, raw json.RawMessage) {
			eventType = kind
			_ = json.Unmarshal(raw, &payload)
		},
	}

	long := strings.Repeat("x", 700)
	bridge.emit(provider.StreamEvent{
		Type:   provider.StreamEventToolResult,
		Tool:   provider.ToolCallDelta{ID: "tool-1", Name: "read_file", Arguments: []byte(`{"path":"/tmp/test.txt"}`)},
		Result: long,
	})

	if eventType != "tool_result" {
		t.Fatalf("expected tool_result event, got %q", eventType)
	}
	result, _ := payload["result"].(string)
	if !strings.HasSuffix(result, "...") {
		t.Fatalf("expected preview payload to be truncated, got length %d", len(result))
	}
	if len([]rune(result)) != 500 {
		t.Fatalf("expected 500-rune preview, got %d", len([]rune(result)))
	}
}

func TestEmitToolCallUsesSharedPresentation(t *testing.T) {
	var (
		eventType string
		payload   map[string]interface{}
	)
	bridge := &ChatBridge{
		OnStreamEvent: func(kind string, raw json.RawMessage) {
			eventType = kind
			_ = json.Unmarshal(raw, &payload)
		},
	}

	bridge.emit(provider.StreamEvent{
		Type: provider.StreamEventToolCallDone,
		Tool: provider.ToolCallDelta{ID: "tool-1", Name: "read_file", Arguments: []byte(`{"path":"/tmp/test.txt"}`)},
	})

	if eventType != "tool_call_done" {
		t.Fatalf("expected tool_call_done event, got %q", eventType)
	}
	if payload["displayName"] != "Read" {
		t.Fatalf("expected shared display name, got %+v", payload)
	}
	if payload["detail"] != "/tmp/test.txt" {
		t.Fatalf("expected shared detail, got %+v", payload)
	}
}

func TestShouldEmitSwarmBoardUpdateFiltersHighFrequencyText(t *testing.T) {
	for _, eventType := range []string{"team_created", "teammate_spawned", "teammate_working", "teammate_idle", "team_board_updated"} {
		if !shouldEmitSwarmBoardUpdate(eventType) {
			t.Fatalf("expected %s to refresh team board", eventType)
		}
	}
	for _, eventType := range []string{"teammate_text", "teammate_reasoning", "teammate_tool_call", "teammate_tool_result"} {
		if shouldEmitSwarmBoardUpdate(eventType) {
			t.Fatalf("expected %s not to refresh team board", eventType)
		}
	}
}

func TestEmitBuildsLiveSessionHistory(t *testing.T) {
	bridge := &ChatBridge{
		currentSes: &session.Session{},
	}

	bridge.appendLiveUserMessage("hello")
	bridge.emit(provider.StreamEvent{
		Type: provider.StreamEventReasoning,
		Text: "thinking",
	})
	bridge.emit(provider.StreamEvent{
		Type: provider.StreamEventText,
		Text: "answer",
	})
	bridge.emit(provider.StreamEvent{
		Type: provider.StreamEventToolCallDone,
		Tool: provider.ToolCallDelta{ID: "tool-1", Name: "read_file", Arguments: []byte(`{"path":"/tmp/test.txt"}`)},
	})
	bridge.emit(provider.StreamEvent{
		Type:    provider.StreamEventToolResult,
		Tool:    provider.ToolCallDelta{ID: "tool-1", Name: "read_file", Arguments: []byte(`{"path":"/tmp/test.txt"}`)},
		Result:  "file contents",
		IsError: false,
	})
	bridge.emit(provider.StreamEvent{
		Type: provider.StreamEventDone,
		Usage: &provider.TokenUsage{
			InputTokens:  1,
			OutputTokens: 2,
		},
	})

	history := bridge.CurrentSessionHistory()
	if len(history) != 4 {
		t.Fatalf("expected 4 live history entries, got %d: %+v", len(history), history)
	}
	if history[0].Role != "user" || history[0].Content != "hello" {
		t.Fatalf("unexpected user entry: %+v", history[0])
	}
	if history[1].Role != "reasoning" || history[1].Content != "thinking" || history[1].Streaming {
		t.Fatalf("unexpected reasoning entry: %+v", history[1])
	}
	if history[2].Role != "assistant" || history[2].Content != "answer" || history[2].Streaming {
		t.Fatalf("unexpected assistant entry: %+v", history[2])
	}
	if history[3].Role != "tool" || history[3].ToolID != "tool-1" || history[3].Content == "" || history[3].Streaming {
		t.Fatalf("unexpected tool entry: %+v", history[3])
	}
}

func TestBuildSessionHistorySkipsSystemMessages(t *testing.T) {
	history := buildSessionHistoryFromMessages([]provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "Turn #1 · TTFT 1s · Dur 2s · Tools 0"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "answer"}}},
	})
	if len(history) != 2 {
		t.Fatalf("expected system message to be skipped, got %d entries: %+v", len(history), history)
	}
	if history[0].Role != "user" || history[0].Content != "hello" {
		t.Fatalf("unexpected first entry: %+v", history[0])
	}
	if history[1].Role != "assistant" || history[1].Content != "answer" {
		t.Fatalf("unexpected second entry: %+v", history[1])
	}
}
