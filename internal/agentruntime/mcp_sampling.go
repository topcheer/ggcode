package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/provider"
)

var samplingMaxTokensMu sync.Mutex

const (
	samplingRateLimit   = 60
	samplingRateWindow  = time.Minute
	samplingMaxInFlight = 2
)

var (
	samplingGateMu      sync.Mutex
	samplingWindowStart time.Time
	samplingWindowCount int
	samplingInFlight    int
)

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

func newMCPSamplingHandler(providerFn func() provider.Provider, disabled bool) func(ctx context.Context, params mcp.SamplingParams) (*mcp.SamplingResult, error) {
	return func(ctx context.Context, params mcp.SamplingParams) (*mcp.SamplingResult, error) {

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

	var messages []provider.Message
	if strings.TrimSpace(params.SystemPrompt) != "" {
		messages = append(messages, provider.Message{
			Role:    "system",
			Content: []provider.ContentBlock{provider.TextBlock(params.SystemPrompt)},
		})
	}
	var tools []provider.ToolDefinition
	if len(params.Tools) > 0 {
		tools = make([]provider.ToolDefinition, 0, len(params.Tools))
		for _, t := range params.Tools {
			tools = append(tools, provider.ToolDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			})
		}
	}
	for _, msg := range params.Messages {
		role := msg.Role
		if role != "user" && role != "assistant" {
			role = "user"
		}

		blocks := msg.Blocks
		if len(blocks) == 0 {
			blocks = []mcp.SamplingContent{msg.Content}
		}
		var pblocks []provider.ContentBlock
		for _, b := range blocks {
			var pblock provider.ContentBlock
			switch {
			case b.Type == "tool_use":
				pblock = provider.ToolUseBlock(b.ID, b.Name, b.Input)
			case b.Type == "tool_result":
				var sb strings.Builder
				for _, rb := range b.ResultContent {
					if rb.Text != "" {
						if sb.Len() > 0 {
							sb.WriteString("\n")
						}
						sb.WriteString(rb.Text)
					}
				}
				pblock = provider.ToolResultBlock(b.ToolUseID, sb.String(), b.IsError)
			case b.Type == "image" && b.Data != "":

				mime := b.MIMEType
				if mime == "" {
					mime = "image/png"
				}
				pblock = provider.ImageBlock(mime, b.Data)
			default:

				pblock = provider.TextBlock(b.Text)
			}
			pblocks = append(pblocks, pblock)
		}
		messages = append(messages, provider.Message{
			Role:    role,
			Content: pblocks,
		})
	}

	maxTokens := mcp.EffectiveMaxTokens(params.MaxTokens)
	debug.Log("mcp-sampling", "handling request: %d messages, maxTokens=%d", len(messages), maxTokens)

	samplingMaxTokensMu.Lock()
	defer samplingMaxTokensMu.Unlock()
	if so, ok := p.(provider.SamplingOverrideSetter); ok {
		prev := so.SamplingOverride()
		so.SetSamplingOverride(&provider.SamplingOverride{
			MaxTokens:     maxTokens,
			StopSequences: params.StopSequences,
			Temperature:   params.Temperature,
		})
		defer func() { so.SetSamplingOverride(prev) }()
	}

	resp, err := p.Chat(ctx, messages, tools)
	if err != nil {
		return nil, fmt.Errorf("provider chat: %w", err)
	}

	var text string
	var toolUses []mcp.SamplingContent
	for _, block := range resp.Message.Content {
		switch block.Type {
		case "text":
			text += block.Text
		case "tool_use":
			input := block.Input
			if input == nil {
				input = json.RawMessage("{}")
			}
			toolUses = append(toolUses, mcp.SamplingContent{
				Type:  "tool_use",
				ID:    block.ToolID,
				Name:  block.ToolName,
				Input: input,
			})
		}
	}

	stopReason := "end_turn"
	switch resp.StopReason {
	case "max_tokens", "stop_sequence":
		stopReason = resp.StopReason
	case "toolUse":
		if len(toolUses) > 0 {
			stopReason = "toolUse"
		}
	case "":

		if resp.Usage.OutputTokens >= maxTokens && maxTokens > 0 {
			stopReason = "max_tokens"
		}
	}

	result := &mcp.SamplingResult{
		Model:      p.Name(),
		Role:       "assistant",
		StopReason: stopReason,
	}
	if stopReason == "toolUse" {

		result.Blocks = toolUses
	} else {
		result.Content = mcp.SamplingContent{
			Type: "text",
			Text: text,
		}
	}

	debug.Log("mcp-sampling", "sampling complete: model=%s output_tokens=%d",
		result.Model, resp.Usage.OutputTokens)

	return result, nil
}
