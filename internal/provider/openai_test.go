package provider

import (
	"context"
	"net/http"
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

func TestEstimateTokensFromChars(t *testing.T) {
	if got := estimateTokensFromChars(0); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
	if got := estimateTokensFromChars(3); got != 1 {
		t.Fatalf("expected minimum 1 token for non-empty output, got %d", got)
	}
	if got := estimateTokensFromChars(40); got != 10 {
		t.Fatalf("expected 10, got %d", got)
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
	if isRetryable(&openai.APIError{HTTPStatusCode: http.StatusBadRequest, Message: "bad request"}) {
		t.Fatal("expected openai 400 not to be retryable")
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
