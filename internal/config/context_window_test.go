package config

import (
	"strings"
	"testing"
)

// #5141 Face A: this table used to hand-pin ~30 exact window values for
// external models. The table is regenerated WEEKLY from models.dev, so
// every upstream revision redded the pin - a hand-frozen copy of live
// external data cannot stay green by construction. Now:
//   - sampled rows assert self-consistency (infer == table value)
//   - hand-pinned SHAPE cases that don't depend on upstream values
//   - a plausibility guard over the whole table (catches sync corruption)
func TestInferContextWindow_KnownModels(t *testing.T) {
	// Self-consistency samples: the inference path must read the synced
	// table exactly (case-insensitive exact key, so mixed-case input too).
	samples := []string{
		"gpt-4.1", "gpt-4o-mini", "o3",
		"claude-sonnet-4-6", "claude-opus-4-5-20251101",
		"gemini-2.5-pro", "deepseek-ai/deepseek-v3",
		"glm-5", "glm-5.1", "kimi-k2",
		"moonshotai/kimi-k2", "deepseek-ai/deepseek-r1",
	}
	for _, model := range samples {
		want, ok := knownModelCapabilities[strings.ToLower(model)]
		if !ok {
			t.Fatalf("sample %q missing from table (renamed upstream?) - update the sample list", model)
		}
		if got := inferContextWindow(model, "openai"); got != want.ContextWindow {
			t.Errorf("inferContextWindow(%q) = %d, table says %d - inference no longer reads the synced table", model, got, want.ContextWindow)
		}
	}

	// SHAPE pins: these do not depend on any specific upstream value.
	// Unknown model falls through to the protocol default...
	if got := inferContextWindow("unknown-model", "openai"); got != 128000 {
		t.Errorf("unknown openai fallback = %d, want 128000", got)
	}
	// ...while a region-suffixed synced row still resolves.
	if _, ok := knownModelCapabilities["glm-5.2@eu"]; ok {
		if got := inferContextWindow("glm-5.2@eu", "openai"); got != knownModelCapabilities["glm-5.2@eu"].ContextWindow {
			t.Errorf("region-suffixed row must resolve via the table, got %d", got)
		}
	}
}

// TestInferContextWindow_TablePlausibility guards the whole synced table:
// catches upstream corruption or a bad regen (zero, negative, or absurd
// windows) without freezing any exact external value.
func TestInferContextWindow_TablePlausibility(t *testing.T) {
	for model, cap := range knownModelCapabilities {
		if cap.ContextWindow <= 0 {
			t.Errorf("table row %q has non-positive window %d", model, cap.ContextWindow)
		}
		if cap.ContextWindow > 10_000_000 {
			t.Errorf("table row %q has implausible window %d (>10M)", model, cap.ContextWindow)
		}
	}
}

func TestInferContextWindow_ProtocolFallback(t *testing.T) {
	if got := inferContextWindow("unknown", "anthropic"); got != 200000 {
		t.Errorf("anthropic fallback = %d, want 200000", got)
	}
	if got := inferContextWindow("unknown", "gemini"); got != 1_000_000 {
		t.Errorf("gemini fallback = %d, want 1000000", got)
	}
	if got := inferContextWindow("unknown", "openai"); got != 128000 {
		t.Errorf("openai fallback = %d, want 128000", got)
	}
}

func TestInferContextWindow_HintParsing(t *testing.T) {

	if got := inferContextWindow("my-model-256k", "openai"); got != 256000 {
		t.Errorf("hint 256k = %d, want 256000", got)
	}
	if got := inferContextWindow("model-1m-turbo", "openai"); got != 1_000_000 {
		t.Errorf("hint 1m = %d, want 1000000", got)
	}
	if got := inferContextWindow("model-128k-pro", "openai"); got != 128000 {
		t.Errorf("hint 128k = %d, want 128000", got)
	}
}

func TestInferVisionSupport_XiaoMiMIMO(t *testing.T) {
	if !inferVisionSupport("MiMo-V2.5", "openai") {
		t.Fatal("expected MiMo-V2.5 to infer vision support")
	}
	if !inferVisionSupport("MiMo-V2-Omni", "openai") {
		t.Fatal("expected MiMo-V2-Omni to infer vision support")
	}
	if inferVisionSupport("MiMo-V2.5-Pro", "openai") {
		t.Fatal("expected MiMo-V2.5-Pro to remain non-vision by default")
	}
}

func TestInferVisionSupportCapabilityTableOnly(t *testing.T) {
	cases := []struct {
		model    string
		protocol string
		want     bool
	}{

		{"glm-5.3-flash", "openai", true},
		{"claude-3-5-haiku", "anthropic", true},
		{"deepseek-chat", "openai", false},
		{"glm-5.3", "openai", false},

		{"my-claude-proxy", "openai", false},
		{"custom-gpt-gateway", "openai", false},
		{"weird-glm-vision-thing", "openai", false},

		{"whatever-model", "gemini", true},
	}
	for _, tc := range cases {
		if got := inferVisionSupport(tc.model, tc.protocol); got != tc.want {
			t.Errorf("inferVisionSupport(%q, %q) = %v, want %v", tc.model, tc.protocol, got, tc.want)
		}
	}
}
