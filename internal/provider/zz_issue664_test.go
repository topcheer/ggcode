package provider

import (
	"testing"
	"time"
)

func TestParseDurationSafeNegativeRejected(t *testing.T) {
	for _, s := range []string{"-5000", "-1", "0"} {
		if got := parseDurationSafe(s); got != 0 {
			t.Errorf("parseDurationSafe(%q): got %v, want 0 (non-positive rejected)", s, got)
		}
	}
}

func TestParseDurationSafeOverflowClamped(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"9300000000000", 24 * time.Hour},
		{"9999999999999999", 24 * time.Hour},
		{"86400001", 24 * time.Hour},
		{"86400000", 24 * time.Hour},
		{"86399999", 86399999 * time.Millisecond},
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
