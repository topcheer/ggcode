package provider

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestClassifyAnthropicFiveHourWindowLimitNotQuota(t *testing.T) {
	errs := []error{
		errors.New(`rate_limit_error - This request would exceed your usage limit for the 5-hour window. Your limit will reset at 2026-01-01T12:00:00Z. Please try again then`),
		errors.New(`429 {"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed your usage limit for the 5-hour window. Your limit will reset at 5pm"}`),

		errors.New(`rate_limit_error: This request would exceed your weekly limit. Your limit will reset on Monday`),
	}
	for _, err := range errs {
		if got := ClassifyLLMError(err); got != FailureRateLimit {
			t.Errorf("ClassifyLLMError(%v) = %v, want FailureRateLimit (recoverable window limit)", err, got)
		}
		if IsQuotaExhaustedError(err) {
			t.Errorf("IsQuotaExhaustedError(%v) = true, want false — window limits auto-reset", err)
		}
	}
}

func TestMiniMaxUsageLimitStillQuota(t *testing.T) {
	err := errors.New("usage limit exceeded, 5-hour usage limit reached")
	if !IsQuotaExhaustedError(err) {
		t.Error("IsQuotaExhaustedError(usage limit + billing cycle) = false, want true")
	}
	if got := ClassifyLLMError(err); got != FailureQuota {
		t.Errorf("ClassifyLLMError = %v, want FailureQuota", got)
	}
}

func TestClassifyDeadlineExceededNotCounted(t *testing.T) {
	if got := ClassifyLLMError(context.DeadlineExceeded); got != FailureNone {
		t.Errorf("ClassifyLLMError(context.DeadlineExceeded) = %v, want FailureNone", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	wrapped := fmt.Errorf("stream request failed: %w", ctx.Err())
	if got := ClassifyLLMError(wrapped); got != FailureNone {
		t.Errorf("ClassifyLLMError(wrapped DeadlineExceeded) = %v, want FailureNone", got)
	}

	if got := ClassifyLLMError(errors.New("server-side processing deadline exceeded")); got == FailureNone {
		t.Error("plain string deadline should not be FailureNone")
	}
}
