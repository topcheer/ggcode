package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestIssue4535_ToolCallDeltaNoIndex covers tool-call delta slot resolution when
// the streaming relay omits the per-call index field.
//
// #4471 fixed the single-delta path (one tool call per chunk) by resolving
// slots via the delta ID. #4535 extends the same treatment to deltas that
// batch SEVERAL tool calls in one chunk: a delta carrying an ID is locatable
// regardless of how many siblings share the chunk; only a delta with neither
// index nor ID is unattributable and must stay dropped.
func TestIssue4535_ToolCallDeltaNoIndex(t *testing.T) {
	const chunkPrefix = `{"id":"x","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":`
	const chunkSuffix = `,"finish_reason":null}]}`
	const finishChunk = `{"id":"x","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`

	cases := []struct {
		name        string
		deltas      []string
		description string
		wantCalls   []ToolCallDelta
	}{
		{
			name: "single delta without index but with id still lands in its own slot (regression, #4471)",
			deltas: []string{
				`{"role":"assistant","tool_calls":[{"id":"call_a","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]}`,
			},
			description: "single-call delta without index must keep resolving to its own slot",
			wantCalls: []ToolCallDelta{
				{ID: "call_a", Name: "get_weather", Arguments: []byte(`{"city":"SF"}`)},
			},
		},
		{
			name: "multi delta without index but with ids resolves each call (new, #4535)",
			deltas: []string{
				`{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"fn_one","arguments":"{\"a\":"}},{"id":"call_2","type":"function","function":{"name":"fn_two","arguments":"{}"}}]}`,
				`{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"arguments":"1}"}}]}`,
			},
			description: "batched parallel calls without index must not be dropped when each carries an id",
			wantCalls: []ToolCallDelta{
				{ID: "call_1", Name: "fn_one", Arguments: []byte(`{"a":1}`)},
				{ID: "call_2", Name: "fn_two", Arguments: []byte(`{}`)},
			},
		},
		{
			name: "multi delta without index and without id stays dropped",
			deltas: []string{
				`{"role":"assistant","tool_calls":[{"type":"function","function":{"name":"fn_one","arguments":"{}"}},{"type":"function","function":{"name":"fn_two","arguments":"{}"}}]}`,
			},
			description: "a batched call with neither index nor id is unattributable and must be dropped",
			wantCalls:   nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for _, d := range tc.deltas {
					fmt.Fprintf(w, "data: %s%s%s\n\n", chunkPrefix, d, chunkSuffix)
				}
				fmt.Fprintf(w, "data: %s\n\n", finishChunk)
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer server.Close()

			p := NewOpenAIProviderWithBaseURL("test-key", "test-model", 9999, server.URL+"/v1")
			ch, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil)
			if err != nil {
				t.Fatalf("ChatStream returned error: %v", err)
			}

			var got []ToolCallDelta
			var sawDone bool
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
						got = append(got, ev.Tool)
					case StreamEventError:
						t.Fatalf("unexpected stream error: %v", ev.Error)
					case StreamEventDone:
						sawDone = true
					}
				case <-timeout:
					t.Fatalf("%s: stream did not finish within 30s", tc.description)
				}
			}
			if !sawDone {
				t.Fatalf("%s: no Done event", tc.description)
			}
			if len(got) != len(tc.wantCalls) {
				t.Fatalf("%s: got %d tool calls, want %d (got=%+v)", tc.description, len(got), len(tc.wantCalls), got)
			}
			for i, want := range tc.wantCalls {
				if got[i].ID != want.ID {
					t.Errorf("%s: call[%d].ID = %q, want %q", tc.description, i, got[i].ID, want.ID)
				}
				if got[i].Name != want.Name {
					t.Errorf("%s: call[%d].Name = %q, want %q", tc.description, i, got[i].Name, want.Name)
				}
				if string(got[i].Arguments) != string(want.Arguments) {
					t.Errorf("%s: call[%d].Arguments = %q, want %q", tc.description, i, string(got[i].Arguments), string(want.Arguments))
				}
			}
		})
	}
}
