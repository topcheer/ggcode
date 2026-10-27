//go:build goolm

package provider

// #4535: a multi-call tool-call delta (len > 1) whose entries omit index
// must not be unconditionally dropped when they carry an ID — resolve by ID
// exactly like the single-delta path (#4471). Only index-less AND ID-less
// entries stay dropped. Real SSE round trips mirror the #722 probes.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func issue4535Stream(t *testing.T, chunks []string) []ToolCallDelta {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	p := NewOpenAIProviderWithBaseURL("test-key", "test-model", 9999, server.URL+"/v1")
	ch, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil)
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}
	var calls []ToolCallDelta
	timeout := time.After(30 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break loop
			}
			switch ev.Type {
			case StreamEventToolCallDone:
				calls = append(calls, ev.Tool)
			case StreamEventError:
				t.Fatalf("unexpected stream error: %v", ev.Error)
			case StreamEventDone:
				break loop
			}
		case <-timeout:
			t.Fatal("stream did not finish within 30s")
		}
	}
	return calls
}

// Core scenario: one delta carrying two parallel calls; one entry has an
// explicit index, the other omits index but has an ID. Both must survive
// with distinct arguments (old code dropped the ID-bearing no-index entry).
func TestIssue4535_MultiDeltaNoIndexWithIDSurvives(t *testing.T) {
	calls := issue4535Stream(t, []string{
		`{"id":"x","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[` +
			`{"index":0,"id":"call_A","function":{"name":"git_add","arguments":"{\"f\":\"a\"}"}},` +
			`{"id":"call_B","function":{"name":"git_commit","arguments":"{\"m\":\"b\"}"}}` +
			`]},"finish_reason":null}]}`,
		`{"id":"x","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	})
	if len(calls) != 2 {
		t.Fatalf("got %d tool calls, want 2 (no-index entry with ID must be resolved, not dropped)", len(calls))
	}
	byID := map[string]ToolCallDelta{}
	for _, c := range calls {
		byID[c.ID] = c
	}
	a, ok := byID["call_A"]
	if !ok || a.Name != "git_add" || string(a.Arguments) != `{"f":"a"}` {
		t.Fatalf("call_A wrong: %+v", a)
	}
	b, ok := byID["call_B"]
	if !ok || b.Name != "git_commit" || string(b.Arguments) != `{"m":"b"}` {
		t.Fatalf("call_B (no index, has ID) wrong: %+v", b)
	}
}

// Distinct no-index IDs in one delta must open distinct slots, not merge
// (#4471 semantics extended to the multi-delta path).
func TestIssue4535_MultiDeltaDistinctIDsNoMerge(t *testing.T) {
	calls := issue4535Stream(t, []string{
		`{"id":"x","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[` +
			`{"id":"call_A","function":{"name":"fn_a","arguments":"{}"}},` +
			`{"id":"call_B","function":{"name":"fn_b","arguments":"{}"}}` +
			`]},"finish_reason":null}]}`,
		`{"id":"x","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	})
	if len(calls) != 2 {
		t.Fatalf("got %d tool calls, want 2 distinct slots", len(calls))
	}
	names := map[string]bool{}
	for _, c := range calls {
		names[c.Name] = true
	}
	if !names["fn_a"] || !names["fn_b"] {
		t.Fatalf("distinct IDs merged or lost: %+v", calls)
	}
}

// Control: an entry with neither index nor ID in a multi-call delta stays
// dropped (genuinely unlocatable) — the #722 Fold 3 contract.
func TestIssue4535_MultiDeltaNoIndexNoIDStillDropped(t *testing.T) {
	calls := issue4535Stream(t, []string{
		`{"id":"x","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[` +
			`{"index":0,"id":"call_A","function":{"name":"fn_a","arguments":"{}"}},` +
			`{"function":{"name":"lost","arguments":"{}"}}` +
			`]},"finish_reason":null}]}`,
		`{"id":"x","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	})
	if len(calls) != 1 {
		t.Fatalf("got %d tool calls, want 1 (index-less ID-less entry stays dropped)", len(calls))
	}
	if calls[0].ID != "call_A" {
		t.Fatalf("surviving call = %+v, want call_A", calls[0])
	}
}
