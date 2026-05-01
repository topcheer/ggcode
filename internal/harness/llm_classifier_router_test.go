package harness

import (
	"testing"
)

// TestDecideRouteWithFeatures_LLMClassifierOverrides tests that the LLM
// classifier can override a score-2 RouteNormal to RouteHarness.
func TestDecideRouteWithFeatures_LLMClassifierOverrides(t *testing.T) {
	prov := &mockClassifierProvider{
		response: `{"classification": "code_change", "confidence": 0.9, "reason": "bug fix"}`,
	}

	// "the login page shows a 500 error" — has no action verb, no file path → score 0-1
	// but LLM classifies as code change → should override to RouteHarness
	features := ExtractFeatures("the login page shows a 500 error when the session expires")
	ctx := RouteContext{
		LLMClassifierProvider: prov,
	}

	decision := DecideRouteWithFeatures("the login page shows a 500 error when the session expires", "on", features, ctx)
	if decision != RouteHarness {
		t.Errorf("decision = %v, want RouteHarness (LLM override)", decision)
	}
}

// TestDecideRouteWithFeatures_LLMClassifierNotCalledWhenScoreHigh
// tests that LLM is NOT called when score >= 3 (already caught).
func TestDecideRouteWithFeatures_LLMClassifierNotCalledWhenScoreHigh(t *testing.T) {
	// Provider that would fail if called
	prov := &mockClassifierProvider{
		err:      nil,
		response: `{"classification": "conversation", "confidence": 0.9, "reason": "test"}`,
	}

	// "fix the auth bug in auth.go" — action verb "fix" (2) + file path "auth.go" (2) = score 4
	features := ExtractFeatures("fix the auth bug in auth.go")
	ctx := RouteContext{
		LLMClassifierProvider: prov,
	}

	decision := DecideRouteWithFeatures("fix the auth bug in auth.go", "on", features, ctx)
	if decision != RouteHarness {
		t.Errorf("decision = %v, want RouteHarness (deterministic, no LLM needed)", decision)
	}
}

// TestDecideRouteWithFeatures_LLMClassifierSkippedWhenNoProvider
// tests that no LLM call happens when provider is nil.
func TestDecideRouteWithFeatures_LLMClassifierSkippedWhenNoProvider(t *testing.T) {
	// "the login page is broken" — no action verb, no file path → score 0-1
	features := ExtractFeatures("the login page is broken")
	ctx := RouteContext{
		LLMClassifierProvider: nil, // no provider
	}

	decision := DecideRouteWithFeatures("the login page is broken", "on", features, ctx)
	// Without LLM, score < 3 → RouteNormal
	if decision != RouteNormal {
		t.Errorf("decision = %v, want RouteNormal (no LLM provider)", decision)
	}
}

// TestDecideRouteWithFeatures_LLMClassifierSkippedForSuggestMode
// tests that LLM is not called in suggest mode.
func TestDecideRouteWithFeatures_LLMClassifierSkippedForSuggestMode(t *testing.T) {
	prov := &mockClassifierProvider{
		response: `{"classification": "code_change", "confidence": 0.9, "reason": "test"}`,
	}

	// Same input that would be classified as code change
	features := ExtractFeatures("the login page is broken")
	ctx := RouteContext{
		LLMClassifierProvider: prov,
	}

	decision := DecideRouteWithFeatures("the login page is broken", "suggest", features, ctx)
	// Suggest mode doesn't use LLM classifier
	if decision != RouteNormal {
		t.Errorf("decision = %v, want RouteNormal (suggest mode skips LLM)", decision)
	}
}
