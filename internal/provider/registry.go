package provider

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
)

// strictToolsAllow resolves the strict-tool allowlist for an endpoint:
// disabled unless strict_tools is set; an empty allow list falls back to
// DefaultStrictTools (Anthropic caps strict tools at 20/request, so this is
// a curated set, never all ~191 registered tools).
func strictToolsAllow(resolved *config.ResolvedEndpoint) map[string]bool {
	if !resolved.StrictTools {
		return nil
	}
	allow := resolved.StrictToolsAllow
	if len(allow) == 0 {
		allow = DefaultStrictTools
	}
	return StrictToolsAllowlist(allow)
}

// NewProvider creates a protocol adapter from a resolved endpoint.
func NewProvider(resolved *config.ResolvedEndpoint) (Provider, error) {
	if resolved == nil {
		return nil, fmt.Errorf("resolved endpoint is nil")
	}

	// One adaptive max-output-tokens cap per (vendor, baseURL, model). Shared
	// across reconstructions of the same logical endpoint so learned bounds
	// survive provider swaps.
	cap := AdaptiveCapFor(resolved.VendorID, resolved.BaseURL, resolved.Model, resolved.MaxTokens)
	// sa-78: resolved call policy (timeout/retry budget). Applied to the
	// OpenAI/Anthropic/Gemini providers via callPolicySetter; protocols
	// without the setter keep the pre-existing defaults.
	policy := callPolicyFromResolved(resolved)

	switch resolved.Protocol {
	case "anthropic":
		p := NewAnthropicProviderWithBaseURL(resolved.APIKey, resolved.Model, resolved.MaxTokens, resolved.BaseURL)
		p.SetAdaptiveCap(cap)
		p.setCallPolicy(policy)
		p.SetToolChoice(resolved.ToolChoice)
		p.SetStrictTools(strictToolsAllow(resolved))
		if len(resolved.ServerTools) > 0 {
			p.SetServerTools(resolved.ServerTools)
		}
		if resolved.MemoryTool {
			p.SetMemoryTool(true)
		}
		if resolved.ThinkingMode != "" {
			p.SetThinkingMode(resolved.ThinkingMode)
		}
		p.SetContextEditing(ParseContextEditing(resolved.ContextEditing))
		return p, nil

	case "openai-responses":
		// sa-40: OpenAI Responses API (/v1/responses) - serves Codex-family
		// and o-series models that have no Chat Completions surface.
		rp := NewOpenAIResponsesProvider(resolved.APIKey, resolved.Model, resolved.MaxTokens, resolved.BaseURL)
		rp.SetReasoningEffort(resolved.ReasoningEffort)
		rp.SetTextVerbosity(resolved.TextVerbosity)
		rp.SetServiceTier(resolved.ServiceTier)
		rp.SetToolChoice(resolved.ToolChoice)
		if len(resolved.ServerTools) > 0 {
			rp.SetServerTools(resolved.ServerTools)
			rp.SetServerTools(resolved.ServerTools) // sa-63: code_interpreter / file_search
		}
		return rp, nil

	case "openai":
		// URL-sniff fallback: an "openai" protocol endpoint pointed at a
		// /responses path is a Responses-API endpoint, not a relay.
		if strings.HasSuffix(strings.TrimRight(resolved.BaseURL, "/"), "/responses") {
			rp := NewOpenAIResponsesProvider(resolved.APIKey, resolved.Model, resolved.MaxTokens, resolved.BaseURL)
			rp.SetReasoningEffort(resolved.ReasoningEffort)
			rp.SetTextVerbosity(resolved.TextVerbosity)
			rp.SetServiceTier(resolved.ServiceTier)
			rp.SetToolChoice(resolved.ToolChoice)
			if len(resolved.ServerTools) > 0 {
				rp.SetServerTools(resolved.ServerTools)
				rp.SetServerTools(resolved.ServerTools) // sa-63: code_interpreter / file_search
			}
			return rp, nil
		}
		p := NewOpenAIProviderWithBaseURL(resolved.APIKey, resolved.Model, resolved.MaxTokens, resolved.BaseURL)
		p.SetAdaptiveCap(cap)
		p.setCallPolicy(policy)
		p.SetReasoningEffort(resolved.ReasoningEffort)
		p.SetServiceTier(resolved.ServiceTier)
		p.SetToolChoice(resolved.ToolChoice)
		p.SetLogprobsRequest(resolved.Logprobs) // sa-74
		p.SetStrictTools(strictToolsAllow(resolved))
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
		prov.setCallPolicy(policy)
		prov.SetReasoningEffort(resolved.ReasoningEffort)
		prov.SetToolChoice(resolved.ToolChoice)
		prov.SetLogprobsRequest(resolved.Logprobs) // sa-74
		if len(resolved.ServerTools) > 0 {
			prov.SetServerTools(resolved.ServerTools)
		}
		return prov, nil

	default:
		return nil, fmt.Errorf("unsupported protocol: %s (supported: anthropic, openai, openai-responses, gemini, copilot)", resolved.Protocol)
	}
}
