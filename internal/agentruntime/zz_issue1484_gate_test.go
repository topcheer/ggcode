package agentruntime

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
		<-f.block
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

func TestIssue1484D_DisabledSwitchRefuses(t *testing.T) {
	resetSamplingGate()
	defer resetSamplingGate()
	h := newMCPSamplingHandler(func() provider.Provider { return &gateFakeProvider{} }, true)
	_, err := h(context.Background(), mcp.SamplingParams{MaxTokens: 16})
	if err == nil || !strings.Contains(err.Error(), "mcp_sampling_disabled") {
		t.Fatalf("disabled switch must refuse with an explanatory error, got %v", err)
	}
}

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
