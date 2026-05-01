package harness

import (
	"testing"
)

// TestDecideRouteWithFeatures_LLMClassifierOverrides tests that the LLM
// classifier can override a low-score input to RouteHarness.
func TestDecideRouteWithFeatures_LLMClassifierOverrides(t *testing.T) {
	prov := &mockClassifierProvider{
		response: `{"classification": "code_change", "confidence": 0.9, "complexity": "complex", "reason": "bug fix"}`,
	}

	features := ExtractFeatures("the login page shows a 500 error when the session expires")
	ctx := RouteContext{
		LLMClassifierProvider: prov,
	}

	decision := DecideRouteWithFeatures("the login page shows a 500 error when the session expires", "on", features, ctx)
	if decision != RouteHarness {
		t.Errorf("decision = %v, want RouteHarness (LLM override)", decision)
	}
}

// TestDecideRouteWithFeatures_ScoreVeryHigh_SkipsLLM tests that score >= 5
// routes directly without calling the LLM classifier.
func TestDecideRouteWithFeatures_ScoreVeryHigh_SkipsLLM(t *testing.T) {
	// This provider returns "conversation" — if LLM were called, it would
	// downgrade to RouteNormal. But score >= 5 should skip LLM entirely.
	prov := &mockClassifierProvider{
		response: `{"classification": "conversation", "confidence": 0.9, "reason": "test"}`,
	}

	// Need score >= 5: action verb (+2) + file path (+2) + code block (+1) + task goal (+1) = 6
	input := "fix the auth bug in auth.go\n```\nfunction authenticate() {}\n```\nimplement JWT validation"
	features := ExtractFeatures(input)
	ctx := RouteContext{
		LLMClassifierProvider: prov,
	}

	decision := DecideRouteWithFeatures(input, "on", features, ctx)
	if decision != RouteHarness {
		t.Errorf("decision = %v, want RouteHarness (score >= 5, LLM skipped)", decision)
	}
}

// TestDecideRouteWithFeatures_Score3or4_GoesThroughLLM tests that score 3-4
// inputs go through the LLM classifier for complexity filtering.
// Note: task goal indicators like "in ", "the ", "a " each add +1, so inputs
// that contain these may score higher than expected. Use inputs carefully.
func TestDecideRouteWithFeatures_Score3or4_GoesThroughLLM(t *testing.T) {
	// "fix the typo" — action verb (+2) + "the " task goal (+1) = score 3
	// No file path. LLM says simple → RouteNormal.
	prov := &mockClassifierProvider{
		response: `{"classification": "code_change", "confidence": 0.95, "complexity": "simple", "reason": "typo fix"}`,
	}

	features := ExtractFeatures("fix the typo")
	ctx := RouteContext{
		LLMClassifierProvider: prov,
	}

	decision := DecideRouteWithFeatures("fix the typo", "on", features, ctx)
	if decision != RouteNormal {
		t.Errorf("decision = %v, want RouteNormal (simple change → main agent)", decision)
	}
}

// TestDecideRouteWithFeatures_Score3or4_ComplexGoesToHarness tests that
// score 3-4 inputs that the LLM classifies as complex go to harness.
func TestDecideRouteWithFeatures_Score3or4_ComplexGoesToHarness(t *testing.T) {
	// "fix the authentication module" — action verb (+2) + "the " task goal (+1) = score 3
	// No file path. LLM says complex → RouteHarness.
	prov := &mockClassifierProvider{
		response: `{"classification": "code_change", "confidence": 0.92, "complexity": "complex", "reason": "bug investigation needed"}`,
	}

	features := ExtractFeatures("fix the authentication module")
	ctx := RouteContext{
		LLMClassifierProvider: prov,
	}

	decision := DecideRouteWithFeatures("fix the authentication module", "on", features, ctx)
	if decision != RouteHarness {
		t.Errorf("decision = %v, want RouteHarness (complex code change)", decision)
	}
}

// TestDecideRouteWithFeatures_SkippedWhenNoProvider tests that no LLM call
// happens when provider is nil — falls back to deterministic only.
func TestDecideRouteWithFeatures_SkippedWhenNoProvider(t *testing.T) {
	features := ExtractFeatures("the login page is broken")
	ctx := RouteContext{
		LLMClassifierProvider: nil,
	}

	decision := DecideRouteWithFeatures("the login page is broken", "on", features, ctx)
	if decision != RouteNormal {
		t.Errorf("decision = %v, want RouteNormal (no provider)", decision)
	}
}

// TestDecideRouteWithFeatures_SkippedForSuggestMode tests that LLM is
// not called in suggest mode.
func TestDecideRouteWithFeatures_SkippedForSuggestMode(t *testing.T) {
	prov := &mockClassifierProvider{
		response: `{"classification": "code_change", "confidence": 0.9, "complexity": "complex", "reason": "test"}`,
	}

	features := ExtractFeatures("the login page is broken")
	ctx := RouteContext{
		LLMClassifierProvider: prov,
	}

	decision := DecideRouteWithFeatures("the login page is broken", "suggest", features, ctx)
	if decision != RouteNormal {
		t.Errorf("decision = %v, want RouteNormal (suggest mode skips LLM)", decision)
	}
}
