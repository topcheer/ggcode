package provider

import (
	"errors"
	"fmt"
	"testing"
)

func TestUserFacingError_Nil(t *testing.T) {
	result := UserFacingError(nil)
	if result != "" {
		t.Errorf("expected empty for nil, got %q", result)
	}
}

func TestUserFacingError_RateLimit(t *testing.T) {
	err := errors.New("rate limit exceeded")
	result := UserFacingError(err)
	if result == "" {
		t.Error("expected non-empty for rate limit error")
	}
}

func TestUserFacingError_AuthError(t *testing.T) {
	err := errors.New("invalid api key")
	result := UserFacingError(err)
	if result == "" {
		t.Error("expected non-empty for auth error")
	}
}

func TestUserFacingError_GenericError(t *testing.T) {
	err := errors.New("something unexpected")
	result := UserFacingError(err)
	// Should return something, even if just the original message
	t.Logf("UserFacingError(generic) = %q", result)
}

func TestUserFacingError_WrappedError(t *testing.T) {
	inner := errors.New("connection refused")
	err := fmt.Errorf("provider call failed: %w", inner)
	result := UserFacingError(err)
	t.Logf("UserFacingError(wrapped) = %q", result)
}

func TestImageBlock(t *testing.T) {
	block := ImageBlock("image/png", "base64data")
	if block.Type != "image" {
		t.Errorf("type = %q, want %q", block.Type, "image")
	}
	if block.ImageMIME != "image/png" {
		t.Errorf("mime = %q", block.ImageMIME)
	}
}

func TestTextBlock(t *testing.T) {
	block := TextBlock("hello")
	if block.Type != "text" {
		t.Errorf("type = %q", block.Type)
	}
	if block.Text != "hello" {
		t.Errorf("text = %q", block.Text)
	}
}

func TestToolUseBlock(t *testing.T) {
	block := ToolUseBlock("id-1", "bash", []byte(`{"command":"ls"}`))
	if block.Type != "tool_use" {
		t.Errorf("type = %q", block.Type)
	}
	if block.ToolID != "id-1" {
		t.Errorf("tool_use_id = %q", block.ToolID)
	}
	if block.ToolName != "bash" {
		t.Errorf("name = %q", block.ToolName)
	}
}

func TestToolResultBlock(t *testing.T) {
	block := ToolResultBlock("id-1", "output text", false)
	if block.Type != "tool_result" {
		t.Errorf("type = %q", block.Type)
	}
	if block.ToolID != "id-1" {
		t.Errorf("tool_use_id = %q", block.ToolID)
	}
}

func TestToolResultNamedBlock(t *testing.T) {
	block := ToolResultNamedBlock("id-1", "bash", "output", false)
	if block.ToolName != "bash" {
		t.Errorf("name = %q", block.ToolName)
	}
}

func TestToolResultWithImages(t *testing.T) {
	images := []ContentImage{
		{MIME: "image/png", Base64: "base64img"},
	}
	block := ToolResultWithImages("id-1", "tool", "screenshot", images, false)
	if block.Type != "tool_result" {
		t.Error("expected tool_result type")
	}
}

func TestDefaultImpersonationPresets(t *testing.T) {
	presets := DefaultImpersonationPresets()
	if len(presets) == 0 {
		t.Error("expected non-empty presets")
	}
	// Each preset should have an ID and name
	for _, p := range presets {
		if p.ID == "" {
			t.Error("preset missing ID")
		}
	}
}

func TestFindPresetByID(t *testing.T) {
	presets := DefaultImpersonationPresets()
	if len(presets) == 0 {
		t.Skip("no presets")
	}
	found := FindPresetByID(presets[0].ID)
	if found == nil {
		t.Error("expected to find preset")
	}
	if found.ID != presets[0].ID {
		t.Errorf("found wrong preset: %q", found.ID)
	}

	// Not found
	missing := FindPresetByID("nonexistent-preset-id-xyz")
	if missing != nil {
		t.Error("expected nil for unknown ID")
	}
}

func TestSetActiveAndGetImpersonation(t *testing.T) {
	presets := DefaultImpersonationPresets()
	if len(presets) == 0 {
		t.Skip("no presets")
	}

	SetActiveImpersonation(&presets[0], "1.0", nil)
	gotPreset, gotVersion, gotHeaders := GetActiveImpersonation()
	if gotPreset == nil {
		t.Fatal("expected active preset")
	}
	if gotVersion != "1.0" {
		t.Errorf("version = %q, want %q", gotVersion, "1.0")
	}
	if gotHeaders != nil {
		t.Errorf("expected nil headers, got %v", gotHeaders)
	}

	// Clear
	SetActiveImpersonation(nil, "", nil)
	gotPreset, _, _ = GetActiveImpersonation()
	if gotPreset != nil {
		t.Error("expected nil after clear")
	}
}

func TestResolveImpersonationHeaders(t *testing.T) {
	// No active impersonation
	headers := ResolveImpersonationHeaders()
	t.Logf("headers without impersonation: %v", headers)

	// With active impersonation
	presets := DefaultImpersonationPresets()
	if len(presets) > 0 {
		SetActiveImpersonation(&presets[0], "1.0", map[string]string{"X-Custom": "test"})
		headers = ResolveImpersonationHeaders()
		if headers == nil {
			t.Error("expected non-nil headers with active impersonation")
		}
		SetActiveImpersonation(nil, "", nil)
	}
}

func TestDefaultHeadersForProtocol(t *testing.T) {
	tests := []string{"anthropic", "openai", "gemini", "unknown"}
	for _, proto := range tests {
		headers := DefaultHeadersForProtocol(proto)
		t.Logf("protocol=%s headers=%v", proto, headers)
	}
}

func TestAdaptiveCapFor(t *testing.T) {
	cap := AdaptiveCapFor("anthropic", "", "claude-3.5-sonnet", 0)
	if cap == nil {
		t.Fatal("expected non-nil cap")
	}
	t.Logf("cap for claude-3.5-sonnet: %v", cap)
}

func TestIsRetryable_DefaultTrue(t *testing.T) {
	// isRetryable defaults to true for unknown errors
	if !isRetryable(errors.New("unknown error")) {
		t.Error("expected retryable for unknown errors (default=true)")
	}
	// nil is not retryable
	if isRetryable(nil) {
		t.Error("expected not retryable for nil")
	}
	// 401 is not retryable (matches " 401 " pattern)
	if isRetryable(errors.New("error 401 unauthorized")) {
		t.Error("expected not retryable for 401")
	}
	// 500 is retryable
	if !isRetryable(errors.New("status: 500")) {
		t.Error("expected retryable for 500")
	}
}
