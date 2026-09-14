package provider

// #2300: the thinking-budget derivation must honor an active MCP sampling
// override (small window => shrink the budget, or disable thinking), not the
// static cap.

import (
	"testing"

	"google.golang.org/genai"
)

func newGeminiEffort2300(model, effort string) *GeminiProvider {
	p := &GeminiProvider{model: model, maxTokens: 8192}
	p.SetReasoningEffort(effort)
	return p
}

func Test2300_EffortBudgetHonorsSamplingOverride(t *testing.T) {
	for _, model := range []string{"gemini-2.5-pro", "gemini-2.0-flash"} {
		p := newGeminiEffort2300(model, "high")
		p.SetSamplingOverride(&SamplingOverride{MaxTokens: 8192})
		config := &genai.GenerateContentConfig{}
		p.applyReasoningEffort(config)
		if config.ThinkingConfig == nil || config.ThinkingConfig.ThinkingBudget == nil {
			t.Fatalf("%s: effort=high with a 8192 window must set a budget", model)
		}
		if b := *config.ThinkingConfig.ThinkingBudget; int(b) >= 8192 {
			t.Fatalf("%s: budget %d must stay under the 8192 window", model, b)
		}
	}
}

func Test2300_SmallOverrideDisablesThinking(t *testing.T) {
	// The exact bug shape: static cap 8192 + override 512 - the old code
	// derived budget from 8192 (>=512), exceeding the 512 output window.
	p := newGeminiEffort2300("gemini-2.5-pro", "high")
	p.SetSamplingOverride(&SamplingOverride{MaxTokens: 512})
	config := &genai.GenerateContentConfig{}
	p.applyReasoningEffort(config)
	if config.ThinkingConfig == nil || config.ThinkingConfig.ThinkingBudget == nil ||
		*config.ThinkingConfig.ThinkingBudget != 0 {
		t.Fatalf("512-token window must disable thinking (budget 0), got %+v", config.ThinkingConfig)
	}
}

func Test2300_Gemini3SmallOverrideUsesMinimalLevel(t *testing.T) {
	p := newGeminiEffort2300("gemini-3-pro-preview", "high")
	p.SetSamplingOverride(&SamplingOverride{MaxTokens: 512})
	config := &genai.GenerateContentConfig{}
	p.applyReasoningEffort(config)
	if config.ThinkingConfig == nil || config.ThinkingConfig.ThinkingLevel != genai.ThinkingLevelMinimal {
		t.Fatalf("512 window on gemini-3 must set Minimal level, got %+v", config.ThinkingConfig)
	}
}
