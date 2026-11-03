package provider

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

func TestIssue577G_CombinedOverflowNotTreatedAsCapRejection(t *testing.T) {
	errs := []error{

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

	rejected, limit := maxTokensRejection(errors.New("max_tokens is too large, must be at most 4096"))
	if !rejected || limit != 4096 {
		t.Errorf("control: maxTokensRejection(genuine) = %v,%d; want true,4096", rejected, limit)
	}
}

func TestIssue577G_CapNotClampedByOverflowMessage(t *testing.T) {

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

}

func TestIssue577B_OpenAIStreamTruncatedResetPerAttempt(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {

			hijackAbortSSE(w, `{"id":"x","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`)
			return
		}

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

func TestIssue577C_AnthropicStreamTruncatedResetPerAttempt(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {

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

func TestIssue577A_TokenCountInQuotaMessageNotMisreadAs401(t *testing.T) {

	got := FriendlyError(errors.New("quota exceeded: 40123 tokens remaining until reset"))
	if strings.Contains(got, "config set api_key") || strings.Contains(got, "(401)") {
		t.Errorf("BUG A: FriendlyError(…40123 tokens remaining…) = %q — misread digit coincidence as 401 auth failure", got)
	}

	got = FriendlyError(errors.New("status code: 401, message: invalid api key"))
	if !strings.Contains(got, "(401)") {
		t.Errorf("control: anchored 401 no longer recognized: %q", got)
	}
}

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

	fallback := &scriptedProvider{name: "fallback", streams: [][]StreamEvent{
		{
			{Type: StreamEventToolCallDone, Tool: ToolCallDelta{Index: 0, ID: "call_1", Name: "grep"}},
			{Type: StreamEventDone, Usage: &TokenUsage{InputTokens: 5, OutputTokens: 5}},
		},
	}}
	fp := NewFallbackProvider(primary, fallback, "primary -> fallback")

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

func TestIssue577E_HTTPTimeoutClassifiedAsNetworkFailure(t *testing.T) {

	httpTimeout := &url.Error{
		Op:  "Post",
		URL: "https://api.example.com/v1/chat/completions",
		Err: context.DeadlineExceeded,
	}
	if got := ClassifyLLMError(httpTimeout); got != FailureNetwork {
		t.Errorf("BUG E: ClassifyLLMError(url.Error→DeadlineExceeded) = %v, want FailureNetwork — pure-timeout endpoints never reached FailoverTriggerRepeated", got)
	}

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

	for i := 0; i < failoverThreshold && !fp.HasFailedOver(); i++ {
		_, _ = fp.Chat(context.Background(), nil, nil)
	}
	if !fp.HasFailedOver() {
		t.Fatal("BUG E: repeated HTTP client timeouts never activated failover")
	}
	if notifyTrigger != FailoverTriggerRepeated {
		t.Errorf("trigger = %s, want %s", notifyTrigger, FailoverTriggerRepeated)
	}

	primary2 := &mockProvider{name: "primary2", chatErr: fmt.Errorf("turn deadline: %w", context.DeadlineExceeded)}
	fp2 := NewFallbackProvider(primary2, &mockProvider{name: "fallback2"}, "p2 -> f2")
	for i := 0; i < failoverThreshold+2; i++ {
		_, _ = fp2.Chat(context.Background(), nil, nil)
	}
	if fp2.HasFailedOver() {
		t.Error("BUG E(control): agent-side DeadlineExceeded triggered failover — #528 exemption regressed")
	}
}

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
