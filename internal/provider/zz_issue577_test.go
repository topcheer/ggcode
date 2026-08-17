package provider

// Characteristic tests for issue #577 (provider batch):
//   G: adaptive_cap exemption reused IsContextOverflowError's narrow word list
//   B: OpenAI stream `truncated` leaked across retry attempts
//   C: Anthropic stream `truncated` leaked across retry attempts
//   A: FriendlyError bare substring "401" matched "40123 tokens remaining"
//   D: fallback replacement stream sawOutput only counted Text events
//   E: url.Error-wrapped DeadlineExceeded (HTTP client timeout) was FailureNone
//   F: MergeSystemMessages dropped non-text blocks from the first system message
// B/C probes mirror the issue's ver-65 httptest SSE probes: attempt 1 observes
// a truncation finish reason and then dies to a retryable transport error with
// nothing emitted; attempt 2 completes fully. Done must carry Truncated=false.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ---- Bug G: context-overflow exemption must reuse IsContextOverflowError ----

func TestIssue577G_CombinedOverflowNotTreatedAsCapRejection(t *testing.T) {
	errs := []error{
		// Combined input+output messages: contain a max_tokens phrase and a
		// "too large" phrase, so the hasMaxTok/tooLarge gates pass, but the
		// dominant cause is input overflow. ver-65 probe: 3/3 misjudged.
		errors.New("your messages resulted in 40123 tokens; max_tokens 8192 exceeds the limit"),
		errors.New("prompt tokens exceeds maximum allowed tokens: input is too long; reduce max_tokens"),
		errors.New("prompt is too long and max_tokens too large for this request"),
		errors.New("request too large: input tokens exceeded; max_tokens must be smaller"),
	}
	for _, err := range errs {
		if !IsContextOverflowError(err) {
			t.Fatalf("precondition: IsContextOverflowError(%v) = false, want true (authority list)", err)
		}
		rejected, limit := maxTokensRejection(err)
		if rejected {
			t.Errorf("BUG G: maxTokensRejection(%v) = true (parsed=%d), want false — input overflow must not clamp the output cap", err, limit)
		}
	}
	// Control: a genuine output-cap rejection must still be recognized
	// (exemption must not over-suppress).
	rejected, limit := maxTokensRejection(errors.New("max_tokens is too large, must be at most 4096"))
	if !rejected || limit != 4096 {
		t.Errorf("control: maxTokensRejection(genuine) = %v,%d; want true,4096", rejected, limit)
	}
}

func TestIssue577G_CapNotClampedByOverflowMessage(t *testing.T) {
	// The monotonic lo/hi invariant makes a wrong OnRejected permanent:
	// simulating the provider call path, an overflow error must leave the
	// cap untouched instead of ratcheting it down to the input window.
	c := &adaptiveCap{key: "issue577-g", userHint: 8192}
	c.cur.Store(8192)
	err := errors.New("your messages resulted in 40123 tokens; max_tokens 8192 exceeds the limit")
	if rejected, parsed := maxTokensRejection(err); rejected {
		c.OnRejected(parsed)
	}
	if got := c.Get(); got != 8192 {
		t.Errorf("BUG G: cap = %d after overflow error, want 8192 (unchanged)", got)
	}
}

// ---- Bug B: OpenAI stream truncated must reset per attempt ----

// hijackAbortSSE writes the given SSE events inside a chunked response and
// then hard-closes the TCP connection mid-stream, so the client reader sees
// an "unexpected EOF" (retryable, nothing emitted) instead of a clean end.
func hijackAbortSSE(w http.ResponseWriter, events ...string) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		panic("server does not support hijacking")
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	fmt.Fprint(buf, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n")
	for _, ev := range events {
		payload := "data: " + ev + "\n\n"
		fmt.Fprintf(buf, "%x\r\n%s\r\n", len(payload), payload)
	}
	buf.Flush()
	// Close without the terminating 0-length chunk → unexpected EOF client-side.
}

func TestIssue577B_OpenAIStreamTruncatedResetPerAttempt(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			// Attempt 1: finish_reason=length with NO content (emitted stays
			// false), then abrupt close → retryable "unexpected EOF" → retry.
			hijackAbortSSE(w, `{"id":"x","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`)
			return
		}
		// Attempt 2: fully complete, normal end (finish_reason=stop, usage).
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	p := NewOpenAIProviderWithBaseURL("test-key", "test-model", 999, server.URL+"/v1")
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
			if ev.Type == StreamEventDone {
				done = &ev
			}
		case <-timeout:
			t.Fatal("BUG B: stream did not finish within 30s (retry loop hung)")
		}
	}
	if done == nil {
		t.Fatal("BUG B: no Done event on stream")
	}
	if attempts.Load() < 2 {
		t.Fatalf("expected >=2 server attempts, got %d — retry scenario not exercised", attempts.Load())
	}
	if done.Truncated {
		t.Error("BUG B: Done.Truncated = true after a fully-complete attempt 2 — stale flag from truncated attempt 1 leaked across retries, causing needless continue-prompts")
	}
}

// ---- Bug C: Anthropic stream truncated must reset per attempt ----

func TestIssue577C_AnthropicStreamTruncatedResetPerAttempt(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			// Attempt 1: message_start + message_delta(stop_reason=max_tokens)
			// (nothing emitted), then abrupt close → retryable error → retry.
			hijackAbortSSE(w,
				`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}`,
				`{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":3}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n")
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	p := NewAnthropicProviderWithBaseURL("test-key", "test-model", 999, server.URL)
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
			if ev.Type == StreamEventDone {
				done = &ev
			}
		case <-timeout:
			t.Fatal("BUG C: stream did not finish within 30s (retry loop hung)")
		}
	}
	if done == nil {
		t.Fatal("BUG C: no Done event on stream")
	}
	if attempts.Load() < 2 {
		t.Fatalf("expected >=2 server attempts, got %d — retry scenario not exercised", attempts.Load())
	}
	if done.Truncated {
		t.Error("BUG C: Done.Truncated = true after a fully-complete attempt 2 — stale flag from max_tokens attempt 1 leaked across retries")
	}
}

// ---- Bug A: FriendlyError must anchor status codes ----

func TestIssue577A_TokenCountInQuotaMessageNotMisreadAs401(t *testing.T) {
	// Quota message whose token count begins with "401" — the bare substring
	// check misreported this as an auth failure (ver-65 probe).
	got := FriendlyError(errors.New("quota exceeded: 40123 tokens remaining until reset"))
	if strings.Contains(got, "config set api_key") || strings.Contains(got, "(401)") {
		t.Errorf("BUG A: FriendlyError(…40123 tokens remaining…) = %q — misread digit coincidence as 401 auth failure", got)
	}
	// Control: genuinely anchored 401 still yields auth advice.
	got = FriendlyError(errors.New("status code: 401, message: invalid api key"))
	if !strings.Contains(got, "(401)") {
		t.Errorf("control: anchored 401 no longer recognized: %q", got)
	}
}

// ---- Bug D: replacement stream sawOutput counts all content events ----

// scriptedProvider serves scripted event slices per ChatStream call.
type scriptedProvider struct {
	name    string
	streams [][]StreamEvent
	calls   atomic.Int32
}

func (s *scriptedProvider) Name() string { return s.name }
func (s *scriptedProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	return &ChatResponse{Message: Message{Role: "assistant"}}, nil
}
func (s *scriptedProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return len(messages) * 10, nil
}
func (s *scriptedProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	n := int(s.calls.Add(1)) - 1
	script := s.streams[0]
	if n < len(s.streams) {
		script = s.streams[n]
	}
	ch := make(chan StreamEvent, len(script)+1)
	go func() {
		for _, ev := range script {
			ch <- ev
		}
		close(ch)
	}()
	return ch, nil
}

func TestIssue577D_ToolOnlyReplacementStreamClearsFailureCount(t *testing.T) {
	primary := &scriptedProvider{name: "primary", streams: [][]StreamEvent{
		{{Type: StreamEventError, Error: errors.New("insufficient_quota: quota exceeded")}},
	}}
	// Fallback delivers a PURE tool-call response: no text at all.
	fallback := &scriptedProvider{name: "fallback", streams: [][]StreamEvent{
		{
			{Type: StreamEventToolCallDone, Tool: ToolCallDelta{Index: 0, ID: "call_1", Name: "grep"}},
			{Type: StreamEventDone, Usage: &TokenUsage{InputTokens: 5, OutputTokens: 5}},
		},
	}}
	fp := NewFallbackProvider(primary, fallback, "primary -> fallback")

	// Pre-seed two stale transient failures (#376/#577-D scenario): without
	// the fix the tool-only success never cleared them.
	fp.consecutiveFail.Store(2)

	ch, err := fp.ChatStream(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}
	timeout := time.After(10 * time.Second)
	for draining := true; draining; {
		select {
		case _, ok := <-ch:
			draining = ok
		case <-timeout:
			t.Fatal("BUG D: stream did not finish within 10s")
		}
	}
	if !fp.HasFailedOver() {
		t.Fatal("expected failover to have activated on quota error")
	}
	if got := fp.consecutiveFail.Load(); got != 0 {
		t.Errorf("BUG D: consecutiveFail = %d after a successful tool-only fallback stream, want 0 — text-only sawOutput left the stale count in place (premature failover risk)", got)
	}
}

// ---- Bug E: HTTP client timeouts must count toward failover ----

func TestIssue577E_HTTPTimeoutClassifiedAsNetworkFailure(t *testing.T) {
	// net/http always wraps client timeouts in *url.Error; errors.Is still
	// resolves DeadlineExceeded. This form is an endpoint-health signal.
	httpTimeout := &url.Error{
		Op:  "Post",
		URL: "https://api.example.com/v1/chat/completions",
		Err: context.DeadlineExceeded,
	}
	if got := ClassifyLLMError(httpTimeout); got != FailureNetwork {
		t.Errorf("BUG E: ClassifyLLMError(url.Error→DeadlineExceeded) = %v, want FailureNetwork — pure-timeout endpoints never reached FailoverTriggerRepeated", got)
	}
	// #528 semantics preserved: the bare / agent-side wrapped sentinel (no
	// url.Error in the chain) still says nothing about provider health.
	if got := ClassifyLLMError(context.DeadlineExceeded); got != FailureNone {
		t.Errorf("ClassifyLLMError(bare DeadlineExceeded) = %v, want FailureNone (#528)", got)
	}
	wrapped := fmt.Errorf("stream request failed: %w", context.DeadlineExceeded)
	if got := ClassifyLLMError(wrapped); got != FailureNone {
		t.Errorf("ClassifyLLMError(fmt-wrapped DeadlineExceeded) = %v, want FailureNone (#528)", got)
	}
}

func TestIssue577E_RepeatedHTTPTimeoutsTriggerFailover(t *testing.T) {
	httpTimeout := &url.Error{Op: "Post", URL: "https://api.example.com/v1", Err: context.DeadlineExceeded}
	primary := &mockProvider{name: "primary", chatErr: httpTimeout}
	fallback := &mockProvider{name: "fallback"}
	fp := NewFallbackProvider(primary, fallback, "primary -> fallback")

	var notifyTrigger FailoverTrigger
	fp.SetFailoverNotify(func(trigger FailoverTrigger, err error) { notifyTrigger = trigger })

	// failoverThreshold == 3: three consecutive client timeouts must trip the
	// repeated-failures failover (ver-65 probe: 10 timeouts, 0 accumulated).
	// Note the call that trips the threshold internally retries on the
	// fallback and may return its success — so check state before the error.
	for i := 0; i < failoverThreshold && !fp.HasFailedOver(); i++ {
		_, _ = fp.Chat(context.Background(), nil, nil)
	}
	if !fp.HasFailedOver() {
		t.Fatal("BUG E: repeated HTTP client timeouts never activated failover")
	}
	if notifyTrigger != FailoverTriggerRepeated {
		t.Errorf("trigger = %s, want %s", notifyTrigger, FailoverTriggerRepeated)
	}

	// Control: agent-side (bare sentinel) deadlines must NOT trigger it.
	primary2 := &mockProvider{name: "primary2", chatErr: fmt.Errorf("turn deadline: %w", context.DeadlineExceeded)}
	fp2 := NewFallbackProvider(primary2, &mockProvider{name: "fallback2"}, "p2 -> f2")
	for i := 0; i < failoverThreshold+2; i++ {
		_, _ = fp2.Chat(context.Background(), nil, nil)
	}
	if fp2.HasFailedOver() {
		t.Error("BUG E(control): agent-side DeadlineExceeded triggered failover — #528 exemption regressed")
	}
}

// ---- Bug F: non-text blocks from the first system message must survive ----

func TestIssue577F_MergeSystemPreservesNonTextBlocks(t *testing.T) {
	img := ContentBlock{Type: "image", ImageMIME: "image/png", ImageData: "aGk="}
	msgs := []Message{
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: "sys1"}, img}},
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: "sys2"}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}},
	}
	result := MergeSystemMessages(msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages (merged system + user), got %d", len(result))
	}
	blocks := result[0].Content
	if len(blocks) != 3 {
		t.Fatalf("BUG F: expected 3 blocks (sys1, image, sys2), got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].Text != "sys1" || blocks[1].Type != "image" || blocks[1].ImageData != "aGk=" || blocks[2].Text != "sys2" {
		t.Errorf("BUG F: unexpected merged blocks: %+v — image block from the first system message was dropped", blocks)
	}
}

func TestIssue577F_ImageOnlyFirstSystemStillMerges(t *testing.T) {
	img := ContentBlock{Type: "image", ImageMIME: "image/png", ImageData: "aGk="}
	msgs := []Message{
		{Role: "system", Content: []ContentBlock{img}},
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: "later"}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}},
	}
	result := MergeSystemMessages(msgs)
	if len(result) != 2 {
		t.Fatalf("expected merged output (2 messages), got %d — image-only first system fell back to unmerged input", len(result))
	}
	blocks := result[0].Content
	if len(blocks) != 2 || blocks[0].Type != "image" || blocks[1].Text != "later" {
		t.Errorf("BUG F: unexpected merged blocks: %+v", blocks)
	}
}
