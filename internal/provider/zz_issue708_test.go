package provider

import (
	"testing"
	"time"
)

func TestCalibratorFailureBackoffSuppressesRecalibration(t *testing.T) {
	c := newTokenCountCalibrator()
	if !c.shouldCalibrate() {
		t.Fatal("fresh calibrator should calibrate (first call)")
	}

	c.recordFailure()

	if c.shouldCalibrate() {
		t.Fatal("shouldCalibrate must be false during the backoff window")
	}

	c.mu.Lock()
	c.lastFailure = time.Now().Add(-6 * time.Second)
	c.mu.Unlock()
	if !c.shouldCalibrate() {
		t.Fatal("shouldCalibrate must be true after the backoff window elapses")
	}
}

func TestCalibratorBackoffEscalatesExponentially(t *testing.T) {
	c := newTokenCountCalibrator()

	for i := 1; i <= 3; i++ {
		c.recordFailure()
	}

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

func TestCalibratorSuccessResetsFailureState(t *testing.T) {
	c := newTokenCountCalibrator()
	c.recordFailure()
	c.recordFailure()

	c.applyResult(1000, 1500)

	c.mu.Lock()
	failCount := c.failCount
	lastFailure := c.lastFailure
	c.mu.Unlock()
	if failCount != 0 || !lastFailure.IsZero() {
		t.Fatalf("success must reset failure state, got failCount=%d lastFailure=%v", failCount, lastFailure)
	}

	c.mu.Lock()
	c.lastCalibrate = time.Now().Add(-calibrateInterval - time.Second)
	c.callCount = calibrateMinCalls + 1
	c.mu.Unlock()
	if !c.shouldCalibrate() {
		t.Fatal("no residual backoff may survive a successful calibration")
	}
}

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

func TestIsAnthropicWindowLimitChineseQuotaVeto(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{

		{"would exceed your usage limit for the 5-hour window (limit will reset at 9pm)", true},
		{"your weekly limit will reset on monday", true},

		{"insufficient_quota: your limit will reset at ...", false},

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

func lowercase(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
