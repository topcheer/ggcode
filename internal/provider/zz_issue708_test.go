package provider

import (
	"testing"
	"time"
)

// TestCalibratorFailureBackoffSuppressesRecalibration (#708): after a failed
// calibration attempt, shouldCalibrate must return false for the duration of
// the exponential backoff window so CountTokens serves the local estimate
// without a remote call (previously a persistently 429/5xx count_tokens
// endpoint turned every call into a synchronous 2s-blocked remote attempt
// competing for the same rate-limit quota).
func TestCalibratorFailureBackoffSuppressesRecalibration(t *testing.T) {
	c := newTokenCountCalibrator()
	if !c.shouldCalibrate() {
		t.Fatal("fresh calibrator should calibrate (first call)")
	}

	c.recordFailure() // failCount=1, backoff 5s

	if c.shouldCalibrate() {
		t.Fatal("shouldCalibrate must be false during the backoff window")
	}

	// Simulate the backoff window elapsing: 6s > 5s base backoff.
	c.mu.Lock()
	c.lastFailure = time.Now().Add(-6 * time.Second)
	c.mu.Unlock()
	if !c.shouldCalibrate() {
		t.Fatal("shouldCalibrate must be true after the backoff window elapses")
	}
}

// TestCalibratorBackoffEscalatesExponentially (#708): consecutive failures
// double the backoff (5s, 10s, 20s, 40s, capped at 60s).
func TestCalibratorBackoffEscalatesExponentially(t *testing.T) {
	c := newTokenCountCalibrator()

	for i := 1; i <= 3; i++ {
		c.recordFailure()
	}
	// failCount=3 → backoff = 5s<<2 = 20s.
	c.mu.Lock()
	c.lastFailure = time.Now().Add(-10 * time.Second)
	inBackoff := c.shouldCalibrate()
	c.mu.Unlock()
	if inBackoff {
		t.Fatal("after 3 consecutive failures, 10s must still be inside the 20s backoff")
	}

	c.mu.Lock()
	c.lastFailure = time.Now().Add(-25 * time.Second)
	escaped := c.shouldCalibrate()
	c.mu.Unlock()
	if !escaped {
		t.Fatal("25s must be outside the 20s backoff window")
	}
}

// TestCalibratorDisablesAfterMaxConsecutiveFailures (#708): after
// calibrateMaxConsecutiveFailures consecutive failures the calibrator
// disables permanently and falls back to the raw local estimate.
func TestCalibratorDisablesAfterMaxConsecutiveFailures(t *testing.T) {
	c := newTokenCountCalibrator()
	for i := 0; i < calibrateMaxConsecutiveFailures; i++ {
		c.recordFailure()
	}
	if c.shouldCalibrate() {
		t.Fatal("calibrator must be disabled after max consecutive failures")
	}
	c.mu.Lock()
	ratio := c.ratio
	enabled := c.enabled
	c.mu.Unlock()
	if enabled {
		t.Fatal("enabled must be false after max consecutive failures")
	}
	if ratio != 1.0 {
		t.Fatalf("disabled calibrator ratio = %f, want 1.0 (raw local estimate)", ratio)
	}
}

// TestCalibratorSuccessResetsFailureState (#708): a successful calibration
// clears the failure memory so the next window follows the normal interval.
func TestCalibratorSuccessResetsFailureState(t *testing.T) {
	c := newTokenCountCalibrator()
	c.recordFailure()
	c.recordFailure()

	c.applyResult(1000, 1500) // success

	c.mu.Lock()
	failCount := c.failCount
	lastFailure := c.lastFailure
	c.mu.Unlock()
	if failCount != 0 || !lastFailure.IsZero() {
		t.Fatalf("success must reset failure state, got failCount=%d lastFailure=%v", failCount, lastFailure)
	}

	// After a success no residual backoff may survive: with the interval and
	// min-calls gates satisfied, the next call must calibrate again.
	c.mu.Lock()
	c.lastCalibrate = time.Now().Add(-calibrateInterval - time.Second)
	c.callCount = calibrateMinCalls + 1 // force past the min-calls gate
	c.mu.Unlock()
	if !c.shouldCalibrate() {
		t.Fatal("no residual backoff may survive a successful calibration")
	}
}

// TestCalibratorFailureFewerThanMaxKeepsEnabled (#708): a failure run shorter
// than the disable threshold must NOT permanently disable the calibrator —
// transient 429 windows recover, unlike the permanent 404/403 disable path.
func TestCalibratorFailureFewerThanMaxKeepsEnabled(t *testing.T) {
	c := newTokenCountCalibrator()
	for i := 0; i < calibrateMaxConsecutiveFailures-1; i++ {
		c.recordFailure()
	}
	c.mu.Lock()
	enabled := c.enabled
	c.mu.Unlock()
	if !enabled {
		t.Fatal("calibrator must remain enabled below the consecutive-failure threshold")
	}
}

// TestIsAnthropicWindowLimitChineseQuotaVeto (#708 folded LOW): a
// non-Anthropic vendor emitting an Anthropic-style message with "limit will
// reset" plus strong Chinese quota wording must NOT be classified as a
// recoverable window limit — it is a permanent quota failure.
func TestIsAnthropicWindowLimitChineseQuotaVeto(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		// Genuine Anthropic recoverable window — still a window limit.
		{"would exceed your usage limit for the 5-hour window (limit will reset at 9pm)", true},
		{"your weekly limit will reset on monday", true},
		// Non-Anthropic English quota codes — already vetoed.
		{"insufficient_quota: your limit will reset at ...", false},
		// Non-Anthropic vendor with Anthropic-style phrasing + Chinese quota
		// wording — must be vetoed (quota, not window limit).
		{"rate limit exceeded: limit will reset at 2026-01-01, 使用上限已达", false},
		{"429: your limit will reset soon; 配额超限，请升级套餐", false},
		{"limit will reset; 额度已用完", false},
	}
	for _, tc := range cases {
		if got := isAnthropicWindowLimit(lowercase(tc.msg)); got != tc.want {
			t.Errorf("isAnthropicWindowLimit(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

// lowercase mirrors how callers normalize error strings before classification.
func lowercase(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
