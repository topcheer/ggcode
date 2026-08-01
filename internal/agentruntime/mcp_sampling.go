package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/provider"
)

// mcpSamplingProvider holds the LLM provider used for MCP sampling requests.
// It is set lazily after the agent (and its provider) are created, since the
// provider is not available at MCPManager construction time.
var (
	samplingProvider   provider.Provider
	samplingProviderMu sync.RWMutex
)

// SetSamplingProvider sets the LLM provider used to handle MCP sampling
// (sampling/createMessage) requests. Called after the agent is created.
func SetSamplingProvider(p provider.Provider) {
	samplingProviderMu.Lock()
	samplingProvider = p
	samplingProviderMu.Unlock()
	debug.Log("mcp-sampling", "sampling provider set: %T", p)
}

// mcpSamplingHandler implements mcp.SamplingHandler using the configured provider.
// If no provider is set, it returns an error.
func mcpSamplingHandler(ctx context.Context, params mcp.SamplingParams) (*mcp.SamplingResult, error) {
	samplingProviderMu.RLock()
	p := samplingProvider
	samplingProviderMu.RUnlock()

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
