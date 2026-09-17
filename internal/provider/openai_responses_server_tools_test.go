package provider

// sa-62 tests for OpenAI Responses hosted (server-side) tools (web_search).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

const responsesWebSearchCallRaw = `{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"search","query":"golang generics"}}`

func TestResponsesSetServerToolsFailClosed(t *testing.T) {
	p := NewOpenAIResponsesProvider("k", "m", 100, "http://127.0.0.1:1")

	// Unknown declarations are ignored (fail closed).
	p.SetServerTools([]ServerToolConfig{{Type: "code_interpreter"}, {Type: "file_search"}})
	if p.hasHostedTools() {
		t.Fatalf("unknown server tools must be dropped")
	}

	// Known declarations are kept.
	p.SetServerTools([]ServerToolConfig{
		{Type: "web_search"},
		{Type: "WEB_SEARCH_PREVIEW"}, // case-insensitive
		{Type: "web_search_2025_08_26"},
		{Type: "bogus_tool"},
	})
	if len(p.serverTools) != 3 {
		t.Fatalf("want 3 kept server tools, got %d", len(p.serverTools))
	}

	// A reload with no recognized entries must not wipe the previous set.
	p.SetServerTools([]ServerToolConfig{{Type: "bogus_tool"}})
	if !p.hasHostedTools() || len(p.serverTools) != 3 {
		t.Fatalf("empty recognized set must keep prior tools, got %d", len(p.serverTools))
	}
}

func TestResponsesBuildRequestHostedTools(t *testing.T) {
	p := NewOpenAIResponsesProvider("k", "m", 100, "http://127.0.0.1:1")
	p.SetServerTools([]ServerToolConfig{{Type: "web_search"}})
	req, err := p.buildRequest([]Message{
		{Role: "user", Content: []ContentBlock{TextBlock("news?")}},
	}, []ToolDefinition{{Name: "read_file", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`)}}, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if len(req.Tools) != 2 {
		t.Fatalf("want 2 tools, got %d", len(req.Tools))
	}
	fn := req.Tools[0]
	if fn.Type != "function" || fn.Name != "read_file" {
		t.Fatalf("tool0 = %+v, want function read_file", fn)
	}
	hosted := req.Tools[1]
	if hosted.Type != "web_search" {
		t.Fatalf("tool1 type = %q, want web_search", hosted.Type)
	}
	if hosted.Name != "" || hosted.Description != "" || hosted.Parameters != nil {
		t.Fatalf("hosted tool must carry type only, got %+v", hosted)
	}
	b, _ := json.Marshal(hosted)
	if string(b) != `{"type":"web_search"}` {
		t.Fatalf("hosted tool JSON = %s", b)
	}
}

func TestResponsesChatWebSearchCallItem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":[{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"search","query":"golang generics"}},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"here you go"}]}],"usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
	defer srv.Close()

	p := NewOpenAIResponsesProvider("k", "m", 100, srv.URL)
	resp, err := p.Chat(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("q")}},
	}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	var st *ContentBlock
	for i := range resp.Message.Content {
		if resp.Message.Content[i].Type == "server_tool" {
			st = &resp.Message.Content[i]
		}
	}
	if st == nil {
		t.Fatalf("no server_tool block, content = %+v", resp.Message.Content)
	}
	if st.ServerTool != "web_search" || st.ID != "ws_1" {
		t.Fatalf("carrier = %+v", st)
	}
	if strings.Contains(string(st.Raw), "golang generics") == false {
		t.Fatalf("raw not verbatim: %s", st.Raw)
	}
	if resp.Message.Content[len(resp.Message.Content)-1].Type != "text" {
		t.Fatalf("text block missing")
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestResponsesChatStreamHostedWebSearch(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		body = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		frames := []string{
			`{"type":"response.output_item.added","item":{"type":"web_search_call","id":"ws_1","status":"in_progress"}}`,
			`{"type":"response.output_item.done","item":` + responsesWebSearchCallRaw + `}`,
			`{"type":"response.completed","response":{"usage":{"input_tokens":7,"output_tokens":3}}}`,
		}
		for _, f := range frames {
			_, _ = w.Write([]byte("data: " + f + "\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer srv.Close()

	p := NewOpenAIResponsesProvider("k", "m", 100, srv.URL)
	p.SetServerTools([]ServerToolConfig{{Type: "web_search"}})
	ch, err := p.ChatStream(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("q")}},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var serverToolEvents int
	var done bool
	for ev := range ch {
		switch ev.Type {
		case StreamEventServerTool:
			serverToolEvents++
			if ev.Block.ID != "ws_1" || !strings.Contains(string(ev.Block.Raw), "golang generics") {
				t.Fatalf("server tool block = %+v", ev.Block)
			}
		case StreamEventDone:
			done = true
			if ev.Usage == nil || ev.Usage.InputTokens != 7 {
				t.Fatalf("done usage = %+v", ev.Usage)
			}
		case StreamEventError:
			t.Fatalf("stream error: %v", ev.Error)
		}
	}
	// Exactly one emission: output_item.added must be ignored, done emitted once.
	if serverToolEvents != 1 {
		t.Fatalf("server tool events = %d, want 1", serverToolEvents)
	}
	if !done {
		t.Fatalf("stream never finished")
	}
	// Request must carry the hosted tool declaration alongside function tools.
	if !strings.Contains(body, `"type":"web_search"`) {
		t.Fatalf("request body missing hosted tool: %s", body)
	}
}

func TestBuildResponsesInputHostedToolReplay(t *testing.T) {
	foreign := json.RawMessage(`{"type":"server_tool_use","id":"srvtoolu_01","name":"web_search"}`)
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("q")}},
		{Role: "assistant", Content: []ContentBlock{
			TextBlock("searching"),
			{Type: "server_tool", ServerTool: "web_search", ID: "ws_1", Raw: json.RawMessage(responsesWebSearchCallRaw)},
			{Type: "server_tool", ServerTool: "web_search", ID: "srvtoolu_01", Raw: foreign},
		}},
	}
	items := buildResponsesInput(msgs)
	if len(items) != 3 { // user text, assistant text message, verbatim web_search_call
		t.Fatalf("want 3 items, got %d: %+v", len(items), items)
	}
	if items[1].Role != "assistant" || !strings.Contains(string(items[1].Content), "searching") {
		t.Fatalf("item1 = %+v, want assistant text", items[1])
	}
	// The hosted item must round-trip byte-for-byte.
	b, err := json.Marshal(items[2])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != responsesWebSearchCallRaw {
		t.Fatalf("replay not verbatim:\n got %s\nwant %s", b, responsesWebSearchCallRaw)
	}
	// Foreign provider blocks must never leak into Responses input.
	for _, it := range items {
		if it.RawReplay != nil && strings.Contains(string(it.RawReplay), "server_tool_use") {
			t.Fatalf("foreign block leaked: %s", it.RawReplay)
		}
	}
}

func TestResponsesInputItemMarshalModes(t *testing.T) {
	structured := responsesInputItem{Type: "function_call_output", CallID: "c1", Output: "ok"}
	b, _ := json.Marshal(structured)
	if !strings.Contains(string(b), `"call_id":"c1"`) {
		t.Fatalf("structured marshal = %s", b)
	}

	raw := responsesInputItem{RawReplay: json.RawMessage(responsesWebSearchCallRaw)}
	b, _ = json.Marshal(raw)
	if string(b) != responsesWebSearchCallRaw {
		t.Fatalf("raw replay marshal = %s", b)
	}
}

func TestResponsesRegistryPassesServerTools(t *testing.T) {
	resolved := &config.ResolvedEndpoint{
		Protocol: "openai-responses",
		APIKey:   "k",
		Model:    "m",
		ServerTools: []config.ServerToolConfig{
			{Type: "web_search"},
			{Type: "totally_bogus"},
		},
	}
	p, err := NewProvider(resolved)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	rp, ok := p.(*OpenAIResponsesProvider)
	if !ok {
		t.Fatalf("provider type = %T", p)
	}
	if len(rp.serverTools) != 1 || rp.serverTools[0].Type != "web_search" {
		t.Fatalf("server tools = %+v", rp.serverTools)
	}
}
