package provider

import "testing"

// Regression for #1610-C: custom headers survive an empty preset.
func TestBuildHeadersCustomOnlyWithoutPreset(t *testing.T) {
	SetActiveImpersonation(nil, "", map[string]string{"X-Api-Version": "2"})
	defer SetActiveImpersonation(nil, "", nil)
	h := BuildHeadersForProvider("anthropic")
	if h == nil || h.Get("X-Api-Version") != "2" {
		t.Fatalf("custom header must apply without a preset, got %v", h)
	}
}
