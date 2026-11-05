package provider

import (
	"errors"
	"testing"
)

// Captured live 2026-11-05 from zai cn-coding-openai (glm-5.3):
//
//	HTTP 429 {"error":{"code":"1308","message":"已达到 5 小时的使用上限。您的限额将在 2026-11-05 18:58:07 重置。"}}
//
// This is a RECOVERABLE plan-window limit: it must classify as rate_limited so
// the agent retries the call in place every 5 minutes until the window resets,
// instead of failing the turn as a hard quota.
func TestIssueRateLimitGLMWindowLimitIsRecoverable(t *testing.T) {
	real := errors.New(`429 {"error":{"code":"1308","message":"已达到 5 小时的使用上限。您的限额将在 2026-11-05 18:58:07 重置。"}}`)
	if got := ClassifyLLMError(real); got != FailureRateLimit {
		t.Fatalf("GLM 5-hour window limit classified as %v, want FailureRateLimit (recoverable, retry in place)", got)
	}
}

// The reset timestamp wording alone (no 5-hour phrase) must also be recovered.
func TestIssueRateLimitGLMResetWording(t *testing.T) {
	for _, s := range []string{
		"您的限额将在 2026-11-05 18:58:07 重置",
		"已达到小时使用上限，请稍后再试",
	} {
		if got := ClassifyLLMError(errors.New(s)); got != FailureRateLimit {
			t.Errorf("%q classified as %v, want FailureRateLimit", s, got)
		}
	}
}

// A genuinely dead plan/balance must still fail fast - blind retry there would
// loop forever.
func TestIssueRateLimitGLMHardQuotaStillFailsFast(t *testing.T) {
	for _, s := range []string{
		"您的余额不足，请充值后重试",
		"账户欠费，服务已停止",
		"套餐已到期，请续费",
		"额度已用完",
	} {
		if got := ClassifyLLMError(errors.New(s)); got != FailureQuota {
			t.Errorf("%q classified as %v, want FailureQuota (hard, fail-fast)", s, got)
		}
	}
}

// The generic Chinese "使用上限" marker without reset semantics keeps its prior
// classification (hard quota) - we only promoted the window-limit forms.
func TestIssueRateLimitGenericUsageCeilingUnchanged(t *testing.T) {
	// "配额超限" has no reset/window wording -> stays quota.
	if got := ClassifyLLMError(errors.New("配额超限")); got != FailureQuota {
		t.Errorf("bare 配额超限 = %v, want FailureQuota (behavior preserved)", got)
	}
}
