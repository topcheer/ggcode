package provider

import (
	"context"
	"strings"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

func TestAdaptiveThinkingForModel(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"", false},
		{"gpt-4o", false},
		{"claude-3-7-sonnet-20250219", false},
		{"claude-sonnet-4-20250514", false},
		{"claude-sonnet-4-5-20250929", false},
		{"claude-opus-4-5", false},
		{"claude-haiku-4-5", false},
		{"claude-sonnet-4-6", true},
		{"claude-sonnet-4-6-20260101", true},
		{"claude-opus-4-6", true},
		{"claude-haiku-4-7", true},
		{"claude-opus-5", true},
		{"claude-sonnet-5-20260101", true},
		{"claude-5", true},
		{"claude-fable-5-1", true},
		{"claude-mythos-5-1", true},
		{"CLAUDE-OPUS-4-6", true}, // case-insensitive
	}
	for _, tc := range cases {
		if got := adaptiveThinkingForModel(tc.model); got != tc.want {
			t.Errorf("adaptiveThinkingForModel(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestSetThinkingMode(t *testing.T) {
	p := NewAnthropicProvider("test-key", "claude-sonnet-4-5", 32000)
	// Auto-detect: old generation -> manual.
	if p.useAdaptiveThinking() {
		t.Fatal("claude-sonnet-4-5 must default to manual mode")
	}
	p.SetThinkingMode(" Adaptive ")
	if p.ThinkingMode() != "adaptive" || !p.useAdaptiveThinking() {
		t.Fatalf("SetThinkingMode normalize failed: %q adaptive=%v", p.ThinkingMode(), p.useAdaptiveThinking())
	}
	p.SetThinkingMode("MANUAL")
	if p.ThinkingMode() != "manual" || p.useAdaptiveThinking() {
		t.Fatalf("manual override failed: %q adaptive=%v", p.ThinkingMode(), p.useAdaptiveThinking())
	}
	p.SetThinkingMode("bogus")
	if p.ThinkingMode() != "" || p.useAdaptiveThinking() {
		t.Fatalf("unknown value must reset to auto: %q adaptive=%v", p.ThinkingMode(), p.useAdaptiveThinking())
	}
	// Auto-detect on a 4.6 model.
	p2 := NewAnthropicProvider("test-key", "claude-opus-4-6", 32000)
	if !p2.useAdaptiveThinking() {
		t.Fatal("claude-opus-4-6 must auto-select adaptive thinking")
	}
}

func TestBuildParamsAdaptiveThinking(t *testing.T) {
	p := NewAnthropicProvider("test-key", "claude-opus-4-6", 32000)
	p.SetReasoningEffort("low")
	p.effortCarrier.Store(true)
	params := p.buildParams(context.Background(), []Message{}, nil)
	if params.Thinking.OfAdaptive == nil {
		t.Fatal("adaptive model must carry thinking:{type:adaptive}, got no OfAdaptive")
	}
	if params.Thinking.OfEnabled != nil {
		t.Fatal("adaptive model must not carry budget_tokens")
	}
	// First call: effort window not yet stable, but adaptive models need
	// effort as their only depth control, so it must ride immediately.
	if string(params.OutputConfig.Effort) != "low" {
		t.Fatalf("adaptive effort = %q, want low", params.OutputConfig.Effort)
	}
}

func TestBuildParamsManualGeneration(t *testing.T) {
	p := NewAnthropicProvider("test-key", "claude-sonnet-4-5-20250929", 32000)
	p.SetReasoningEffort("high")
	params := p.buildParams(context.Background(), []Message{}, nil)
	if params.Thinking.OfEnabled == nil {
		t.Fatal("manual generation must carry budget_tokens thinking")
	}
	if params.Thinking.OfAdaptive != nil {
		t.Fatal("manual generation must not carry adaptive thinking")
	}
	// Manual mode composes budget with the carrier hysteresis: no effort on
	// the first call (stability window not yet established).
	if params.OutputConfig.Effort != "" {
		t.Fatalf("manual generation first call must not attach effort, got %q", params.OutputConfig.Effort)
	}
}

func TestBuildParamsThinkingModeOverride(t *testing.T) {
	// manual override on an adaptive-capable model -> budget_tokens + beta.
	p := NewAnthropicProvider("test-key", "claude-opus-4-6", 32000)
	p.SetThinkingMode("manual")
	p.SetReasoningEffort("medium")
	params := p.buildParams(context.Background(), []Message{}, nil)
	if params.Thinking.OfEnabled == nil || params.Thinking.OfAdaptive != nil {
		t.Fatal("manual override must force budget_tokens on 4.6 models")
	}
	if got := p.anthropicBetaHeader(true); got != "interleaved-thinking-2025-05-14" {
		t.Fatalf("manual tool-using beta = %q, want interleaved-thinking beta", got)
	}
	if got := p.anthropicBetaHeader(false); got != "" {
		t.Fatalf("manual non-tool beta = %q, want empty", got)
	}

	// adaptive override on the extended-thinking-only generation.
	p2 := NewAnthropicProvider("test-key", "claude-haiku-4-5", 32000)
	p2.SetThinkingMode("adaptive")
	p2.SetReasoningEffort("high")
	params2 := p2.buildParams(context.Background(), []Message{}, nil)
	if params2.Thinking.OfAdaptive == nil {
		t.Fatal("adaptive override must force adaptive thinking on old models")
	}
	if got := p2.anthropicBetaHeader(true); got != "" {
		t.Fatalf("adaptive mode must not set beta header, got %q", got)
	}
}

func TestBuildParamsXhighEffortCarrier(t *testing.T) {
	// xhigh/max remain carrier-only (no thinking param) on the manual
	// generation - unchanged pre-existing behavior.
	p := NewAnthropicProvider("test-key", "claude-sonnet-4-5", 32000)
	p.SetReasoningEffort("xhigh")
	params := p.buildParams(context.Background(), []Message{}, nil)
	if params.Thinking.OfEnabled != nil || params.Thinking.OfAdaptive != nil {
		t.Fatal("xhigh on manual generation must not enable thinking params")
	}

	// On adaptive-capable models xhigh/max select adaptive thinking and the
	// matching effort level directly (they exist in output_config.effort).
	p2 := NewAnthropicProvider("test-key", "claude-opus-4-6", 32000)
	p2.SetReasoningEffort("max")
	p2.effortCarrier.Store(true)
	params2 := p2.buildParams(context.Background(), []Message{}, nil)
	if params2.Thinking.OfAdaptive == nil {
		t.Fatal("max effort on adaptive model must enable adaptive thinking")
	}
	if string(params2.OutputConfig.Effort) != "max" {
		t.Fatalf("max effort on adaptive model: effort = %q, want max", params2.OutputConfig.Effort)
	}
}

func TestBuildParamsNoEffortNoThinking(t *testing.T) {
	p := NewAnthropicProvider("test-key", "claude-opus-4-6", 32000)
	params := p.buildParams(context.Background(), []Message{}, nil)
	if params.Thinking.OfEnabled != nil || params.Thinking.OfAdaptive != nil {
		t.Fatal("no effort must mean no thinking param")
	}
	if got := p.anthropicBetaHeader(true); got != "" {
		t.Fatalf("no effort must mean no beta header, got %q", got)
	}
}

func TestAdaptiveEffort(t *testing.T) {
	for in, want := range map[string]string{
		"low": "low", "Medium": "medium", "HIGH": "high",
		"xhigh": "xhigh", " max ": "max", "": "", "ultra": "",
	} {
		if got := adaptiveEffort(in); got != want {
			t.Errorf("adaptiveEffort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAdaptiveThinkingUnionSerialization(t *testing.T) {
	// The adaptive variant's Type field marshals its zero value as
	// "adaptive" (SDK default tag) - verify the wire shape we rely on.
	u := anthropic.ThinkingConfigParamUnion{
		OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{},
	}
	b, err := u.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"type":"adaptive"`) && !strings.Contains(s, `"type": "adaptive"`) {
		t.Fatalf("adaptive union must serialize type:adaptive, got %s", s)
	}
	if strings.Contains(s, "budget_tokens") {
		t.Fatalf("adaptive union must not carry budget_tokens, got %s", s)
	}
}
