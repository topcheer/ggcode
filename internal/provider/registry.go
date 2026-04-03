package provider

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/config"
)

// NewProvider creates a Provider instance from the given config.
func NewProvider(cfg *config.Config) (Provider, error) {
	pc := cfg.GetProviderConfig()
	maxTok := pc.MaxTokens
	if maxTok == 0 {
		maxTok = 8192
	}

	switch cfg.Provider {
	case "anthropic":
		if pc.BaseURL != "" {
			return NewAnthropicProviderWithBaseURL(pc.APIKey, cfg.Model, maxTok, pc.BaseURL), nil
		}
		return NewAnthropicProvider(pc.APIKey, cfg.Model, maxTok), nil

	case "openai":
		if pc.BaseURL != "" {
			return NewOpenAIProviderWithBaseURL(pc.APIKey, cfg.Model, maxTok, pc.BaseURL), nil
		}
		return NewOpenAIProvider(pc.APIKey, cfg.Model, maxTok), nil

	case "gemini":
		prov, err := NewGeminiProvider(pc.APIKey, cfg.Model, maxTok)
		if err != nil {
			return nil, fmt.Errorf("creating gemini provider: %w", err)
		}
		return prov, nil

	default:
		return nil, fmt.Errorf("unsupported provider: %s (supported: anthropic, openai, gemini)", cfg.Provider)
	}
}
