package provider

// Characteristic tests for issue #722 (provider batch):
//   Fold 1 (MED): retry × failover budgets multiplied into a ~23min
//     headless hard-block. Fix: cumulative backoff sleep is capped per
//     logical call (retryBudget), and budget-exhausted failures carry a
//     sentinel that FallbackProvider treats as sustained failure —
//     switching immediately instead of demanding the full threshold again.
//   Fold 2 (LOW-MED): non-streaming Chat used resp.Usage verbatim; relays
//     that omit the field produced zero usage (compaction summarizer
//     understated cost/budget). Fix: CountTokens + chars estimate fallback.
//   Fold 3 (LOW): tool-call deltas without index were silently discarded.
//     Fix: a single-call delta defaults to index 0; multi-call deltas
//     without index stay dropped (cannot be located safely).
//   Fold 4 (LOW, theoretical): message_start unconditionally overwrote
//     inputTokens. Fix: non-zero guard, mirroring message_delta.
//
// Probes use real httptest round trips, mirroring zz_issue561/577 probes.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// ---- Fold 1: retry budget caps cumulative backoff sleep ----

func TestIssue722_RetryBudgetSleep(t *testing.T) {
	cases := []struct {
		name       string
		deadlineIn time.Duration // relative deadline for the budget
		delay      time.Duration // requested backoff sleep
		wantErr    error         // nil or errRetryBudgetExhausted
		wantDone   bool
		minElapsed time.Duration
		maxElapsed time.Duration
	}{
		{
			name:       "expired budget returns sentinel without sleeping",
			deadlineIn: -1 * time.Second,
			delay:      5 * time.Second,
			wantErr:    errRetryBudgetExhausted,
			wantDone:   true,
			maxElapsed: 1 * time.Second,
		},
		{
			name:       "sleep longer than remaining is truncated then sentinel",
			deadlineIn: 80 * time.Millisecond,
			delay:      time.Hour,
			wantErr:    errRetryBudgetExhausted,
			wantDone:   true,
			minElapsed: 50 * time.Millisecond,
			maxElapsed: 5 * time.Second,
		},
		{
			name:       "sleep within budget returns nil",
			deadlineIn: 10 * time.Second,
			delay:      10 * time.Millisecond,
			wantErr:    nil,
			wantDone:   false,
			maxElapsed: 2 * time.Second,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := &retryBudget{deadline: time.Now().Add(tc.deadlineIn)}
			start := time.Now()
			err := b.sleep(context.Background(), tc.delay)
			elapsed := time.Since(start)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("sleep err = %v, want %v", err, tc.wantErr)
			}
			if b.done != tc.wantDone {
				t.Fatalf("budget.done = %v, want %v", b.done, tc.wantDone)
			}
			if elapsed < tc.minElapsed {
				t.Fatalf("elapsed %v < min %v (sleep should not be skipped)", elapsed, tc.minElapsed)
			}
			if elapsed > tc.maxElapsed {
				t.Fatalf("elapsed %v > max %v (sleep exceeded the budget cap)", elapsed, tc.maxElapsed)
			}
		})
	}
}

func TestIssue722_RetryWithBackoffCtx_BudgetCap(t *testing.T) {
	// Shrink the per-call budget so the test doesn't sleep for minutes.
	oldBudget := providerRetryTimeBudget
	providerRetryTimeBudget = 50 * time.Millisecond
	t.Cleanup(func() { providerRetryTimeBudget = oldBudget })

	var calls atomic.Int32
	providerErr := errors.New("status code: 503 Service Unavailable") // retryable
	start := time.Now()
	err := retryWithBackoffCtx(context.Background(), func() error {
		calls.Add(1)
		return providerErr
	}, providerRetryAttempts)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error after exhausted budget")
	}
	if !errors.Is(err, errRetryBudgetExhausted) {
		t.Fatalf("error should wrap errRetryBudgetExhausted, got: %v", err)
	}
	if !errors.Is(err, providerErr) {
		t.Fatalf("error should preserve the provider error, got: %v", err)
	}
	if got := calls.Load(); got > 3 {
		t.Fatalf("fn called %d times — budget cap should stop retries early (max 20 attempts)", got)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("retry loop took %v — cumulative backoff budget not enforced", elapsed)
	}
}

func TestIssue722_RetryWithBackoffCtx_UnbudgetedStillRetries(t *testing.T) {
	// Control: with a generous budget the loop keeps its attempt-count
	// semantics (interactive users still see full retry behavior).
	oldBudget := providerRetryTimeBudget
	providerRetryTimeBudget = 1 * time.Hour
	t.Cleanup(func() { providerRetryTimeBudget = oldBudget })
	oldSleep := retrySleep
	retrySleep = func(ctx context.Context, delay time.Duration) error { return nil } // instant sleep
	t.Cleanup(func() { retrySleep = oldSleep })

	var calls atomic.Int32
	err := retryWithBackoffCtx(context.Background(), func() error {
		calls.Add(1)
		if calls.Load() < 3 {
			return errors.New("status code: 502 Bad Gateway") // retryable
		}
		return nil // 3rd attempt succeeds
	}, providerRetryAttempts)
	if err != nil {
		t.Fatalf("expected success on 3rd attempt, got: %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("expected 3 calls, got %d", calls.Load())
	}
}

func TestIssue722_FailoverImmediateOnBudgetExhausted(t *testing.T) {
	// A budget-exhausted failure already gave the primary a best-effort
	// chance; the fallback must take over on the FIRST call instead of the
	// failoverThreshold-th (which headless callers would block ~23min for).
	primaryErr := fmt.Errorf("%w: %w", errRetryBudgetExhausted, errors.New("status code: 503 Service Unavailable"))
	primary := &mockProvider{name: "primary", chatErr: primaryErr}
	fallback := &mockProvider{name: "fallback"}
	fp := NewFallbackProvider(primary, fallback, "primary -> fallback")

	var notifyTrigger FailoverTrigger
	fp.SetFailoverNotify(func(trigger FailoverTrigger, err error) {
		notifyTrigger = trigger
	})

	resp, err := fp.Chat(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got: %v", err)
	}
	if resp == nil {
		t.Fatal("expected fallback response")
	}
	if !fp.HasFailedOver() {
		t.Fatal("budget-exhausted failure should fail over immediately")
	}
	if fallback.chatCalls.Load() != 1 {
		t.Fatalf("fallback called %d times, want 1", fallback.chatCalls.Load())
	}
	if notifyTrigger != FailoverTriggerRepeated {
		t.Fatalf("expected trigger repeated, got %s", notifyTrigger)
	}
}

func TestIssue722_FailoverThresholdUnchangedForPlainTransient(t *testing.T) {
	// Control: a plain transient error (no budget sentinel) must keep the
	// old consecutive-failure threshold semantics.
	primary := &mockProvider{name: "primary", chatErr: errors.New("429 rate limit exceeded")}
	fallback := &mockProvider{name: "fallback"}
	fp := NewFallbackProvider(primary, fallback, "primary -> fallback")

	if _, err := fp.Chat(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error from primary (no failover yet)")
	}
	if fp.HasFailedOver() {
		t.Fatal("plain transient error must NOT fail over below the threshold")
	}
	if fallback.chatCalls.Load() != 0 {
		t.Fatalf("fallback called %d times, want 0", fallback.chatCalls.Load())
	}
}

// ---- Fold 2: non-streaming Chat usage fallback ----

func TestIssue722_ChatUsageFallbackWhenOmitted(t *testing.T) {
	cases := []struct {
		name           string
		body           string
		wantInput      int
		wantOutput     int
		wantFromServer bool // usage taken verbatim from response
	}{
		{
			name: "usage omitted by relay falls back to estimates",
			body: `{"id":"x","object":"chat.completion","created":0,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hello world, this is a summary"},"finish_reason":"stop"}]}`,
			// Fallback: input from CountTokens estimate, output from chars.
			wantInput:  -1, // assert > 0 below
			wantOutput: -1,
		},
		{
			name:           "usage present is used verbatim",
			body:           `{"id":"x","object":"chat.completion","created":0,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`,
			wantInput:      7,
			wantOutput:     3,
			wantFromServer: true,
		},
		{
			name:       "zero-valued usage falls back to estimates",
			body:       `{"id":"x","object":"chat.completion","created":0,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"a longer summary body"},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
			wantInput:  -1,
			wantOutput: -1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			p := NewOpenAIProviderWithBaseURL("test-key", "test-model", 9999, server.URL+"/v1")
			msgs := []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "please summarize this rather long conversation"}}}}
			resp, err := p.Chat(context.Background(), msgs, nil)
			if err != nil {
				t.Fatalf("Chat returned error: %v", err)
			}
			if tc.wantFromServer {
				if resp.Usage.InputTokens != tc.wantInput || resp.Usage.OutputTokens != tc.wantOutput {
					t.Fatalf("usage = %+v, want input=%d output=%d (server values must be kept)", resp.Usage, tc.wantInput, tc.wantOutput)
				}
				return
			}
			// Fallback estimates must be non-zero so cost/budget accounting
			// does not silently undercount.
			if resp.Usage.InputTokens <= 0 {
				t.Fatalf("usage.InputTokens = %d, want > 0 (relay omitted usage; CountTokens estimate missing)", resp.Usage.InputTokens)
			}
			if resp.Usage.OutputTokens <= 0 {
				t.Fatalf("usage.OutputTokens = %d, want > 0 (chars-based estimate missing)", resp.Usage.OutputTokens)
			}
		})
	}
}

// ---- Fold 3: tool-call delta with nil index ----

func TestIssue722_ToolCallDeltaNilIndex(t *testing.T) {
	cases := []struct {
		name         string
		chunks       []string
		wantToolCall bool
		wantToolName string
		description  string
	}{
		{
			name: "single tool call without index defaults to 0",
			chunks: []string{
				`{"id":"x","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]},"finish_reason":null}]}`,
				`{"id":"x","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			},
			wantToolCall: true,
			wantToolName: "get_weather",
			description:  "minimal relay omitting index on a single-call delta must not discard the tool call",
		},
		{
			name: "multiple tool calls without index stay dropped",
			chunks: []string{
				`{"id":"x","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"fn_one","arguments":"{}"}},{"id":"call_2","type":"function","function":{"name":"fn_two","arguments":"{}"}}]},"finish_reason":null}]}`,
				`{"id":"x","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			},
			wantToolCall: false,
			description:  "multi-call deltas without index cannot be located safely and remain dropped",
		},
		{
			name: "indexed tool calls still work",
			chunks: []string{
				`{"id":"x","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_9","type":"function","function":{"name":"indexed_fn","arguments":"{}"}}]},"finish_reason":null}]}`,
				`{"id":"x","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			},
			wantToolCall: true,
			wantToolName: "indexed_fn",
			description:  "regression control: explicit index keeps working",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for _, chunk := range tc.chunks {
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

			var toolCalls []ToolCallDelta
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
						toolCalls = append(toolCalls, ev.Tool)
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
			if tc.wantToolCall {
				if len(toolCalls) != 1 {
					t.Fatalf("%s: got %d tool calls, want 1", tc.description, len(toolCalls))
				}
				if toolCalls[0].Name != tc.wantToolName {
					t.Fatalf("%s: tool name = %q, want %q", tc.description, toolCalls[0].Name, tc.wantToolName)
				}
			} else if len(toolCalls) != 0 {
				t.Fatalf("%s: got %d tool calls, want 0 (unlocatable deltas must be dropped)", tc.description, len(toolCalls))
			}
		})
	}
}

// ---- Fold 4: message_start must not clobber non-zero inputTokens ----

func TestIssue722_AnthropicMessageStartZeroDoesNotClobber(t *testing.T) {
	// Protocol-violating out-of-order stream: a message_delta carrying
	// input_tokens arrives BEFORE message_start, whose usage then reports
	// input_tokens=0. The zero must not erase the value already counted.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{},\"usage\":{\"input_tokens\":99,\"output_tokens\":2}}\n\n")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n")
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	p := NewAnthropicProviderWithBaseURL("test-key", "test-model", 9999, server.URL)
	ch, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil)
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}

	var done *StreamEvent
	timeout := time.After(30 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break loop
			}
			if ev.Type == StreamEventError {
				t.Fatalf("unexpected stream error: %v", ev.Error)
			}
			if ev.Type == StreamEventDone {
				done = &ev
			}
		case <-timeout:
			t.Fatal("stream did not finish within 30s")
		}
	}
	if done == nil {
		t.Fatal("no Done event on stream")
	}
	if done.Usage.InputTokens != 99 {
		t.Fatalf("Usage.InputTokens = %d, want 99 (message_start with input_tokens=0 must not clobber the earlier non-zero delta)", done.Usage.InputTokens)
	}
}
