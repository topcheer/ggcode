package provider

// sa-40 tests for the OpenAI Responses API adapter.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestBuildResponsesInputOrderAndMapping(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: []ContentBlock{TextBlock("you are an agent")}},
		{Role: "user", Content: []ContentBlock{TextBlock("hi")}},
		{Role: "assistant", Content: []ContentBlock{
			TextBlock("let me check"),
			ToolUseBlock("call_1", "read_file", json.RawMessage(`{"path":"a.go"}`)),
		}},
		{Role: "tool", Content: []ContentBlock{ToolResultBlock("call_1", "file body", false)}},
	}
	items := buildResponsesInput(msgs)

	if len(items) != 5 {
		t.Fatalf("want 5 items, got %d: %+v", len(items), items)
	}
	// Order matters: assistant text must precede its function_call item, and
	// the function_call_output must come after the function_call (protocol red
	// line: no role-boundary violation between call and result).
	if items[0].Role != "system" {
		t.Errorf("item0 role = %q, want system", items[0].Role)
	}
	if items[1].Role != "user" || !strings.Contains(string(items[1].Content), "hi") {
		t.Errorf("item1 = %+v, want user text", items[1])
	}
	if items[2].Role != "assistant" || !strings.Contains(string(items[2].Content), "let me check") {
		t.Errorf("item2 = %+v, want assistant text", items[2])
	}
	if items[3].Type != "function_call" || items[3].CallID != "call_1" || items[3].Name != "read_file" {
		t.Errorf("item3 = %+v, want function_call call_1", items[3])
	}
	if items[3].Args != `{"path":"a.go"}` {
		t.Errorf("args = %q", items[3].Args)
	}
	if items[4].Type != "function_call_output" || items[4].CallID != "call_1" || items[4].Output != "file body" {
		t.Errorf("item4 = %+v, want function_call_output", items[4])
	}
}

func TestOpenAIResponsesChatStream(t *testing.T) {
	var gotPath string
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hel\"}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"lo\"}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"call_id\":\"c1\",\"name\":\"run\",\"arguments\":\"\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"c1\",\"name\":\"run\",\"arguments\":\"{\\\"cmd\\\":\\\"ls\\\"}\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":11,\"output_tokens\":7,\"input_tokens_details\":{\"cached_tokens\":3}}}}\n\n")
	}))
	defer srv.Close()

	p := NewOpenAIResponsesProvider("sk-test", "gpt-5-codex", 1024, srv.URL+"/v1")
	ch, err := p.ChatStream(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("hi")}},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	var text strings.Builder
	var done *StreamEvent
	var toolDone *ToolCallDelta
	for ev := range ch {
		switch ev.Type {
		case StreamEventText:
			text.WriteString(ev.Text)
		case StreamEventToolCallDone:
			tc := ev.Tool
			toolDone = &tc
		case StreamEventDone:
			cp := ev
			done = &cp
		case StreamEventError:
			t.Fatalf("unexpected error event: %v", ev.Error)
		}
	}
	if gotPath != "/v1/responses" {
		t.Errorf("request path = %q, want /v1/responses", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if text.String() != "hello" {
		t.Errorf("text = %q, want hello", text.String())
	}
	if toolDone == nil || toolDone.ID != "c1" || toolDone.Name != "run" || string(toolDone.Arguments) != `{"cmd":"ls"}` {
		t.Errorf("toolDone = %+v", toolDone)
	}
	if done == nil || done.Usage == nil || done.Usage.InputTokens != 11 || done.Usage.OutputTokens != 7 || done.Usage.CacheRead != 3 {
		t.Errorf("done = %+v", done)
	}
}

func TestOpenAIResponsesChatStreamFailedEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"boom\"}}}\n\n")
	}))
	defer srv.Close()

	p := NewOpenAIResponsesProvider("k", "m", 0, srv.URL)
	ch, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: []ContentBlock{TextBlock("x")}}}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	for ev := range ch {
		if ev.Type == StreamEventError {
			if !strings.Contains(ev.Error.Error(), "boom") {
				t.Fatalf("error = %v, want boom", ev.Error)
			}
			return
		}
	}
	t.Fatal("no error event")
}

func TestOpenAIResponsesChatNonStreaming(t *testing.T) {
	var reqBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &reqBody)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"output":[{"type":"message","content":[{"type":"output_text","text":"answer"}]},{"type":"function_call","call_id":"c9","name":"grep","arguments":"{\"q\":\"x\"}"}],"usage":{"input_tokens":5,"output_tokens":2},"status":"completed"}`)
	}))
	defer srv.Close()

	p := NewOpenAIResponsesProvider("k", "gpt-5-codex", 512, srv.URL)
	p.SetReasoningEffort("turbo")
	resp, err := p.Chat(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("q")}},
	}, []ToolDefinition{{Name: "grep", Description: "search", Parameters: json.RawMessage(`{"type":"object"}`)}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reqBody["model"] != "gpt-5-codex" {
		t.Errorf("model = %v", reqBody["model"])
	}
	if reqBody["max_output_tokens"] != float64(512) {
		t.Errorf("max_output_tokens = %v", reqBody["max_output_tokens"])
	}
	reasoning, _ := reqBody["reasoning"].(map[string]any)
	if reasoning == nil || reasoning["effort"] != "high" {
		t.Errorf("reasoning = %v, want effort high (turbo normalized)", reqBody["reasoning"])
	}
	tools, _ := reqBody["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v", reqBody["tools"])
	}
	if tool0, ok := tools[0].(map[string]any); !ok || tool0["type"] != "function" {
		t.Errorf("tool type = %v", tools[0])
	}
	if store, ok := reqBody["store"].(bool); !ok || store {
		t.Errorf("store = %v, want false (stateless replay)", reqBody["store"])
	}

	if resp.Message.Role != "assistant" {
		t.Errorf("role = %q", resp.Message.Role)
	}
	var text, toolUse bool
	for _, b := range resp.Message.Content {
		switch b.Type {
		case "text":
			if b.Text == "answer" {
				text = true
			}
		case "tool_use":
			if b.ToolID == "c9" && b.ToolName == "grep" && string(b.Input) == `{"q":"x"}` {
				toolUse = true
			}
		}
	}
	if !text || !toolUse {
		t.Errorf("content blocks missing: text=%v toolUse=%v", text, toolUse)
	}
	if resp.Usage.InputTokens != 5 || resp.Usage.OutputTokens != 2 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if resp.StopReason != "" {
		t.Errorf("stopReason = %q, want empty for completed", resp.StopReason)
	}
}

func testResolvedEndpoint(_, protocol, baseURL string) *config.ResolvedEndpoint {
	return &config.ResolvedEndpoint{Protocol: protocol, BaseURL: baseURL, APIKey: "k", Model: "m", MaxTokens: 1024}
}

func TestRegistryDispatchesOpenAIResponses(t *testing.T) {
	p, err := NewProvider(testResolvedEndpoint("", "openai-responses", "https://api.openai.com/v1"))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if _, ok := p.(*OpenAIResponsesProvider); !ok {
		t.Fatalf("want *OpenAIResponsesProvider, got %T", p)
	}

	// URL-sniff fallback: protocol "openai" + /responses suffix.
	p2, err := NewProvider(testResolvedEndpoint("", "openai", "https://api.openai.com/v1/responses"))
	if err != nil {
		t.Fatalf("NewProvider(sniff): %v", err)
	}
	if _, ok := p2.(*OpenAIResponsesProvider); !ok {
		t.Fatalf("sniff: want *OpenAIResponsesProvider, got %T", p2)
	}

	// Plain chat-completions endpoint must stay on the legacy adapter.
	p3, err := NewProvider(testResolvedEndpoint("", "openai", "https://api.openai.com/v1"))
	if err != nil {
		t.Fatalf("NewProvider(legacy): %v", err)
	}
	if _, ok := p3.(*OpenAIProvider); !ok {
		t.Fatalf("legacy: want *OpenAIProvider, got %T", p3)
	}
}

func TestNormalizeResponsesEffort(t *testing.T) {
	cases := map[string]string{
		"":        "",
		"turbo":   "high",
		"max":     "high",
		"minimal": "low",
		"LOW":     "low",
		"medium":  "medium",
	}
	for in, want := range cases {
		if got := normalizeResponsesEffort(in); got != want {
			t.Errorf("normalizeResponsesEffort(%q) = %q, want %q", in, got, want)
		}
	}
}
