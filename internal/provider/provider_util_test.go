package provider

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sashabaranov/go-openai"
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

func TestUserFacingError_KimiAccessTerminated(t *testing.T) {
	// Scenario 1: Original *openai.APIError with HTTP status chain intact
	apiErr := &openai.APIError{
		HTTPStatusCode: 403,
		Message:        "You've reached your usage limit for this billing cycle. Your quota will be refreshed in the next cycle. To continue now, purchase extra usage or upgrade your plan: https://www.kimi.com/code/#pricing",
	}
	result := UserFacingErrorLang(apiErr, "en")
	if !strings.Contains(strings.ToLower(result), "quota") {
		t.Errorf("expected 'quota' in English result, got %q", result)
	}

	resultZh := UserFacingErrorLang(apiErr, "zh-CN")
	if !strings.Contains(resultZh, "额度") {
		t.Errorf("expected '额度' in Chinese result, got %q", resultZh)
	}

	// Scenario 2: Error chain destroyed — agent converts to FriendlyError string,
	// then TUI calls UserFacingErrorLang on the string-only error
	stringErr := fmt.Errorf("%s", FriendlyError(apiErr))
	result2 := UserFacingErrorLang(stringErr, "en")
	if !strings.Contains(strings.ToLower(result2), "quota") {
		t.Errorf("expected 'quota' in string-only error result, got %q", result2)
	}

	// Scenario 3: Raw Kimi error string without HTTP status (simulating
	// stream mid-error where SDK may not wrap into APIError)
	rawErr := errors.New("access_terminated_error: usage limit reached for billing cycle")
	result3 := UserFacingErrorLang(rawErr, "en")
	if !strings.Contains(strings.ToLower(result3), "quota") {
		t.Errorf("expected 'quota' for raw access_terminated string, got %q", result3)
	}
}

func TestUserFacingError_ZAICodingPlan(t *testing.T) {
	// ZAI/GLM coding plan returns 429 with business code 1308 (5h limit)
	t.Run("1308_5h_limit", func(t *testing.T) {
		err := &openai.APIError{
			HTTPStatusCode: 429,
			Message:        "已达到 5 hour 的使用上限。您的限额将在 2025-01-01 05:00:00 重置",
		}
		result := UserFacingErrorLang(err, "zh-CN")
		if !strings.Contains(result, "额度") {
			t.Errorf("expected '额度' for ZAI 429 quota, got %q", result)
		}
	})

	// ZAI code 1309: coding plan expired
	t.Run("1309_plan_expired", func(t *testing.T) {
		err := &openai.APIError{
			HTTPStatusCode: 429,
			Message:        "您的 GLM Coding Plan 套餐已到期，暂无法使用",
		}
		result := UserFacingErrorLang(err, "zh-CN")
		if !strings.Contains(result, "额度") && !strings.Contains(result, "过期") {
			t.Errorf("expected '额度/过期' for expired plan, got %q", result)
		}
	})

	// ZAI code 1113: insufficient balance
	t.Run("1113_insufficient_balance", func(t *testing.T) {
		err := &openai.APIError{
			HTTPStatusCode: 429,
			Message:        "您的账户已欠费，请充值后重试",
		}
		result := UserFacingErrorLang(err, "zh-CN")
		if !strings.Contains(result, "额度") && !strings.Contains(result, "充值") {
			t.Errorf("expected '额度/充值' for arrears, got %q", result)
		}
	})

	// ZAI English variant
	t.Run("english_coding_plan", func(t *testing.T) {
		err := &openai.APIError{
			HTTPStatusCode: 429,
			Message:        "Usage limit reached for 5 hour. Your limit will reset at 2025-01-01 05:00:00",
		}
		result := UserFacingErrorLang(err, "en")
		if !strings.Contains(strings.ToLower(result), "quota") {
			t.Errorf("expected 'quota' for ZAI English, got %q", result)
		}
	})

	// Standard transient rate limit should NOT be treated as quota
	t.Run("transient_rate_limit", func(t *testing.T) {
		err := &openai.APIError{
			HTTPStatusCode: 429,
			Message:        "Rate limit reached for requests",
		}
		result := UserFacingErrorLang(err, "en")
		if strings.Contains(strings.ToLower(result), "quota exhausted") {
			t.Errorf("transient 429 should not say 'quota exhausted', got %q", result)
		}
	})
}

func TestIsQuotaExhaustedError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"kimi_access_terminated", &openai.APIError{HTTPStatusCode: 403, Message: "access_terminated_error"}, true},
		{"zai_5h_limit", &openai.APIError{HTTPStatusCode: 429, Message: "已达到 5 hour 的使用上限"}, true},
		{"zai_plan_expired", &openai.APIError{HTTPStatusCode: 429, Message: "GLM Coding Plan package has expired"}, true},
		{"zai_insufficient_balance", &openai.APIError{HTTPStatusCode: 429, Message: "insufficient balance"}, true},
		{"openai_quota", &openai.APIError{HTTPStatusCode: 429, Message: "You exceeded your current quota"}, true},
		{"zai_arrears_cn", &openai.APIError{HTTPStatusCode: 429, Message: "您的账户已欠费"}, true},
		{"zai_fair_usage", &openai.APIError{HTTPStatusCode: 429, Message: "公平使用策略"}, true},
		{"aliyun_allocated_quota", &openai.APIError{HTTPStatusCode: 429, Message: "Allocated quota exceeded"}, true},
		{"ark_quota_exceeded", &openai.APIError{HTTPStatusCode: 429, Message: "QuotaExceeded: The specified quota has been exceeded"}, true},
		{"minimax_usage_limit", &openai.APIError{HTTPStatusCode: 429, Message: "usage limit exceeded, 5-hour usage limit reached"}, true},
		{"mimo_quota_cn", &openai.APIError{HTTPStatusCode: 429, Message: "Token Plan 的额度耗尽"}, true},
		{"mimo_quota_exceed_cn", &openai.APIError{HTTPStatusCode: 429, Message: "配额超限"}, true},
		{"transient_rate_limit", &openai.APIError{HTTPStatusCode: 429, Message: "Rate limit reached for requests"}, false},
		{"normal_error", errors.New("something went wrong"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isQuotaExhaustedError(tt.err); got != tt.want {
				t.Errorf("isQuotaExhaustedError() = %v, want %v", got, tt.want)
			}
		})
	}
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
