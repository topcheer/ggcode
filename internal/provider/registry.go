package provider

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
)

// NewProvider creates a protocol adapter from a resolved endpoint.
func NewProvider(resolved *config.ResolvedEndpoint) (Provider, error) {
	if resolved == nil {
		return nil, fmt.Errorf("resolved endpoint is nil")
	}

	// One adaptive max-output-tokens cap per (vendor, baseURL, model). Shared
	// across reconstructions of the same logical endpoint so learned bounds
	// survive provider swaps.
	cap := AdaptiveCapFor(resolved.VendorID, resolved.BaseURL, resolved.Model, resolved.MaxTokens)

	switch resolved.Protocol {
	case "anthropic":
		p := NewAnthropicProviderWithBaseURL(resolved.APIKey, resolved.Model, resolved.MaxTokens, resolved.BaseURL)
		p.SetAdaptiveCap(cap)
		p.SetToolChoice(resolved.ToolChoice)
		if len(resolved.ServerTools) > 0 {
			p.SetServerTools(resolved.ServerTools)
		}
		return p, nil

	case "openai-responses":
		// sa-40: OpenAI Responses API (/v1/responses) - serves Codex-family
		// and o-series models that have no Chat Completions surface.
		rp := NewOpenAIResponsesProvider(resolved.APIKey, resolved.Model, resolved.MaxTokens, resolved.BaseURL)
		rp.SetReasoningEffort(resolved.ReasoningEffort)
		rp.SetToolChoice(resolved.ToolChoice)
		return rp, nil

	case "openai":
		// URL-sniff fallback: an "openai" protocol endpoint pointed at a
		// /responses path is a Responses-API endpoint, not a relay.
		if strings.HasSuffix(strings.TrimRight(resolved.BaseURL, "/"), "/responses") {
			rp := NewOpenAIResponsesProvider(resolved.APIKey, resolved.Model, resolved.MaxTokens, resolved.BaseURL)
			rp.SetReasoningEffort(resolved.ReasoningEffort)
			rp.SetToolChoice(resolved.ToolChoice)
			return rp, nil
		}
		p := NewOpenAIProviderWithBaseURL(resolved.APIKey, resolved.Model, resolved.MaxTokens, resolved.BaseURL)
		p.SetAdaptiveCap(cap)
		p.SetReasoningEffort(resolved.ReasoningEffort)
		p.SetToolChoice(resolved.ToolChoice)
		return p, nil

	case "copilot":
		if err := validateCopilotResolved(resolved.BaseURL, resolved.APIKey); err != nil {
			return nil, err
		}
		p := NewCopilotProvider(resolved.APIKey, resolved.Model, resolved.MaxTokens, resolved.BaseURL)
		p.SetAdaptiveCap(cap)
		return p, nil

	case "gemini":
		prov, err := NewGeminiProviderWithBaseURL(resolved.APIKey, resolved.Model, resolved.MaxTokens, resolved.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("creating gemini provider: %w", err)
		}
		prov.SetAdaptiveCap(cap)
		prov.SetReasoningEffort(resolved.ReasoningEffort)
		prov.SetToolChoice(resolved.ToolChoice)
		if len(resolved.ServerTools) > 0 {
			prov.SetServerTools(resolved.ServerTools)
		}
		return prov, nil

	default:
		return nil, fmt.Errorf("unsupported protocol: %s (supported: anthropic, openai, openai-responses, gemini, copilot)", resolved.Protocol)
	}
}
