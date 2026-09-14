package provider

import (
	"testing"

	"google.golang.org/genai"
)

// #2282: Gemini must consume the sampling override's MaxTokens like the
// other two providers - MCP sampling budgets used to run to the model
// default on this backend.
func TestIssue2282GeminiConsumesOverrideMaxTokens(t *testing.T) {
	g := &GeminiProvider{temperature: 0.7}
	so := SamplingOverrideSetter(g)
	so.SetSamplingOverride(&SamplingOverride{MaxTokens: 777})
	defer so.SetSamplingOverride(nil)

	if got := g.effectiveMaxTokens(); got != 777 {
		t.Errorf("override budget must win, got %d", got)
	}
	cfg := &genai.GenerateContentConfig{}
	g.applySamplingConfig(cfg)
	if cfg.MaxOutputTokens != 777 {
		t.Errorf("MaxOutputTokens must reach the config, got %d", cfg.MaxOutputTokens)
	}
}

func TestIssue2282GeminiFallbackChain(t *testing.T) {
	g := &GeminiProvider{}
	if got := g.effectiveMaxTokens(); got != 0 {
		t.Errorf("no override/cap/config: zero must hold, got %d", got)
	}
	// and the config leg must stay silent on zero (no fake cap)
	cfg := &genai.GenerateContentConfig{}
	g.applySamplingConfig(cfg)
	if cfg.MaxOutputTokens != 0 {
		t.Errorf("zero budget must not be written, got %d", cfg.MaxOutputTokens)
	}
}
