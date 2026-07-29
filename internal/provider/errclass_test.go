package provider

import (
	"context"
	"errors"
	"testing"
)

func TestClassifyLLMError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want FailureClass
	}{
		{"nil", nil, FailureNone},
		{"canceled", context.Canceled, FailureNone},
		// Quota takes precedence over rate-limit keywords in the same message.
		{"quota with 429", errors.New("429: exceeded your current quota"), FailureQuota},
		{"quota coding plan", errors.New("rate limit: coding plan usage limit reached"), FailureQuota},
		{"quota chinese", errors.New("错误：余额不足，请充值"), FailureQuota},
		{"quota fair usage", errors.New("access_terminated due to fair usage policy"), FailureQuota},
		// Auth.
		{"auth 401", errors.New("401 unauthorized"), FailureAuth},
		{"auth invalid key", errors.New("invalid api key provided"), FailureAuth},
		// Rate limit (transient — no quota keywords).
		{"rate limit plain", errors.New("rate limit exceeded, retry after 30s"), FailureRateLimit},
		{"rate limit 429", errors.New("HTTP 429 too many requests"), FailureRateLimit},
		{"overloaded", errors.New("anthropic API overloaded"), FailureRateLimit},
		// Network.
		{"network eof", errors.New("unexpected EOF"), FailureNetwork},
		{"network dns", errors.New("dial tcp: no such host"), FailureNetwork},
		{"network timeout", errors.New("i/o timeout"), FailureNetwork},
		// Transient / unknown.
		{"server 503", errors.New("503 service unavailable"), FailureTransient},
		{"unknown", errors.New("something weird happened"), FailureTransient},
		{"context overflow", errors.New("context length exceeded"), FailureTransient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyLLMError(tt.err); got != tt.want {
				t.Errorf("ClassifyLLMError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestFailureClassString(t *testing.T) {
	if FailureNone.String() != "" {
		t.Error("FailureNone should stringify to empty")
	}
	if FailureQuota.String() != "quota" || FailureRateLimit.String() != "rate_limited" || FailureAuth.String() != "auth" {
		t.Error("unexpected status strings")
	}
}

// TestIsQuotaExhaustedErrorConsistency guards the shared keyword list used by
// both the retry path (provider) and the agent retry path.
func TestIsQuotaExhaustedErrorConsistency(t *testing.T) {
	quotaErrs := []error{
		errors.New("coding plan usage limit"),
		errors.New("insufficient balance"),
		errors.New("配额耗尽"),
	}
	for _, err := range quotaErrs {
		if !IsQuotaExhaustedError(err) {
			t.Errorf("IsQuotaExhaustedError(%v) = false, want true", err)
		}
		if ClassifyLLMError(err) != FailureQuota {
			t.Errorf("ClassifyLLMError(%v) != FailureQuota", err)
		}
	}
	if IsQuotaExhaustedError(errors.New("429 too many requests")) {
		t.Error("plain 429 should not be classified as quota exhaustion")
	}
}
