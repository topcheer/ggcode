package agentruntime

// #1484 case D regression: sampling was the only LLM-consumption path
// with no gate - no permission check, no rate cap, not even an off
// switch. A buggy/malicious MCP server could loop sampling requests and
// burn the provider budget with no prompt. The gate: a config kill
// switch (mcp_sampling_disabled, zero value keeps sampling ON) plus a
// package-level rate cap (60/min) and concurrency cap (2) that apply
// regardless of the switch.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/provider"
)

type gateFakeProvider struct {
	provider.Provider
	block chan struct{}
}

func (f *gateFakeProvider) Name() string { return "fake" }
func (f *gateFakeProvider) Chat(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	if f.block != nil {
		<-f.block // hold the in-flight slot (concurrency-cap test)
	}
	resp := provider.ChatResponse{
		Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("ok")}},
	}
	return &resp, nil
}

func resetSamplingGate() {
	samplingGateMu.Lock()
	samplingWindowStart = samplingWindowStart.Add(-2 * samplingRateWindow)
	samplingWindowCount = 0
	samplingInFlight = 0
	samplingGateMu.Unlock()
}

// The kill switch fails closed with an explanatory error.
func TestIssue1484D_DisabledSwitchRefuses(t *testing.T) {
	resetSamplingGate()
	defer resetSamplingGate()
	h := newMCPSamplingHandler(func() provider.Provider { return &gateFakeProvider{} }, true)
	_, err := h(context.Background(), mcp.SamplingParams{MaxTokens: 16})
	if err == nil || !strings.Contains(err.Error(), "mcp_sampling_disabled") {
		t.Fatalf("disabled switch must refuse with an explanatory error, got %v", err)
	}
}

// Enabled path passes through the gate untouched.
func TestIssue1484D_EnabledPasses(t *testing.T) {
	resetSamplingGate()
	defer resetSamplingGate()
	h := newMCPSamplingHandler(func() provider.Provider { return &gateFakeProvider{} }, false)
	res, err := h(context.Background(), mcp.SamplingParams{MaxTokens: 16})
	if err != nil {
		t.Fatal(err)
	}
	if res.Content.Text != "ok" {
		t.Fatalf("enabled sampling must pass, got %q", res.Content.Text)
	}
}

// The per-minute rate cap refuses request 61 even with the switch off.
func TestIssue1484D_RateCapRefuses(t *testing.T) {
	resetSamplingGate()
	defer resetSamplingGate()
	h := newMCPSamplingHandler(func() provider.Provider { return &gateFakeProvider{} }, false)
	var lastErr error
	for i := 0; i < samplingRateLimit+1; i++ {
		_, lastErr = h(context.Background(), mcp.SamplingParams{MaxTokens: 16})
	}
	if lastErr == nil || !strings.Contains(lastErr.Error(), "rate limit") {
		t.Fatalf("request %d must hit the rate cap, got %v", samplingRateLimit+1, lastErr)
	}
}

// The concurrency cap refuses a third simultaneous sampling chat.
func TestIssue1484D_ConcurrencyCapRefuses(t *testing.T) {
	resetSamplingGate()
	defer resetSamplingGate()
	block := make(chan struct{})
	p := &gateFakeProvider{block: block}
	h := newMCPSamplingHandler(func() provider.Provider { return p }, false)

	var wg sync.WaitGroup
	for i := 0; i < samplingMaxInFlight; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = h(context.Background(), mcp.SamplingParams{MaxTokens: 16})
		}()
	}
	// Let the two in-flight chats park on the block channel.
	deadline := time.Now().Add(5 * time.Second)
	for {
		samplingGateMu.Lock()
		inFlight := samplingInFlight
		samplingGateMu.Unlock()
		if inFlight >= samplingMaxInFlight || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, err := h(context.Background(), mcp.SamplingParams{MaxTokens: 16})
	if err == nil || !strings.Contains(err.Error(), "concurrency limit") {
		t.Fatalf("third concurrent sampling must be refused, got %v", err)
	}
	close(block)
	wg.Wait()
}
