package provider

// sa-54 tests: stateless encrypted reasoning round-trip for the OpenAI
// Responses API adapter. With store=false the API cannot reconstruct a
// reasoning model's chain-of-thought between tool calls unless the client
// replays the reasoning items it received (via
// include=["reasoning.encrypted_content"]) verbatim on the next request.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sa54ReasoningItemRaw = `{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"considering paths"}],"encrypted_content":"ENC-1"}`

// A full agent loop: user prompt -> assistant (reasoning + tool call) -> tool
// result. The next request must replay the reasoning item verbatim, before
// its function_call item.
func TestBuildResponsesInputEncryptedReasoningReplay(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("list files")}},
		{Role: "assistant", Content: []ContentBlock{
			{Type: "thinking", ThinkingSignature: sa54ReasoningItemRaw},
			ToolUseBlock("call_1", "ls", json.RawMessage(`{"path":"."}`)),
		}},
		{Role: "tool", Content: []ContentBlock{ToolResultBlock("call_1", "a.go b.go", false)}},
	}
	items := buildResponsesInput(msgs)

	if len(items) != 4 {
		t.Fatalf("want 4 items, got %d: %+v", len(items), items)
	}
	// Reasoning item first, replayed losslessly.
	if items[1].Type != "reasoning" || items[1].ID != "rs_1" || items[1].EncryptedContent != "ENC-1" {
		t.Errorf("item1 = %+v, want replayed reasoning item rs_1", items[1])
	}
	if !strings.Contains(string(items[1].Summary), "considering paths") {
		t.Errorf("summary = %s, want summary_text part preserved", items[1].Summary)
	}
	wire, err := json.Marshal(items[1])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(wire), `"type":"reasoning"`) || !strings.Contains(string(wire), `"encrypted_content":"ENC-1"`) {
		t.Errorf("wire form %s must round-trip type + encrypted_content", wire)
	}
	// Tool call/result ordering is untouched.
	if items[2].Type != "function_call" || items[2].CallID != "call_1" {
		t.Errorf("item2 = %+v, want function_call", items[2])
	}
	if items[3].Type != "function_call_output" || items[3].CallID != "call_1" {
		t.Errorf("item3 = %+v, want function_call_output", items[3])
	}
}

// decodeResponsesReasoningItem must be a strict gate: only genuine Responses
// reasoning items with encrypted content pass.
func TestDecodeResponsesReasoningItemGating(t *testing.T) {
	if it, ok := decodeResponsesReasoningItem(sa54ReasoningItemRaw); !ok || it.ID != "rs_1" {
		t.Errorf("valid item rejected: ok=%v item=%+v", ok, it)
	}
	// Anthropic signature / Gemini blob / garbage: dropped.
	for _, bad := range []string{
		"", `not json`, `{"type":"message"}`, `{"type":"reasoning"}`,
		`{"type":"reasoning","id":"rs_2"}`, // no encrypted content: unreplayable
	} {
		if _, ok := decodeResponsesReasoningItem(bad); ok {
			t.Errorf("%q should not be replayable", bad)
		}
	}
}

// Non-streaming Chat: reasoning items in the output array become thinking
// blocks carrying the raw item for later replay.
func TestOpenAIResponsesChatCapturesReasoningItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"output":[`+
			`{"type":"reasoning","id":"rs_1","encrypted_content":"ENC-1"},`+
			`{"type":"function_call","call_id":"c1","name":"t","arguments":"{}"},`+
			`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}`+
			`],"usage":{"input_tokens":5,"output_tokens":3},"status":"completed"}`)
	}))
	defer srv.Close()

	p := NewOpenAIResponsesProvider("k", "m", 0, srv.URL)
	resp, err := p.Chat(context.Background(), []Message{{Role: "user", Content: []ContentBlock{TextBlock("x")}}}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(resp.Message.Content) != 3 {
		t.Fatalf("blocks = %+v, want 3", resp.Message.Content)
	}
	th := resp.Message.Content[0]
	if th.Type != "thinking" || !strings.Contains(th.ThinkingSignature, "rs_1") || !strings.Contains(th.ThinkingSignature, "ENC-1") {
		t.Errorf("block0 = %+v, want thinking block with raw reasoning item", th)
	}
	// And the captured block replays cleanly.
	if _, ok := decodeResponsesReasoningItem(th.ThinkingSignature); !ok {
		t.Error("captured block should decode as a replayable reasoning item")
	}
}

// Streaming: output_item.done for a reasoning item arrives as a signature-only
// reasoning stream event (empty Text so UIs render nothing opaque).
func TestOpenAIResponsesChatStreamCapturesEncryptedItem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"hmm\"}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"id\":\"rs_9\",\"encrypted_content\":\"E9\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"c1\",\"name\":\"run\",\"arguments\":\"{}\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
	}))
	defer srv.Close()

	p := NewOpenAIResponsesProvider("k", "m", 0, srv.URL)
	ch, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: []ContentBlock{TextBlock("x")}}}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var itemEv, summaryEv *StreamEvent
	var gotTool bool
	for ev := range ch {
		e := ev
		switch {
		case e.Type == StreamEventReasoning && e.ThinkingSignature != "":
			itemEv = &e
		case e.Type == StreamEventReasoning && e.Text != "":
			summaryEv = &e
		case e.Type == StreamEventToolCallDone:
			gotTool = true
		}
	}
	if summaryEv == nil || summaryEv.Text != "hmm" {
		t.Errorf("summary delta event = %+v", summaryEv)
	}
	if itemEv == nil || !strings.Contains(itemEv.ThinkingSignature, "rs_9") || itemEv.Text != "" {
		t.Errorf("encrypted item event = %+v, want signature-only capture", itemEv)
	}
	if !gotTool {
		t.Error("function_call done event lost")
	}
	// Round-trip: what the stream produced, the input builder replays.
	if itemEv != nil {
		blk := ContentBlock{Type: "thinking", ThinkingSignature: itemEv.ThinkingSignature}
		if it, ok := decodeResponsesReasoningItem(blk.ThinkingSignature); !ok || it.ID != "rs_9" || it.EncryptedContent != "E9" {
			t.Errorf("round-trip item = %+v ok=%v", it, ok)
		}
	}
}

// Every request must ask for encrypted reasoning tokens.
func TestBuildResponsesRequestIncludeEncryptedReasoning(t *testing.T) {
	p := NewOpenAIResponsesProvider("k", "m", 0, "http://127.0.0.1:1/v1")
	for _, stream := range []bool{false, true} {
		req, err := p.buildRequest([]Message{{Role: "user", Content: []ContentBlock{TextBlock("x")}}}, nil, stream)
		if err != nil {
			t.Fatalf("buildRequest: %v", err)
		}
		if len(req.Include) != 1 || req.Include[0] != responsesIncludeEncryptedReasoning {
			t.Errorf("include = %v, want [%s]", req.Include, responsesIncludeEncryptedReasoning)
		}
		wire, _ := json.Marshal(req)
		if !strings.Contains(string(wire), `"include":["reasoning.encrypted_content"]`) {
			t.Errorf("wire = %s", wire)
		}
	}
}
