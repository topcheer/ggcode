package provider

import (
	"fmt"

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
		return p, nil

	case "openai":
		p := NewOpenAIProviderWithBaseURL(resolved.APIKey, resolved.Model, resolved.MaxTokens, resolved.BaseURL)
		p.SetAdaptiveCap(cap)
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
		return prov, nil

	default:
		return nil, fmt.Errorf("unsupported protocol: %s (supported: anthropic, openai, gemini, copilot)", resolved.Protocol)
	}
}
