package config

import (
	"strings"
	"testing"
)

func TestAnnotateVisionFlags(t *testing.T) {
	in := []string{"gpt-4o", "deepseek-chat", "glm-5.3-flash", "glm-5.3"}
	out := annotateVisionFlags(in)

	if out[0] != "gpt-4o [vision]" {
		t.Errorf("gpt-4o should be marked vision, got %q", out[0])
	}
	if out[1] != "deepseek-chat" {
		t.Errorf("deepseek-chat should not be marked vision, got %q", out[1])
	}
	// Vision via exact capability-table match, without a -v/-vl naming hint.
	if out[2] != "glm-5.3-flash [vision]" {
		t.Errorf("glm-5.3-flash should be marked vision from capability table, got %q", out[2])
	}
	if out[3] != "glm-5.3" {
		t.Errorf("glm-5.3 should not be marked vision, got %q", out[3])
	}
}

func TestBuildSystemPrompt_ModelListHasVisionFlags(t *testing.T) {
	prompt := BuildSystemPrompt("", "/tmp", "en", nil, "", nil, []string{"gpt-4o", "deepseek-chat"})
	if !strings.Contains(prompt, "gpt-4o [vision]") {
		t.Error("system prompt should mark gpt-4o with [vision]")
	}
	if !strings.Contains(prompt, "accept image input") {
		t.Error("system prompt should explain the [vision] marker")
	}
	// The non-vision model must not gain the marker.
	if strings.Contains(prompt, "deepseek-chat [vision]") {
		t.Error("deepseek-chat must not be marked [vision]")
	}
}

func TestModelCapabilityExports(t *testing.T) {
	if !ModelSupportsVision("glm-5.3-flash") {
		t.Error("ModelSupportsVision(glm-5.3-flash) = false, want true")
	}
	if ModelSupportsVision("glm-5.3") {
		t.Error("ModelSupportsVision(glm-5.3) = true, want false")
	}
	if w := ModelContextWindow("glm-5.3-flash"); w != 1000000 {
		t.Errorf("ModelContextWindow(glm-5.3-flash) = %d, want 1000000", w)
	}
}
