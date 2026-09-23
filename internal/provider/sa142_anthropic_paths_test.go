package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Tests for anthropic.go request/response paths driven through a real
// httptest server (FailureAtlas-style failure injection + happy paths).

// newSA142AnthropicServer spins up a fake Anthropic Messages API.
func newSA142AnthropicServer(t *testing.T, handler http.HandlerFunc) *AnthropicProvider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewAnthropicProviderWithBaseURL("test-key", "test-model", 9999, server.URL)
}

func sa142ReadBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Errorf("decode request body: %v", err)
	}
	return body
}

// TestSA142_AnthropicChatText: non-streaming happy path relays content,
// usage, stop reason, and captures the PTC container from the response.
func TestSA142_AnthropicChatText(t *testing.T) {
	var gotModel any
	p := newSA142AnthropicServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		body := sa142ReadBody(t, r)
		gotModel = body["model"]
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant","model":"test-model",`+
			`"content":[{"type":"text","text":"Hello sa142"}],`+
			`"stop_reason":"end_turn","stop_sequence":null,`+
			`"usage":{"input_tokens":10,"output_tokens":5},`+
			`"container":{"id":"cnt_1","expires_at":"2030-01-01T00:00:00Z"}}`)
	})
	resp, err := p.Chat(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil)
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if gotModel != "test-model" {
		t.Fatalf("request model = %v, want test-model", gotModel)
	}
	if len(resp.Message.Content) != 1 || resp.Message.Content[0].Text != "Hello sa142" {
		t.Fatalf("content = %+v", resp.Message.Content)
	}
	if resp.StopReason != "end_turn" {
		t.Fatalf("StopReason = %q, want end_turn", resp.StopReason)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	// PTC container captured from the response and still fresh.
	if id, ok := p.freshPTCContainer(); !ok || id != "cnt_1" {
		t.Fatalf("freshPTCContainer = %q, %v; want cnt_1, true", id, ok)
	}
}

// TestSA142_AnthropicChatToolUse: tool_use blocks are converted losslessly.
func TestSA142_AnthropicChatToolUse(t *testing.T) {
	p := newSA142AnthropicServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg_2","type":"message","role":"assistant","model":"test-model",`+
			`"content":[{"type":"text","text":"checking"},`+
			`{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"SF"}}],`+
			`"stop_reason":"tool_use","usage":{"input_tokens":20,"output_tokens":9}}`)
	})
	resp, err := p.Chat(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "weather?"}}}}, nil)
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if len(resp.Message.Content) != 2 {
		t.Fatalf("content blocks = %d, want 2", len(resp.Message.Content))
	}
	tb := resp.Message.Content[1]
	if tb.Type != "tool_use" || tb.ToolName != "get_weather" || tb.ToolID != "toolu_1" {
		t.Fatalf("tool block = %+v", tb)
	}
	if !strings.Contains(string(tb.Input), "SF") {
		t.Fatalf("tool input = %s, want city SF", tb.Input)
	}
	if resp.StopReason != "tool_use" {
		t.Fatalf("StopReason = %q, want tool_use", resp.StopReason)
	}
}

// TestSA142_AnthropicChatAuthError: a 401 surfaces as an error (no retry
// storm, no fabricated response).
func TestSA142_AnthropicChatAuthError(t *testing.T) {
	p := newSA142AnthropicServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	})
	if _, err := p.Chat(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil); err == nil {
		t.Fatal("expected error on 401")
	}
}

// TestSA142_AnthropicStreamText: full SSE text stream produces text chunks
// plus a Done event carrying usage.
func TestSA142_AnthropicStreamText(t *testing.T) {
	p := newSA142AnthropicServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_3\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"test-model\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":11,\"output_tokens\":1}}}\n\n")
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello \"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"世界\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		fl.Flush()
	})
	ch, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil)
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}
	var text strings.Builder
	var done *StreamEvent
	timeout := time.After(15 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break loop
			}
			switch ev.Type {
			case StreamEventText:
				text.WriteString(ev.Text)
			case StreamEventError:
				t.Fatalf("unexpected stream error: %v", ev.Error)
			case StreamEventDone:
				e := ev
				done = &e
			}
		case <-timeout:
			t.Fatal("stream timed out")
		}
	}
	if text.String() != "Hello 世界" {
		t.Fatalf("text = %q", text.String())
	}
	if done == nil {
		t.Fatal("no Done event")
	}
	if done.Usage == nil || done.Usage.InputTokens != 11 || done.Usage.OutputTokens != 7 {
		t.Fatalf("Done usage = %+v", done.Usage)
	}
}

// TestSA142_AnthropicStreamToolUse: input_json_delta fragments accumulate
// into one ToolCallDone with the merged arguments.
func TestSA142_AnthropicStreamToolUse(t *testing.T) {
	p := newSA142AnthropicServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_4\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"test-model\",\"content\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}}\n\n")
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_9\",\"name\":\"get_weather\",\"input\":{}}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"city\\\":\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"SF\\\"}\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":6}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		fl.Flush()
	})
	ch, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "weather?"}}}}, nil)
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}
	var toolDone *ToolCallDelta
	timeout := time.After(15 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break loop
			}
			switch ev.Type {
			case StreamEventError:
				t.Fatalf("unexpected stream error: %v", ev.Error)
			case StreamEventToolCallDone:
				tc := ev.Tool
				toolDone = &tc
			}
		case <-timeout:
			t.Fatal("stream timed out")
		}
	}
	if toolDone == nil {
		t.Fatal("no ToolCallDone event")
	}
	if toolDone.ID != "toolu_9" || toolDone.Name != "get_weather" {
		t.Fatalf("tool call = %+v", toolDone)
	}
	var args map[string]string
	if err := json.Unmarshal(toolDone.Arguments, &args); err != nil {
		t.Fatalf("arguments not valid JSON: %v (%s)", err, toolDone.Arguments)
	}
	if args["city"] != "SF" {
		t.Fatalf("arguments = %v, want city=SF", args)
	}
}

// TestSA142_AnthropicCountTokensRemote: first calibration runs synchronously
// against the count_tokens endpoint and returns the REAL count.
func TestSA142_AnthropicCountTokensRemote(t *testing.T) {
	var hitCount int
	p := newSA142AnthropicServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "count_tokens") {
			hitCount++
			fmt.Fprint(w, `{"input_tokens":42}`)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	})
	msgs := []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "count me"}}}}
	n, err := p.CountTokens(context.Background(), msgs)
	if err != nil {
		t.Fatalf("CountTokens error: %v", err)
	}
	if n != 42 {
		t.Fatalf("CountTokens = %d, want 42 (remote count)", n)
	}
	if hitCount != 1 {
		t.Fatalf("count_tokens hits = %d, want 1", hitCount)
	}
	if p.calibrator.currentRatio() == 1.0 {
		t.Fatal("calibration did not update the ratio")
	}
}

// TestSA142_AnthropicCountTokensUnsupported: a 404 count_tokens endpoint
// permanently disables remote calibration and falls back to the local
// estimate.
func TestSA142_AnthropicCountTokensUnsupported(t *testing.T) {
	p := newSA142AnthropicServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "count_tokens") {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"type":"error","error":{"type":"not_found_error","message":"route not found"}}`)
			return
		}
		fmt.Fprint(w, `{"id":"msg_5","type":"message","role":"assistant","model":"m","content":[],"usage":{}}`)
	})
	msgs := []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}
	n, err := p.CountTokens(context.Background(), msgs)
	if err != nil {
		t.Fatalf("CountTokens error: %v", err)
	}
	if n <= 0 {
		t.Fatalf("local estimate = %d, want > 0", n)
	}
	if p.calibrator == nil || p.calibrator.enabled {
		t.Fatal("calibrator should be disabled after a 404 count_tokens endpoint")
	}
	// A second call takes the fast path with the local estimate.
	if n2, err := p.CountTokens(context.Background(), msgs); err != nil || n2 != n {
		t.Fatalf("second CountTokens = %d, %v; want %d, nil", n2, err, n)
	}
}

// TestSA142_AnthropicAccessorsAndSampling: simple getters/setters, the
// sampling-override precedence, header injection, effort-carrier hysteresis,
// and probeChat all behave per contract.
func TestSA142_AnthropicAccessorsAndSampling(t *testing.T) {
	p := newSA142AnthropicServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg_6","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
	})

	if got := p.ModelName(); got != "test-model" {
		t.Fatalf("ModelName = %q", got)
	}
	p.SetMaxTokens(777)
	if p.maxTokens != 777 {
		t.Fatalf("SetMaxTokens ignored: %d", p.maxTokens)
	}
	p.SetMaxTokens(0) // zero is ignored, not destructive
	if p.maxTokens != 777 {
		t.Fatalf("SetMaxTokens(0) clobbered the budget: %d", p.maxTokens)
	}
	p.SetStrictTools(map[string]bool{"run_cmd": true})
	if !p.strictTools["run_cmd"] {
		t.Fatal("SetStrictTools ignored")
	}
	p.SetMemoryTool(true)
	if !p.MemoryToolEnabled() {
		t.Fatal("MemoryToolEnabled after SetMemoryTool(true)")
	}
	p.SetToolChoice("Required")
	if got := p.ToolChoice(); got != "required" {
		t.Fatalf("ToolChoice = %q, want lowercased required", got)
	}
	p.SetTemperature(0.7)
	p.SetTopP(0.9)
	if p.Temperature() != 0.7 || p.TopP() != 0.9 {
		t.Fatalf("sampling accessors = %v/%v", p.Temperature(), p.TopP())
	}
	// Sampling override wins over the configured budget (#2248).
	p.SetSamplingOverride(&SamplingOverride{MaxTokens: 500})
	if got := p.effectiveMaxTokens(); got != 500 {
		t.Fatalf("effectiveMaxTokens with override = %d, want 500", got)
	}
	if got := p.SamplingOverride(); got == nil || got.MaxTokens != 500 {
		t.Fatalf("SamplingOverride getter = %+v", got)
	}
	p.SetSamplingOverride(nil)
	p.SetAdaptiveCap(nil)
	if got := p.effectiveMaxTokens(); got != 777 {
		t.Fatalf("effectiveMaxTokens without override = %d, want 777", got)
	}

	// Runtime header injection + session ID.
	h := http.Header{}
	h.Set("X-SA142", "1")
	p.UpdateRuntimeHeaders(h)
	p.SetSessionID("sess-sa142")
	snap := p.transport.snapshotHeaders()
	if snap.Get("X-SA142") != "1" || snap.Get("GGCode-SessionID") != "sess-sa142" {
		t.Fatalf("injected headers = %v", snap)
	}
	p.SetSessionID("") // empty is a no-op, not a panic
	rl := p.RateLimitInfo()
	_ = rl // must not panic with an initialized transport

	// Effort-carrier hysteresis: two consecutive calls at the same level
	// establish the carrier (Anthropic effort guidance, 2026).
	p.SetReasoningEffort("high")
	p.effortCarrier.Store(true)
	if p.beginEffortTracking() {
		t.Fatal("first call at a level must not establish the carrier")
	}
	if !p.beginEffortTracking() {
		t.Fatal("second consecutive call at the same level must establish the carrier")
	}
	p.SetReasoningEffort("") // oscillation resets the stability window
	if p.beginEffortTracking() {
		t.Fatal("empty effort must reset the carrier window")
	}

	// probeChat: single messages request without retry bookkeeping.
	if err := p.probeChat(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}); err != nil {
		t.Fatalf("probeChat error: %v", err)
	}
}

// TestSA142_AnthropicPTCAndServerTools: PTC container freshness margin and
// server-tool declaration flags.
func TestSA142_AnthropicPTCAndServerTools(t *testing.T) {
	p := newSA142AnthropicServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	// Container inside the 30s safety margin must be treated as expired.
	p.storePTCContainer("cnt_exp", time.Now().Add(10*time.Second))
	if id, ok := p.freshPTCContainer(); ok {
		t.Fatalf("freshPTCContainer inside expiry margin = %q, %v; want expired", id, ok)
	}
	p.storePTCContainer("", time.Time{})
	if _, ok := p.freshPTCContainer(); ok {
		t.Fatal("empty container id must report not-fresh")
	}

	if p.ptcCodeExecutionEnabled() {
		t.Fatal("PTC must be off before any server tool declaration")
	}
	if opts := p.ptcRequestOptions(); opts != nil {
		t.Fatal("ptcRequestOptions must be nil without PTC")
	}
	p.SetServerTools([]ServerToolConfig{{Type: "code_execution_20260120"}})
	if !p.ptcCodeExecutionEnabled() {
		t.Fatal("code_execution declaration must enable PTC")
	}
	if opts := p.ptcRequestOptions(); opts == nil {
		t.Fatal("ptcRequestOptions must be non-nil with PTC enabled")
	}
	if p.ServerToolSearchActive() {
		t.Fatal("tool search beta must be off without search tool declaration")
	}
	p.SetServerTools([]ServerToolConfig{{Type: "tool_search_tool_regex"}})
	if !p.ServerToolSearchActive() {
		t.Fatal("tool_search_tool_regex must activate the tool search beta")
	}
	if opts := p.serverToolOpts(); len(opts) == 0 {
		t.Fatal("serverToolOpts must carry the advanced-tool-use beta header")
	}
	// Unknown tool types keep both flags off.
	p.SetServerTools([]ServerToolConfig{{Type: "web_search"}})
	if p.ptcCodeExecutionEnabled() || p.ServerToolSearchActive() {
		t.Fatal("web_search must not enable PTC or tool search beta")
	}
	if opts := p.serverToolOpts(); opts != nil {
		t.Fatal("serverToolOpts must be nil without the tool search beta")
	}
}
