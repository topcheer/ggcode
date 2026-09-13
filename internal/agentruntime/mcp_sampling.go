package agentruntime

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/provider"
)

// mcpSamplingMaxTokensMu serializes the shared-provider maxTokens
// mutate->chat->restore window (#1612-A).
var samplingMaxTokensMu sync.Mutex

// #1484-D: sampling is the only LLM-consumption path with no gate - a
// buggy/malicious server could loop sampling requests and burn the
// budget with no prompt or breaker. Package-level rate limiter +
// concurrency cap apply to every sampling request regardless of the
// mcp_sampling_disabled switch.
const (
	samplingRateLimit   = 60          // requests per window
	samplingRateWindow  = time.Minute // sliding window
	samplingMaxInFlight = 2           // concurrent sampling chats
)

var (
	samplingGateMu      sync.Mutex
	samplingWindowStart time.Time
	samplingWindowCount int
	samplingInFlight    int
)

// samplingGateAllow admits one sampling request under the rate+inflight
// caps; release() must be called when the chat finishes.
func samplingGateAllow() (release func(), err error) {
	samplingGateMu.Lock()
	defer samplingGateMu.Unlock()
	now := time.Now()
	if now.Sub(samplingWindowStart) >= samplingRateWindow {
		samplingWindowStart = now
		samplingWindowCount = 0
	}
	if samplingWindowCount >= samplingRateLimit {
		return nil, fmt.Errorf("mcp sampling rate limit exceeded (%d/min) - refusing to protect the token budget (#1484-D)", samplingRateLimit)
	}
	if samplingInFlight >= samplingMaxInFlight {
		return nil, fmt.Errorf("mcp sampling concurrency limit reached (%d) - refusing concurrent sampling chats (#1484-D)", samplingMaxInFlight)
	}
	samplingWindowCount++
	samplingInFlight++
	return func() {
		samplingGateMu.Lock()
		samplingInFlight--
		samplingGateMu.Unlock()
	}, nil
}

// newMCPSamplingHandler binds the sampling handler to ONE runtime's
// provider getter (#1592-B): the package-global let a second session in
// the same process overwrite the first's provider - session A's sampling
// then ran on B's model and API key, with results tagged B.Name().
func newMCPSamplingHandler(providerFn func() provider.Provider, disabled bool) func(ctx context.Context, params mcp.SamplingParams) (*mcp.SamplingResult, error) {
	return func(ctx context.Context, params mcp.SamplingParams) (*mcp.SamplingResult, error) {
		// #1484-D: the kill switch (config mcp_sampling_disabled) fails
		// closed with an explanatory error instead of silently burning
		// budget on a path no permission system covers.
		if disabled {
			return nil, fmt.Errorf("mcp sampling disabled by config (mcp_sampling_disabled) - refusing server sampling request (#1484-D)")
		}
		release, err := samplingGateAllow()
		if err != nil {
			return nil, err
		}
		defer release()
		return mcpSamplingHandlerWith(ctx, params, providerFn())
	}
}

func mcpSamplingHandlerWith(ctx context.Context, params mcp.SamplingParams, p provider.Provider) (*mcp.SamplingResult, error) {
	if p == nil {
		return nil, fmt.Errorf("no LLM provider available for sampling")
	}

	// Convert MCP messages to provider messages.
	var messages []provider.Message
	if strings.TrimSpace(params.SystemPrompt) != "" {
		messages = append(messages, provider.Message{
			Role:    "system",
			Content: []provider.ContentBlock{provider.TextBlock(params.SystemPrompt)},
		})
	}
	for _, msg := range params.Messages {
		role := msg.Role
		if role != "user" && role != "assistant" {
			role = "user"
		}
		messages = append(messages, provider.Message{
			Role:    role,
			Content: []provider.ContentBlock{provider.TextBlock(msg.Content.Text)},
		})
	}

	// Sampling is a simple completion — no tools.
	maxTokens := mcp.EffectiveMaxTokens(params.MaxTokens)
	debug.Log("mcp-sampling", "handling request: %d messages, maxTokens=%d", len(messages), maxTokens)

	// #1592-A: honor the server's budget - parsed then dropped before,
	// silently violating the MCP sampling contract (maxTokens:50 ran to
	// the model default). Best-effort: providers without the optional
	// setter keep their configured default.
	if ms, ok := p.(provider.MaxTokensSetter); ok {
		// #1612-A: set/restore with NO mutex - two concurrent samplings
		// interleaved (A reads 8192 -> sets 50; B reads 50 -> sets 200; A
		// restores 8192; B restores 50) and the SHARED provider stayed at
		// 50 forever, truncating every main-agent chat until restart; the
		// naked writes also raced Chat's reads. Serialize the whole
		// mutate->chat->restore window; the sampling chat itself runs
		// inside so the restore is guaranteed before the next sampler.
		samplingMaxTokensMu.Lock()
		defer samplingMaxTokensMu.Unlock()
		prevField := reflect.ValueOf(ms).Elem().FieldByName("maxTokens")
		// Third-party MaxTokensSetter impls may not carry this field -
		// FieldByName on a missing field yields an invalid Value whose
		// .Int() panics (#1612 rider). Skip the mutation entirely then.
		if !prevField.IsValid() || prevField.Kind() != reflect.Int {
			debug.Log("mcp-sampling", "provider %T lacks an int maxTokens field; skipping per-request budget", p)
		} else {
			prev := prevField.Int()
			ms.SetMaxTokens(maxTokens)
			defer func() { ms.SetMaxTokens(int(prev)) }()
		}
	}

	resp, err := p.Chat(ctx, messages, nil)
	if err != nil {
		return nil, fmt.Errorf("provider chat: %w", err)
	}

	// Extract text from response.
	var text string
	for _, block := range resp.Message.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}

	stopReason := "end_turn"
	if resp.Usage.OutputTokens >= maxTokens {
		stopReason = "max_tokens"
	}

	result := &mcp.SamplingResult{
		Model:      p.Name(),
		Role:       "assistant",
		StopReason: stopReason,
		Content: mcp.SamplingContent{
			Type: "text",
			Text: text,
		},
	}

	debug.Log("mcp-sampling", "sampling complete: model=%s output_tokens=%d",
		result.Model, resp.Usage.OutputTokens)

	return result, nil
}
