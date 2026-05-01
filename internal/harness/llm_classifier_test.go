package harness

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// mockClassifierProvider is a test provider that returns a fixed response.
type mockClassifierProvider struct {
	response string
	err      error
	delay    time.Duration
}

func (m *mockClassifierProvider) Name() string { return "mock" }

func (m *mockClassifierProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if m.err != nil {
		return nil, m.err
	}
	return &provider.ChatResponse{
		Message: provider.Message{
			Content: []provider.ContentBlock{provider.TextBlock(m.response)},
		},
	}, nil
}

func (m *mockClassifierProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	return nil, nil
}

func (m *mockClassifierProvider) CountTokens(ctx context.Context, messages []provider.Message) (int, error) {
	return 0, nil
}

func TestParseClassifierResponse_CodeChange(t *testing.T) {
	input := `{"classification": "code_change", "confidence": 0.9, "reason": "User wants to fix a bug"}`
	result, err := parseClassifierResponse(input)
	if err != nil {
		t.Fatalf("parseClassifierResponse() error = %v", err)
	}
	if !result.IsCodeChange {
		t.Error("expected IsCodeChange = true")
	}
	if result.Confidence != 0.9 {
		t.Errorf("confidence = %f, want 0.9", result.Confidence)
	}
	if result.Reason != "User wants to fix a bug" {
		t.Errorf("reason = %q, want bug fix", result.Reason)
	}
}

func TestParseClassifierResponse_Conversation(t *testing.T) {
	input := `{"classification": "conversation", "confidence": 0.85, "reason": "User is asking a question"}`
	result, err := parseClassifierResponse(input)
	if err != nil {
		t.Fatalf("parseClassifierResponse() error = %v", err)
	}
	if result.IsCodeChange {
		t.Error("expected IsCodeChange = false")
	}
}

func TestParseClassifierResponse_MarkdownWrapped(t *testing.T) {
	input := "```json\n{\"classification\": \"code_change\", \"confidence\": 0.7, \"reason\": \"test\"}\n```"
	result, err := parseClassifierResponse(input)
	if err != nil {
		t.Fatalf("parseClassifierResponse() error = %v", err)
	}
	if !result.IsCodeChange {
		t.Error("expected IsCodeChange = true from markdown-wrapped JSON")
	}
}

func TestParseClassifierResponse_ConfidenceClamped(t *testing.T) {
	tests := []struct {
		name        string
		confidence  float64
		wantClamped float64
	}{
		{"negative", -0.5, 0},
		{"over 1", 1.5, 1},
		{"normal", 0.75, 0.75},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := `{"classification": "code_change", "confidence": ` + fmt.Sprintf("%f", tt.confidence) + `, "reason": "test"}`
			result, err := parseClassifierResponse(input)
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if result.Confidence != tt.wantClamped {
				t.Errorf("confidence = %f, want %f", result.Confidence, tt.wantClamped)
			}
		})
	}
}

func TestParseClassifierResponse_InvalidJSON(t *testing.T) {
	_, err := parseClassifierResponse("not json at all")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestClassifyWithLLM_NilProvider(t *testing.T) {
	result, err := ClassifyWithLLM(context.Background(), nil, "fix the auth bug in login.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result when provider is nil")
	}
}

func TestClassifyWithLLM_TooShort(t *testing.T) {
	prov := &mockClassifierProvider{}
	result, err := ClassifyWithLLM(context.Background(), prov, "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for short input")
	}
}

func TestClassifyWithLLM_SuccessfulClassification(t *testing.T) {
	prov := &mockClassifierProvider{
		response: `{"classification": "code_change", "confidence": 0.92, "reason": "bug fix request"}`,
	}
	result, err := ClassifyWithLLM(context.Background(), prov, "the login page shows a 500 error when the session expires")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsCodeChange {
		t.Error("expected IsCodeChange = true")
	}
	if result.Confidence < 0.9 {
		t.Errorf("confidence = %f, want >= 0.9", result.Confidence)
	}
}

func TestClassifyWithLLM_ProviderError(t *testing.T) {
	prov := &mockClassifierProvider{
		err: context.DeadlineExceeded,
	}
	result, err := ClassifyWithLLM(context.Background(), prov, "this is a long enough input for the classifier to process")
	if err == nil {
		t.Error("expected error from provider failure")
	}
	if result != nil {
		t.Error("expected nil result on provider error")
	}
}

func TestClassifyWithLLM_Timeout(t *testing.T) {
	prov := &mockClassifierProvider{
		response: `{"classification": "code_change", "confidence": 0.9, "reason": "test"}`,
		delay:    5 * time.Second, // longer than 3s timeout
	}
	result, err := ClassifyWithLLM(context.Background(), prov, "this is a long enough input for the classifier to process")
	if err == nil {
		t.Error("expected timeout error")
	}
	if result != nil {
		t.Error("expected nil result on timeout")
	}
}

func TestRouteFromLLMResult(t *testing.T) {
	tests := []struct {
		name      string
		result    *LLMClassifierResult
		mode      string
		wantRoute RouteDecision
	}{
		{"nil result", nil, "on", RouteNormal},
		{"not code change", &LLMClassifierResult{IsCodeChange: false, Confidence: 0.9}, "on", RouteNormal},
		{"high confidence on", &LLMClassifierResult{IsCodeChange: true, Confidence: 0.85}, "on", RouteHarness},
		{"medium confidence on", &LLMClassifierResult{IsCodeChange: true, Confidence: 0.65}, "on", RouteSuggest},
		{"low confidence on", &LLMClassifierResult{IsCodeChange: true, Confidence: 0.3}, "on", RouteNormal},
		{"high confidence strict", &LLMClassifierResult{IsCodeChange: true, Confidence: 0.6}, "strict", RouteHarness},
		{"low confidence strict", &LLMClassifierResult{IsCodeChange: true, Confidence: 0.3}, "strict", RouteNormal},
		{"suggest mode ignored", &LLMClassifierResult{IsCodeChange: true, Confidence: 0.9}, "suggest", RouteNormal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RouteFromLLMResult(tt.result, tt.mode)
			if got != tt.wantRoute {
				t.Errorf("RouteFromLLMResult() = %v, want %v", got, tt.wantRoute)
			}
		})
	}
}

func TestClassifyWithLLM_TruncatesLongInput(t *testing.T) {
	longInput := strings.Repeat("a", 3000)
	prov := &mockClassifierProvider{
		response: `{"classification": "conversation", "confidence": 0.8, "reason": "test"}`,
	}
	result, err := ClassifyWithLLM(context.Background(), prov, longInput)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result for long input")
	}
}
