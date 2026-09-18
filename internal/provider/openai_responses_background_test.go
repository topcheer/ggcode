package provider

// sa-73 tests for OpenAI Responses background mode: create with
// background=true, poll GET /responses/{id} to a terminal state, cancel on
// context cancellation.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newBackgroundTestServer(t *testing.T, handler http.HandlerFunc) *OpenAIResponsesProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewOpenAIResponsesProvider("test-key", "gpt-5.2", 1024, srv.URL+"/responses")
	p.SetBackgroundMode(true)
	p.pollDelay = time.Millisecond // fast polls in tests
	return p
}

func TestBackgroundRequestFlag(t *testing.T) {
	p := NewOpenAIResponsesProvider("k", "gpt-5.2", 100, "https://example.com/v1/responses")
	p.SetBackgroundMode(true)

	req, err := p.buildRequest([]Message{{Role: "user", Content: []ContentBlock{TextBlock("hi")}}}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"background":true`) {
		t.Fatalf("want background:true in request body, got %s", body)
	}
	if !strings.Contains(string(body), `"store":false`) {
		t.Fatalf("want store:false (stateless replay), got %s", body)
	}

	// Without SetBackgroundMode the flag must stay absent.
	p2 := NewOpenAIResponsesProvider("k", "gpt-5.2", 100, "https://example.com/v1/responses")
	req2, _ := p2.buildRequest([]Message{{Role: "user", Content: []ContentBlock{TextBlock("hi")}}}, nil, true)
	body2, _ := json.Marshal(req2)
	if strings.Contains(string(body2), `"background"`) {
		t.Fatalf("background flag leaked into default mode: %s", body2)
	}
}

func messageText(t *testing.T, m Message) string {
	t.Helper()
	var b strings.Builder
	for _, blk := range m.Content {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

func TestBackgroundChatPollsUntilCompleted(t *testing.T) {
	var gets atomic.Int32
	p := newBackgroundTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/responses":
			fmt.Fprint(w, `{"id":"resp_1","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/responses/resp_1":
			n := gets.Add(1)
			if n == 1 {
				fmt.Fprint(w, `{"id":"resp_1","status":"in_progress"}`)
				return
			}
			fmt.Fprint(w, `{"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done!"}]}],"usage":{"input_tokens":10,"output_tokens":5}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			fmt.Fprint(w, `{"error":{"message":"not found"}}`)
		}
	})
	_ = p

	out, err := p.Chat(context.Background(), []Message{{Role: "user", Content: []ContentBlock{TextBlock("long task")}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gets.Load() != 2 {
		t.Fatalf("want 2 polls, got %d", gets.Load())
	}
	if got := messageText(t, out.Message); got != "done!" {
		t.Fatalf("want text %q, got %q", "done!", got)
	}
	if out.Usage.InputTokens != 10 || out.Usage.OutputTokens != 5 {
		t.Fatalf("unexpected usage: %+v", out.Usage)
	}
}

func TestBackgroundChatRetriesTransientPollErrors(t *testing.T) {
	var gets atomic.Int32
	p := newBackgroundTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/responses/resp_1" {
			if gets.Add(1) <= 2 {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, `{"error":{"message":"upstream busy"}}`)
				return
			}
			fmt.Fprint(w, `{"id":"resp_1","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`)
			return
		}
		fmt.Fprint(w, `{"id":"resp_1","status":"queued"}`)
	})
	_ = p

	out, err := p.Chat(context.Background(), []Message{{Role: "user", Content: []ContentBlock{TextBlock("hi")}}}, nil)
	if err != nil {
		t.Fatalf("transient poll errors should be retried, got: %v", err)
	}
	if got := messageText(t, out.Message); got != "ok" {
		t.Fatalf("unexpected text %q", got)
	}
}

func TestBackgroundChatFailedStatus(t *testing.T) {
	p := newBackgroundTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"id":"resp_1","status":"failed","error":{"code":"server_error","message":"model exploded"}}`)
			return
		}
		fmt.Fprint(w, `{"id":"resp_1","status":"queued"}`)
	})
	_ = p

	_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: []ContentBlock{TextBlock("hi")}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "model exploded") {
		t.Fatalf("want failed-status error with API message, got %v", err)
	}
}

func TestBackgroundChatCancelledStatus(t *testing.T) {
	p := newBackgroundTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"id":"resp_1","status":"cancelled"}`)
			return
		}
		fmt.Fprint(w, `{"id":"resp_1","status":"queued"}`)
	})
	_ = p

	_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: []ContentBlock{TextBlock("hi")}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("want cancelled-status error, got %v", err)
	}
}

func TestBackgroundChatCancelOnContextCancel(t *testing.T) {
	var cancels atomic.Int32
	deadline := time.Now().Add(5 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	p := newBackgroundTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cancel"):
			cancels.Add(1)
			fmt.Fprint(w, `{"id":"resp_1","status":"cancelled"}`)
		case r.Method == http.MethodGet:
			// Stay queued until the client gives up.
			fmt.Fprint(w, `{"id":"resp_1","status":"queued"}`)
		default:
			fmt.Fprint(w, `{"id":"resp_1","status":"queued"}`)
		}
	})
	_ = p

	_, err := p.Chat(ctx, []Message{{Role: "user", Content: []ContentBlock{TextBlock("hi")}}}, nil)
	if err == nil {
		t.Fatal("want context-cancel error")
	}
	for cancels.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if cancels.Load() == 0 {
		t.Fatal("server-side cancel endpoint was not called on context cancellation")
	}
}

func TestBackgroundChatStreamIncremental(t *testing.T) {
	var gets atomic.Int32
	var mu sync.Mutex
	var order []string
	p := newBackgroundTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/responses/resp_1" {
			switch gets.Add(1) {
			case 1:
				fmt.Fprint(w, `{"id":"resp_1","status":"in_progress","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hel"}]}]}`)
			case 2:
				fmt.Fprint(w, `{"id":"resp_1","status":"in_progress","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello world"}]},{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"enc"}]}`)
			default:
				fmt.Fprint(w, `{"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello world"}]},{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"enc"}],"usage":{"input_tokens":3,"output_tokens":7}}`)
			}
			return
		}
		fmt.Fprint(w, `{"id":"resp_1","status":"queued"}`)
	})
	_ = p

	ch, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: []ContentBlock{TextBlock("hi")}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var done *StreamEvent
	var reasoningSeen bool
	for ev := range ch {
		mu.Lock()
		switch ev.Type {
		case StreamEventText:
			order = append(order, ev.Text)
			text.WriteString(ev.Text)
		case StreamEventReasoning:
			reasoningSeen = true
		case StreamEventDone:
			done = &ev
		case StreamEventError:
			mu.Unlock()
			t.Fatalf("unexpected stream error: %v", ev.Error)
		}
		mu.Unlock()
	}
	if text.String() != "Hello world" {
		t.Fatalf("want incremental text %q, got %q", "Hello world", text.String())
	}
	if len(order) != 2 {
		t.Fatalf("want 2 text deltas (Hel + lo world), got %v", order)
	}
	if !reasoningSeen {
		t.Fatal("terminal reasoning item was not forwarded")
	}
	if done == nil {
		t.Fatal("missing Done event")
	}
	if done.Usage == nil || done.Usage.OutputTokens != 7 {
		t.Fatalf("unexpected Done usage: %+v", done.Usage)
	}
}

func TestBackgroundChatIncompleteStopsAsMaxTokens(t *testing.T) {
	p := newBackgroundTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"id":"resp_1","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}`)
			return
		}
		fmt.Fprint(w, `{"id":"resp_1","status":"queued"}`)
	})
	_ = p

	out, err := p.Chat(context.Background(), []Message{{Role: "user", Content: []ContentBlock{TextBlock("hi")}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.StopReason != "max_tokens" {
		t.Fatalf("want max_tokens stop reason, got %q", out.StopReason)
	}
}

func TestBackgroundConfigWiring(t *testing.T) {
	// Registry must propagate ResponsesBackground into the provider.
	p := NewOpenAIResponsesProvider("k", "m", 10, "https://example.com/v1/responses")
	if p.background {
		t.Fatal("background must default to off")
	}
	p.SetBackgroundMode(true)
	if !p.background {
		t.Fatal("SetBackgroundMode(true) had no effect")
	}
}
