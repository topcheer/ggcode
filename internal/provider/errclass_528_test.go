package provider

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestClassifyAnthropicFiveHourWindowLimitNotQuota (#528): Anthropic's 429
// rate_limit_error message ("This request would exceed your usage limit for
// the 5-hour window. Your limit will reset at ...") contains the substring
// "usage limit", which previously classified it as permanent quota — a single
// rate-limit hit then triggered sticky failover for the rest of the session.
// The window auto-resets within hours, so it must be FailureRateLimit.
func TestClassifyAnthropicFiveHourWindowLimitNotQuota(t *testing.T) {
	errs := []error{
		errors.New(`rate_limit_error - This request would exceed your usage limit for the 5-hour window. Your limit will reset at 2026-01-01T12:00:00Z. Please try again then`),
		errors.New(`429 {"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed your usage limit for the 5-hour window. Your limit will reset at 5pm"}`),
		// Weekly window variant.
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

// TestMiniMaxUsageLimitStillQuota (#528): MiniMax's genuine permanent form
// ("usage limit exceeded, 5-hour usage limit reached") carries none of the
// Anthropic window markers and must still classify as quota.
func TestMiniMaxUsageLimitStillQuota(t *testing.T) {
	err := errors.New("usage limit exceeded, 5-hour usage limit reached")
	if !IsQuotaExhaustedError(err) {
		t.Error("IsQuotaExhaustedError(usage limit + billing cycle) = false, want true")
	}
	if got := ClassifyLLMError(err); got != FailureQuota {
		t.Errorf("ClassifyLLMError = %v, want FailureQuota", got)
	}
}

// TestClassifyDeadlineExceededNotCounted (#528, sibling of #304): client-side
// timeouts (context.DeadlineExceeded — long-turn deadline, user re-sending)
// say nothing about provider health and must not count toward the
// consecutive-failure failover threshold, which only exempts FailureNone.
func TestClassifyDeadlineExceededNotCounted(t *testing.T) {
	if got := ClassifyLLMError(context.DeadlineExceeded); got != FailureNone {
		t.Errorf("ClassifyLLMError(context.DeadlineExceeded) = %v, want FailureNone", got)
	}
	// Wrapped deadline (the form the agent loop actually sees).
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	wrapped := fmt.Errorf("stream request failed: %w", ctx.Err())
	if got := ClassifyLLMError(wrapped); got != FailureNone {
		t.Errorf("ClassifyLLMError(wrapped DeadlineExceeded) = %v, want FailureNone", got)
	}
	// A server-reported deadline string (not a client context error) stays
	// transient — only errors.Is(context.DeadlineExceeded) is exempt.
	if got := ClassifyLLMError(errors.New("server-side processing deadline exceeded")); got == FailureNone {
		t.Error("plain string deadline should not be FailureNone")
	}
}
