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
	// MCP 2025-11-25 (SEP-1577): tool-enabled sampling - forward the server's
	// tool definitions to the LLM so it can emit tool_use decisions that the
	// server executes in follow-up turns.
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
		// #1592-A family follow-up (R232 audit item): image content was
		// silently stripped - only TextBlock was forwarded, so a vision
		// server's image sampling request degraded to empty text. All
		// three providers consume ImageBlock (anthropic base64 / openai
		// data URI / gemini inline), so the conversion is complete.
		// SEP-1577: array-form messages carry tool_use / tool_result blocks
		// which map onto the providers' native tool calling roles.
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
				// #2283: mimeType is OPTIONAL in the MCP schema - a
				// spec-legal omission used to pass "" straight through and
				// hard-fail all three providers (anthropic/gemini 400,
				// openai malformed data URL). Default to PNG, the most
				// interoperable choice per the spec's own examples.
				mime := b.MIMEType
				if mime == "" {
					mime = "image/png"
				}
				pblock = provider.ImageBlock(mime, b.Data)
			default:
				// "text" or an unknown/empty type - keep the text contract.
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

	// #1592-A: honor the server's budget - parsed then dropped before,
	// silently violating the MCP sampling contract (maxTokens:50 ran to
	// the model default). Best-effort: providers without the optional
	// setter keep their configured default.
	// #1612-A: serialize the whole set->chat->restore window - two
	// concurrent samplings used to interleave and leave the SHARED
	// provider stuck at the wrong value.
	// #2248: one atomic override pointer replaces the reflect maxTokens
	// mutation AND the #2239 stop-sequence field writes: the request
	// builders snapshot the pointer (race-free for concurrent main-agent
	// Chats, the reader side #1612 never covered) and the window stays
	// under the sampler mutex.
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

	// SEP-1577: tools flow into the provider's native tool-calling path.
	resp, err := p.Chat(ctx, messages, tools)
	if err != nil {
		return nil, fmt.Errorf("provider chat: %w", err)
	}

	// Extract text and tool_use decisions (SEP-1577) from the response.
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

	// #1484-C: prefer the provider-reported stop reason (relayed through
	// ChatResponse since #1484-C) over the length heuristic. The heuristic
	// false-positived (natural output >= budget with no truncation →
	// reported max_tokens for a completion that was never cut) and made
	// the stop_sequence branch unreachable. "toolUse" (SEP-1577) is legal
	// since 2025-11-25 when the model actually emitted tool_use blocks;
	// a stale provider report with no blocks falls back to end_turn.
	stopReason := "end_turn"
	switch resp.StopReason {
	case "max_tokens", "stop_sequence":
		stopReason = resp.StopReason
	case "toolUse":
		if len(toolUses) > 0 {
			stopReason = "toolUse"
		}
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
	}
	if stopReason == "toolUse" {
		// Array-form content: tool_use blocks, per the SEP-1577 response
		// schema (content is a list of ToolUseContent blocks).
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
