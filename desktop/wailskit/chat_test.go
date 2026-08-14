package wailskit

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// TestMergeTunnelUserMessages verifies tunnel-recorded user messages are
// merged into session history (#242), with ID-based dedup (#268).
func TestMergeTunnelUserMessages(t *testing.T) {
	mkEvent := func(text string) session.TunnelEvent {
		data, _ := json.Marshal(map[string]string{"text": text, "message_id": "tm-" + text})
		return session.TunnelEvent{Type: "user_message", Data: data}
	}
	msgs := []SessionMessage{
		{ID: "tm-hello", Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	events := []session.TunnelEvent{
		mkEvent("hello"),                                 // same message_id as persisted msg -> skipped
		mkEvent("from mobile"),                           // new -> appended
		{Type: "other", Data: nil},                       // non user_message -> skipped
		{Type: "user_message", Data: []byte("not-json")}, // unparseable -> skipped
	}

	out := mergeTunnelUserMessages(msgs, events)
	if len(out) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(out), out)
	}
	if out[2].Content != "from mobile" {
		t.Fatalf("expected tunnel message appended, got %+v", out[2])
	}
	if out[2].ID != "tm-from mobile" {
		t.Fatalf("expected message_id preserved, got %q", out[2].ID)
	}

	// Empty events must not alter input.
	same := mergeTunnelUserMessages(msgs, nil)
	if len(same) != len(msgs) {
		t.Fatalf("expected %d messages with no tunnel events, got %d", len(msgs), len(same))
	}
}

// TestMergeTunnelUserMessages_SameTextDistinctIDsKept verifies that tunnel
// events with identical text but distinct message IDs are all preserved —
// the user may legitimately send "yes" twice (#268).
func TestMergeTunnelUserMessages_SameTextDistinctIDsKept(t *testing.T) {
	mkEvent := func(text, id string) session.TunnelEvent {
		data, _ := json.Marshal(map[string]string{"text": text, "message_id": id})
		return session.TunnelEvent{Type: "user_message", Data: data}
	}
	msgs := []SessionMessage{
		{ID: "msg-1", Role: "user", Content: "what do you think?"},
		{Role: "assistant", Content: "do you want to proceed?"},
	}
	events := []session.TunnelEvent{
		mkEvent("yes", "tm-1"),
		mkEvent("yes", "tm-2"), // same text, different message — must be kept
		mkEvent("yes", "tm-1"), // exact replay of the first event — deduped
	}

	out := mergeTunnelUserMessages(msgs, events)
	if len(out) != 4 {
		t.Fatalf("expected 4 messages (2 persisted + 2 distinct tunnel), got %d: %+v", len(out), out)
	}
	if out[2].ID != "tm-1" || out[3].ID != "tm-2" {
		t.Fatalf("expected both tunnel messages kept in order, got %+v", out[2:])
	}
}

// TestMergeTunnelUserMessages_NoIDFallsBackToTextDedup verifies the text
// fallback when a tunnel event carries no message_id (#268): such an event
// matching a persisted user message is deduped, while new text is kept.
func TestMergeTunnelUserMessages_NoIDFallsBackToTextDedup(t *testing.T) {
	mkNoIDEvent := func(text string) session.TunnelEvent {
		data, _ := json.Marshal(map[string]string{"text": text})
		return session.TunnelEvent{Type: "user_message", Data: data}
	}
	msgs := []SessionMessage{
		{ID: "msg-1", Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	events := []session.TunnelEvent{
		mkNoIDEvent("hello"), // no ID, same text as persisted -> deduped
		mkNoIDEvent("hello"), // second ID-less event with same text -> also deduped
		mkNoIDEvent("fresh"), // no ID, new text -> kept
	}

	out := mergeTunnelUserMessages(msgs, events)
	if len(out) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(out), out)
	}
	if out[2].Content != "fresh" || out[2].ID != "" {
		t.Fatalf("expected fresh ID-less tunnel message appended, got %+v", out[2])
	}
}

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
