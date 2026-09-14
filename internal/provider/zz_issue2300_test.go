package provider

import (
	"testing"

	"google.golang.org/genai"
)

// #2300: the thinking budget must derive from the EFFECTIVE budget chain so
// an active MCP sampling override shrinks (or disables) thinking instead of
// producing a budget far above MaxOutputTokens (hard 400 / empty reply).
func TestIssue2300BudgetHonorsOverride(t *testing.T) {
	g := &GeminiProvider{maxTokens: 8192, reasoningEffort: "high"}
	g.SetSamplingOverride(&SamplingOverride{MaxTokens: 4096})
	defer g.SetSamplingOverride(nil)

	cfg := &genai.GenerateContentConfig{}
	g.applyReasoningEffort(cfg)

	if cfg.ThinkingConfig == nil || cfg.ThinkingConfig.ThinkingBudget == nil {
		t.Fatal("expected a thinking budget")
	}
	if got := *cfg.ThinkingConfig.ThinkingBudget; got != 3072 { // 4096*3/4
		t.Errorf("budget must derive from the override (4096), got %d", got)
	}
}

// small override disables thinking entirely instead of an oversized budget
func TestIssue2300SmallOverrideDisablesThinking(t *testing.T) {
	g := &GeminiProvider{maxTokens: 8192, reasoningEffort: "high"}
	g.SetSamplingOverride(&SamplingOverride{MaxTokens: 512})
	defer g.SetSamplingOverride(nil)

	cfg := &genai.GenerateContentConfig{}
	g.applyReasoningEffort(cfg)

	if cfg.ThinkingConfig == nil || cfg.ThinkingConfig.ThinkingBudget == nil {
		t.Fatal("expected an explicit budget")
	}
	if got := *cfg.ThinkingConfig.ThinkingBudget; got != 0 {
		t.Errorf("override 512 must disable thinking (budget 0), got %d", got)
	}
}

// no override: unchanged legacy behavior (default maxTokens base)
func TestIssue2300NoOverrideUnchanged(t *testing.T) {
	g := &GeminiProvider{maxTokens: 8192, reasoningEffort: "low"}

	cfg := &genai.GenerateContentConfig{}
	g.applyReasoningEffort(cfg)

	if cfg.ThinkingConfig == nil || cfg.ThinkingConfig.ThinkingBudget == nil {
		t.Fatal("expected a thinking budget")
	}
	if got := *cfg.ThinkingConfig.ThinkingBudget; got != 2048 { // 8192/4
		t.Errorf("no override: legacy low-effort budget expected 2048, got %d", got)
	}
}
