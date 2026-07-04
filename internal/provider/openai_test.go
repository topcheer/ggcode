package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/sashabaranov/go-openai"
)

func TestOpenAIConvertMessages_SystemText(t *testing.T) {
	p := &OpenAIProvider{}
	msgs := []Message{
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: "Be helpful"}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "Hello"}}},
	}
	result := p.convertMessages(msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	if result[0].Role != "system" {
		t.Errorf("expected system role, got %s", result[0].Role)
	}
	if result[0].Content != "Be helpful" {
		t.Errorf("expected 'Be helpful', got %s", result[0].Content)
	}
}

func TestOpenAIConvertMessages_ToolResult(t *testing.T) {
	p := &OpenAIProvider{}
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolID: "call_123", Output: "file contents here"},
		}},
	}
	result := p.convertMessages(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0].Role != "tool" {
		t.Errorf("expected tool role, got %s", result[0].Role)
	}
	if result[0].ToolCallID != "call_123" {
		t.Errorf("expected ToolCallID 'call_123', got %s", result[0].ToolCallID)
	}
}

func TestOpenAIConvertMessages_Empty(t *testing.T) {
	p := &OpenAIProvider{}
	result := p.convertMessages(nil)
	if len(result) != 0 {
		t.Errorf("expected 0 messages, got %d", len(result))
	}
}

func TestOpenAIConvertMessages_NormalizesInvalidToolUseInput(t *testing.T) {
	p := &OpenAIProvider{}
	msgs := []Message{
		{Role: "assistant", Content: []ContentBlock{
			ToolUseBlock("call_123", "edit_file", json.RawMessage(`{"path":"README.md"`)),
		}},
	}
	result := p.convertMessages(msgs)
	if len(result) != 1 || len(result[0].ToolCalls) != 1 {
		t.Fatalf("expected one assistant message with one tool call, got %#v", result)
	}
	args := result[0].ToolCalls[0].Function.Arguments
	if !json.Valid([]byte(args)) {
		t.Fatalf("expected normalized OpenAI tool arguments to be valid JSON, got %q", args)
	}
	if !strings.Contains(args, "_ggcode_raw_input") {
		t.Fatalf("expected fallback marker in normalized OpenAI tool arguments, got %q", args)
	}
}

func TestEstimateTokensFromChars(t *testing.T) {
	if got := estimateTokensFromChars(0); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
	if got := estimateTokensFromChars(3); got != 1 {
		t.Fatalf("expected minimum 1 token for non-empty output, got %d", got)
	}
	if got := estimateTokensFromChars(40); got != 13 { // 40/3 ≈ 13
		t.Fatalf("expected 13, got %d", got)
	}
}

func TestFinishReasonError(t *testing.T) {
	tests := []struct {
		name         string
		finishReason string
		wantErr      string
	}{
		{name: "stop is normal", finishReason: "stop"},
		{name: "tool calls is normal", finishReason: "tool_calls"},
		{name: "function call is normal", finishReason: "function_call"},
		{name: "context overflow", finishReason: "model_context_window_exceeded", wantErr: "prompt too long: model context window exceeded"},
		{name: "context overflow alias", finishReason: "context_window_exceeded", wantErr: "prompt too long: model context window exceeded"},
		{name: "length surfaces error", finishReason: "length", wantErr: "finish_reason=length"},
		{name: "sensitive surfaces error", finishReason: "sensitive", wantErr: "finish_reason=sensitive"},
		{name: "network error surfaces error", finishReason: "network_error", wantErr: "finish_reason=network_error"},
		{name: "content filter surfaces error", finishReason: "content_filter", wantErr: "finish_reason=content_filter"},
		{name: "unknown reason surfaces error", finishReason: "weird_reason", wantErr: "finish_reason=weird_reason"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := finishReasonError(tc.finishReason)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected nil error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestAnthropicBuildParams_Basic(t *testing.T) {
	p := &AnthropicProvider{model: "claude-3", maxTokens: 1024}
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "Hello"}}},
	}
	params := p.buildParams(msgs, nil)
	if params.Model != "claude-3" {
		t.Errorf("expected model 'claude-3', got %s", params.Model)
	}
	if params.MaxTokens != 1024 {
		t.Errorf("expected MaxTokens 1024, got %d", params.MaxTokens)
	}
	if len(params.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(params.Messages))
	}
}

func TestAnthropicBuildParams_SystemInUser(t *testing.T) {
	p := &AnthropicProvider{model: "claude-3", maxTokens: 1024}
	msgs := []Message{
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: "Be helpful"}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "Hello"}}},
	}
	params := p.buildParams(msgs, nil)
	// System should be embedded into first user message, not separate
	if len(params.Messages) != 1 {
		t.Fatalf("expected 1 message (system merged into user), got %d", len(params.Messages))
	}
}

func TestGeminiConvertMessages_NormalizesInvalidToolUseInput(t *testing.T) {
	p := &GeminiProvider{}
	contents, _ := p.convertMessages([]Message{
		{Role: "assistant", Content: []ContentBlock{
			ToolUseBlock("call_123", "edit_file", json.RawMessage(`{"path":"README.md"`)),
		}},
	})
	if len(contents) != 1 || len(contents[0].Parts) != 1 || contents[0].Parts[0].FunctionCall == nil {
		t.Fatalf("expected one Gemini function call part, got %#v", contents)
	}
	if got := contents[0].Parts[0].FunctionCall.Args["_ggcode_raw_input"]; got == nil {
		t.Fatalf("expected fallback marker in Gemini function args, got %#v", contents[0].Parts[0].FunctionCall.Args)
	}
}

func TestConvertAnthropicResponse(t *testing.T) {
	// Test with empty response
	result := convertAnthropicResponse(nil)
	if len(result) != 0 {
		t.Errorf("expected empty, got %d blocks", len(result))
	}
}

func TestIsRetryableRecognizesProviderErrors(t *testing.T) {
	if !isRetryable(&openai.APIError{HTTPStatusCode: http.StatusTooManyRequests, Message: "rate limited"}) {
		t.Fatal("expected openai 429 to be retryable")
	}
	if !isRetryable(&anthropic.Error{StatusCode: http.StatusBadGateway}) {
		t.Fatal("expected anthropic 502 to be retryable")
	}
	// 400 is retryable under the aggressive retry policy (only 401/403/404 are permanent)
	if !isRetryable(&openai.APIError{HTTPStatusCode: http.StatusBadRequest, Message: "bad request"}) {
		t.Fatal("expected openai 400 to be retryable")
	}
	// 401/403/404 are NOT retryable
	if isRetryable(&openai.APIError{HTTPStatusCode: http.StatusUnauthorized, Message: "unauthorized"}) {
		t.Fatal("expected openai 401 not to be retryable")
	}
	if isRetryable(&openai.APIError{HTTPStatusCode: http.StatusForbidden, Message: "forbidden"}) {
		t.Fatal("expected openai 403 not to be retryable")
	}
	if isRetryable(&openai.APIError{HTTPStatusCode: http.StatusNotFound, Message: "not found"}) {
		t.Fatal("expected openai 404 not to be retryable")
	}
	if !isRetryable(context.DeadlineExceeded) {
		t.Fatal("expected deadline exceeded to be retryable when caller context is still active")
	}
	if isRetryable(context.Canceled) {
		t.Fatal("expected context cancellation not to be retryable")
	}
}

func TestRetryAfterDelayFromAnthropicHeader(t *testing.T) {
	err := &anthropic.Error{
		StatusCode: http.StatusTooManyRequests,
		Response: &http.Response{
			Header: http.Header{
				"Retry-After": []string{"3"},
			},
		},
	}
	delay, ok := retryAfterDelay(err)
	if !ok {
		t.Fatal("expected retry-after delay to be detected")
	}
	if delay != 3*time.Second {
		t.Fatalf("expected 3s retry delay, got %v", delay)
	}
}

func TestRetryWithBackoffCtxHonorsRetryAfter(t *testing.T) {
	originalSleep := retrySleep
	defer func() { retrySleep = originalSleep }()

	var slept []time.Duration
	retrySleep = func(ctx context.Context, delay time.Duration) error {
		slept = append(slept, delay)
		return nil
	}

	attempts := 0
	err := retryWithBackoffCtx(context.Background(), func() error {
		attempts++
		if attempts < 3 {
			return &anthropic.Error{
				StatusCode: http.StatusTooManyRequests,
				Response: &http.Response{
					Header: http.Header{
						"Retry-After": []string{"2"},
					},
				},
			}
		}
		return nil
	}, providerRetryAttempts)
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	if len(slept) != 2 || slept[0] != 2*time.Second || slept[1] != 2*time.Second {
		t.Fatalf("expected retry-after sleeps [2s 2s], got %+v", slept)
	}
}

func TestOpenAIReasoningEffortRetriesWithoutUnsupportedParam(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unknown parameter: reasoning_effort","type":"invalid_request_error","param":"reasoning_effort","code":"unknown_parameter"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","object":"chat.completion","created":0,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	p := NewOpenAIProviderWithBaseURL("test-key", "test-model", 1024, server.URL+"/v1")
	p.SetReasoningEffort("high")
	resp, err := p.Chat(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hello"}}}}, nil)
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp == nil || len(resp.Message.Content) != 1 || resp.Message.Content[0].Text != "ok" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if len(bodies) != 2 {
		t.Fatalf("expected fallback retry, got %d requests", len(bodies))
	}
	if bodies[0]["reasoning_effort"] != "high" {
		t.Fatalf("expected first request reasoning_effort=high, got %#v", bodies[0]["reasoning_effort"])
	}
	if _, ok := bodies[1]["reasoning_effort"]; ok {
		t.Fatalf("expected fallback request to omit reasoning_effort, got %#v", bodies[1]["reasoning_effort"])
	}
	if got := p.ReasoningEffort(); got != "" {
		t.Fatalf("expected unsupported reasoning_effort to switch to auto, got %q", got)
	}
}

func TestRetryWithBackoffCtxRetriesDeadlineExceededWhenContextActive(t *testing.T) {
	originalSleep := retrySleep
	defer func() { retrySleep = originalSleep }()

	var slept []time.Duration
	retrySleep = func(ctx context.Context, delay time.Duration) error {
		slept = append(slept, delay)
		return nil
	}

	attempts := 0
	err := retryWithBackoffCtx(context.Background(), func() error {
		attempts++
		if attempts == 1 {
			return context.DeadlineExceeded
		}
		return nil
	}, providerRetryAttempts)
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
	if len(slept) != 1 || slept[0] < 750*time.Millisecond || slept[0] > 1250*time.Millisecond {
		t.Fatalf("expected one ~1s backoff (with jitter), got %+v", slept)
	}
}

func TestRetryWithBackoffCtxDoesNotRetryExpiredCallerContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	attempts := 0
	err := retryWithBackoffCtx(ctx, func() error {
		attempts++
		return context.DeadlineExceeded
	}, providerRetryAttempts)
	if err == nil {
		t.Fatal("expected deadline error")
	}
	if attempts != 1 {
		t.Fatalf("expected no retry after caller context ends, got %d attempts", attempts)
	}
}

func TestHeaderInjectingTransportConcurrentUpdate(t *testing.T) {
	// Regression test: UpdateHeaders and RoundTrip must be safe for concurrent use.
	base := http.DefaultTransport
	tr := &headerInjectingTransport{
		base:    base,
		headers: http.Header{"X-Test": []string{"v1"}},
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writer goroutine: continuously update headers.
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
				h := make(http.Header)
				h.Set("X-Test", fmt.Sprintf("v%d", i))
				tr.UpdateHeaders(h)
				i++
			}
		}
	}()

	// Reader goroutines: continuously read headers via RoundTrip.
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					req, _ := http.NewRequestWithContext(context.Background(), "GET", "http://127.0.0.1:1", nil)
					// RoundTrip will fail to connect but that's fine — we just
					// need to exercise the header-reading path.
					_, _ = tr.RoundTrip(req)
				}
			}
		}()
	}

	// Let the goroutines hammer it for 100ms.
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}

func TestEstimateTokensForMessages(t *testing.T) {
	tests := []struct {
		name  string
		msgs  []Message
		check func(t *testing.T, got int)
	}{
		{
			name: "empty messages",
			msgs: []Message{},
			check: func(t *testing.T, got int) {
				if got != 0 {
					t.Fatalf("expected 0, got %d", got)
				}
			},
		},
		{
			name: "text only",
			msgs: []Message{
				{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hello world"}}},
			},
			check: func(t *testing.T, got int) {
				if got == 0 {
					t.Fatal("expected non-zero token count for text")
				}
				// "hello world" = 11 chars / 3 = 3.67 → 3 tokens
				if got != 3 {
					t.Fatalf("expected 3, got %d", got)
				}
			},
		},
		{
			name: "tool_result output is counted",
			msgs: []Message{
				{Role: "user", Content: []ContentBlock{
					{Type: "text", Text: "run this"},
				}},
				{Role: "assistant", Content: []ContentBlock{
					{Type: "tool_use", ToolName: "run_command", ToolID: "c1"},
				}},
				{Role: "user", Content: []ContentBlock{
					{Type: "tool_result", ToolID: "c1", Output: strings.Repeat("x", 300)},
				}},
			},
			check: func(t *testing.T, got int) {
				// "run this" = 8 chars, output = 300 chars, total = 308 / 3 = 102
				if got < 100 {
					t.Fatalf("expected ~102 tokens (output counted), got %d", got)
				}
			},
		},
		{
			name: "text + output + input all counted",
			msgs: []Message{
				{Role: "user", Content: []ContentBlock{
					{Type: "text", Text: "hello"},                                    // 5 chars
					{Type: "tool_result", Output: "world"},                           // 5 chars
					{Type: "image", Input: json.RawMessage(strings.Repeat("a", 30))}, // 30 chars
				}},
			},
			check: func(t *testing.T, got int) {
				// total = 5 + 5 + 30 = 40 chars / 3 = 13 tokens
				if got != 13 {
					t.Fatalf("expected 13, got %d", got)
				}
			},
		},
		{
			name: "large output dominates token count",
			msgs: []Message{
				{Role: "user", Content: []ContentBlock{
					{Type: "text", Text: "list files"},
				}},
				{Role: "user", Content: []ContentBlock{
					{Type: "tool_result", ToolID: "c1", Output: strings.Repeat("file.txt\n", 1000)}, // 9000 chars
				}},
			},
			check: func(t *testing.T, got int) {
				// "list files" = 10 + 9000 = 9010 / 3 = 3003
				if got < 2900 {
					t.Fatalf("expected ~3003 tokens (output dominates), got %d", got)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := estimateTokensForMessages(tc.msgs)
			tc.check(t, got)
		})
	}
}

func TestCountTokens_AllProvidersCountOutput(t *testing.T) {
	// Verify Anthropic and Gemini now count tool_result Output (not just Text).
	messages := []Message{
		{Role: "user", Content: []ContentBlock{
			{Type: "text", Text: "short"},
		}},
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolID: "c1", Output: strings.Repeat("x", 9000)},
		}},
	}

	// All providers should return roughly the same count now.
	providers := []struct {
		name string
		prov Provider
	}{
		{"openai", &OpenAIProvider{}},
		{"anthropic", &AnthropicProvider{}},
		{"gemini", &GeminiProvider{}},
	}

	counts := make(map[string]int)
	for _, p := range providers {
		count, err := p.prov.CountTokens(context.Background(), messages)
		if err != nil {
			t.Fatalf("%s CountTokens failed: %v", p.name, err)
		}
		counts[p.name] = count
		t.Logf("%s: %d tokens", p.name, count)
	}

	// All should be > 2900 (the output dominates at 9000 chars / 3 = 3000).
	for name, count := range counts {
		if count < 2900 {
			t.Errorf("%s: expected >= 2900 tokens (output should be counted), got %d", name, count)
		}
	}

	// All should agree (they all use the same estimateTokensForMessages now).
	openaiCount := counts["openai"]
	for name, count := range counts {
		if count != openaiCount {
			t.Errorf("%s count %d differs from openai %d", name, count, openaiCount)
		}
	}
}

func TestOpenAIUsageIncludesCachedTokens(t *testing.T) {
	usage := openAIUsage(openai.Usage{
		PromptTokens:     1200,
		CompletionTokens: 300,
		PromptTokensDetails: &openai.PromptTokensDetails{
			CachedTokens: 800,
		},
	})
	if usage.InputTokens != 1200 || usage.OutputTokens != 300 {
		t.Fatalf("expected input/output tokens 1200/300, got %d/%d", usage.InputTokens, usage.OutputTokens)
	}
	if usage.CacheRead != 800 || usage.CacheWrite != 0 {
		t.Fatalf("expected cache usage read/write 800/0, got %d/%d", usage.CacheRead, usage.CacheWrite)
	}
}

func TestAnthropicUsageIncludesCacheTokens(t *testing.T) {
	usage := anthropicUsage(anthropic.Usage{
		InputTokens:              23,
		OutputTokens:             9,
		CacheCreationInputTokens: 128,
		CacheReadInputTokens:     8832,
	})
	if usage.InputTokens != 23 || usage.OutputTokens != 9 {
		t.Fatalf("expected input/output tokens 23/9, got %d/%d", usage.InputTokens, usage.OutputTokens)
	}
	if usage.CacheRead != 8832 || usage.CacheWrite != 128 {
		t.Fatalf("expected cache usage read/write 8832/128, got %d/%d", usage.CacheRead, usage.CacheWrite)
	}
}

func TestRetryDelayJitterRange(t *testing.T) {
	// Test that jitter keeps delays within ±25% of the base delay.
	// attempt=2 → base delay = 4s, jitter range = ±0.5s → [3.5s, 4.5s]
	base := time.Second * 4
	minExpected := base - base/4 // 3s
	maxExpected := base + base/4 // 5s

	for i := 0; i < 100; i++ {
		delay := retryDelay(nil, 2) // nil err → uses exponential backoff path
		if delay < minExpected || delay > maxExpected {
			t.Errorf("attempt %d: delay %v outside expected range [%v, %v]", i, delay, minExpected, maxExpected)
		}
	}

	// Verify attempt=5+ hits the cap (30s) with jitter → [22.5s, 37.5s]
	cap := providerRetryBackoffCap
	minCap := cap - cap/4
	maxCap := cap + cap/4
	for i := 0; i < 100; i++ {
		delay := retryDelay(nil, 10)
		if delay < minCap || delay > maxCap {
			t.Errorf("capped attempt %d: delay %v outside expected range [%v, %v]", i, delay, minCap, maxCap)
		}
	}
}

func TestRetryDelayRetryAfterNoJitter(t *testing.T) {
	// Retry-After header values must not get jitter — respect server instruction exactly.
	err := &anthropic.Error{
		StatusCode: http.StatusTooManyRequests,
		Response: &http.Response{
			Header: http.Header{
				"Retry-After": []string{"5"},
			},
		},
	}
	for i := 0; i < 50; i++ {
		delay := retryDelay(err, 3)
		if delay != 5*time.Second {
			t.Errorf("Retry-After delay should be exactly 5s (no jitter), got %v on iteration %d", delay, i)
		}
	}
}
