package agentruntime

// #1484 case C regression: the MCP sampling handler used to guess
// stopReason from Usage.OutputTokens >= maxTokens - false-positiving on
// naturally-long output that was never truncated, and making the
// stop_sequence branch unreachable. Since #1484-C the provider relays
// the REAL stop reason through ChatResponse.StopReason; the handler
// prefers it and keeps the length heuristic only as a fallback for
// providers that report nothing.

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/provider"
)

// fakeSamplingProvider embeds the interface so only Chat/Name need stubs.
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

// Truth wins over the heuristic: provider says max_tokens with output
// BELOW the budget (old code would have reported end_turn from the
// heuristic? no - old code reported max_tokens only on >=; here truth
// reports a truncation the length heuristic could not see).
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

// The old false positive dies: natural output >= budget with provider
// saying end_turn must report end_turn, not the old heuristic's
// max_tokens guess.
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

// stop_sequence becomes reachable: the provider relays it and the
// handler passes it through (MCP spec value).
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

// Non-MCP values (e.g. refusal) fold to end_turn: the MCP spec allows
// only end_turn / stop_sequence / max_tokens.
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

// Fallback preserved: providers reporting nothing keep the pre-#1484-C
// length heuristic (backward compatibility for silent backends).
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
