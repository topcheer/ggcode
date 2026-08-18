package im

import (
	"math"
	"testing"
	"time"
)

// TestMatrixRetryAfterClamp verifies #664: the server-controlled
// retry_after_ms value must be clamped to a sane maximum BEFORE the
// float64→Duration multiplication. Values above ~9.22e12 ms (or +Inf) used
// to wrap to a large negative Duration, making time.After(negative) fire
// immediately and bypass the homeserver's backoff signal.
func TestMatrixRetryAfterClamp(t *testing.T) {
	tests := []struct {
		name string
		ms   float64
		want time.Duration
	}{
		{"normal value passes through", 250, 250 * time.Millisecond},
		{"small value", 1, time.Millisecond},
		{"exactly at cap", float64(matrixRetryAfterMaxMS), time.Duration(matrixRetryAfterMaxMS) * time.Millisecond},
		{"just above cap clamps", float64(matrixRetryAfterMaxMS) + 1, 24 * time.Hour},
		{"overflow-sized value clamps (was negative wrap)", 9.3e12, 24 * time.Hour},
		{"far overflow clamps", 1e18, 24 * time.Hour},
		{"+Inf clamps (was negative wrap)", math.Inf(1), 24 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matrixRetryAfter(tt.ms)
			if got != tt.want {
				t.Errorf("matrixRetryAfter(%v): got %v, want %v", tt.ms, got, tt.want)
			}
			if got <= 0 {
				t.Errorf("matrixRetryAfter(%v) must never be non-positive (would zero out backoff): got %v", tt.ms, got)
			}
		})
	}
}

// TestMatrixRetryAfterOverflowRegression is the concrete regression from the
// issue: the raw conversion of 9.3e12 ms wraps negative, while the clamped
// helper returns a positive, bounded duration.
func TestMatrixRetryAfterOverflowRegression(t *testing.T) {
	// Use a variable so the multiplication happens at runtime (a constant
	// expression overflows at compile time, which is itself the bug signal).
	ms := float64(9.3e12)
	raw := time.Duration(ms) * time.Millisecond
	if raw > 0 {
		t.Fatalf("precondition failed: raw conversion of 9.3e12ms should wrap negative on this platform, got %v", raw)
	}
	got := matrixRetryAfter(ms)
	if got != 24*time.Hour {
		t.Errorf("clamped duration: got %v, want 24h", got)
	}
}
