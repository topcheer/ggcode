package agentruntime

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/provider"
)

type fakeSamplingProvider struct {
	provider.Provider
	chat provider.ChatResponse
}

func (f *fakeSamplingProvider) Name() string { return "fake" }
func (f *fakeSamplingProvider) Chat(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	resp := f.chat
	return &resp, nil
}

func samplingParams(maxTokens int) mcp.SamplingParams {
	return mcp.SamplingParams{
		Messages:  []mcp.SamplingMessage{{Role: "user", Content: mcp.SamplingContent{Type: "text", Text: "hi"}}},
		MaxTokens: maxTokens,
	}
}

func textResp(text string, outputTokens int, stopReason string) provider.ChatResponse {
	return provider.ChatResponse{
		Message:    provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock(text)}},
		Usage:      provider.TokenUsage{OutputTokens: outputTokens},
		StopReason: stopReason,
	}
}

func TestIssue1484C_TruthBeatsHeuristic(t *testing.T) {
	p := &fakeSamplingProvider{chat: textResp("cut", 30, "max_tokens")}
	res, err := mcpSamplingHandlerWith(context.Background(), samplingParams(64), p)
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != "max_tokens" {
		t.Fatalf("provider-reported max_tokens must win, got %q", res.StopReason)
	}
}

func TestIssue1484C_NaturalLengthNotMisreported(t *testing.T) {
	p := &fakeSamplingProvider{chat: textResp("long natural answer", 100, "end_turn")}
	res, err := mcpSamplingHandlerWith(context.Background(), samplingParams(64), p)
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != "end_turn" {
		t.Fatalf("natural length must not be misreported as max_tokens, got %q", res.StopReason)
	}
}

func TestIssue1484C_StopSequenceRelayed(t *testing.T) {
	p := &fakeSamplingProvider{chat: textResp("ok", 5, "stop_sequence")}
	res, err := mcpSamplingHandlerWith(context.Background(), samplingParams(64), p)
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != "stop_sequence" {
		t.Fatalf("stop_sequence must be relayed, got %q", res.StopReason)
	}
}

func TestIssue1484C_NonMCPValueFoldsToEndTurn(t *testing.T) {
	p := &fakeSamplingProvider{chat: textResp("no", 5, "refusal")}
	res, err := mcpSamplingHandlerWith(context.Background(), samplingParams(64), p)
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != "end_turn" {
		t.Fatalf("refusal must fold to end_turn, got %q", res.StopReason)
	}
}

func TestIssue1484C_HeuristicFallbackWhenProviderSilent(t *testing.T) {
	p := &fakeSamplingProvider{chat: textResp("cut", 100, "")}
	res, err := mcpSamplingHandlerWith(context.Background(), samplingParams(64), p)
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != "max_tokens" {
		t.Fatalf("silent provider must keep length heuristic, got %q", res.StopReason)
	}
}
