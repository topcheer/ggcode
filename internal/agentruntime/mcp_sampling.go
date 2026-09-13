package agentruntime

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/provider"
)

// mcpSamplingMaxTokensMu serializes the shared-provider maxTokens
// mutate->chat->restore window (#1612-A).
var samplingMaxTokensMu sync.Mutex

// newMCPSamplingHandler binds the sampling handler to ONE runtime's
// provider getter (#1592-B): the package-global let a second session in
// the same process overwrite the first's provider - session A's sampling
// then ran on B's model and API key, with results tagged B.Name().
func newMCPSamplingHandler(providerFn func() provider.Provider) func(ctx context.Context, params mcp.SamplingParams) (*mcp.SamplingResult, error) {
	return func(ctx context.Context, params mcp.SamplingParams) (*mcp.SamplingResult, error) {
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

	// #1484-C: prefer the provider-reported stop reason (relayed through
	// ChatResponse since #1484-C) over the length heuristic. The heuristic
	// false-positived (natural output >= budget with no truncation →
	// reported max_tokens for a completion that was never cut) and made
	// the stop_sequence branch unreachable. Map non-MCP values (refusal,
	// tool_use, ...) to end_turn: MCP spec allows only end_turn /
	// stop_sequence / max_tokens.
	stopReason := "end_turn"
	switch resp.StopReason {
	case "max_tokens", "stop_sequence":
		stopReason = resp.StopReason
	case "":
		// Provider did not report one — keep the pre-#1484-C heuristic as
		// the fallback for providers without stop-reason relay.
		if resp.Usage.OutputTokens >= maxTokens && maxTokens > 0 {
			stopReason = "max_tokens"
		}
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
