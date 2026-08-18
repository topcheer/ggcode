package provider

import (
	"testing"
	"time"
)

// TestParseDurationSafeNegativeRejected verifies #664: a bare negative
// millisecond header value (e.g. "-5000") must be rejected instead of
// producing a negative duration that flows into tracker display as a
// misleading value.
func TestParseDurationSafeNegativeRejected(t *testing.T) {
	for _, s := range []string{"-5000", "-1", "0"} {
		if got := parseDurationSafe(s); got != 0 {
			t.Errorf("parseDurationSafe(%q): got %v, want 0 (non-positive rejected)", s, got)
		}
	}
}

// TestParseDurationSafeOverflowClamped verifies #664: a bare ms value large
// enough to overflow the ms→ns multiplication (~9.3e12) used to wrap to a
// negative duration. It must clamp to 24h instead.
func TestParseDurationSafeOverflowClamped(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"9300000000000", 24 * time.Hour},         // 9.3e12 ms — overflow point
		{"9999999999999999", 24 * time.Hour},      // far overflow
		{"86400001", 24 * time.Hour},              // 1ms above cap
		{"86400000", 24 * time.Hour},              // exactly at cap
		{"86399999", 86399999 * time.Millisecond}, // just under cap
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseDurationSafe(tt.input)
			if got != tt.want {
				t.Errorf("parseDurationSafe(%q): got %v, want %v", tt.input, got, tt.want)
			}
			if got < 0 {
				t.Errorf("parseDurationSafe(%q) must never return negative, got %v", tt.input, got)
			}
		})
	}
}
