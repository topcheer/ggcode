package main

import (
	"testing"
	"time"
)

// #4462: a successfully parsed non-positive duration must fall back to the
// 5m default instead of flowing into time.NewTimer and expiring instantly.
func TestParseA2ATimeoutNonPositive(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 5 * time.Minute},
		{"-5m", 5 * time.Minute},
		{"-1s", 5 * time.Minute},
		{"0s", 5 * time.Minute},
		{"not-a-duration", 5 * time.Minute},
		{"10m", 10 * time.Minute},
		{"30s", 30 * time.Second},
	}
	for _, tc := range cases {
		if got := parseA2ATimeout(tc.in); got != tc.want {
			t.Errorf("parseA2ATimeout(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// #4463: record_effectiveness score must be clamped-rejected outside 1-5
// (usage_tracker scale) so knight statistics cannot be poisoned.
func TestKnightScoreFromParamsBounds(t *testing.T) {
	// in-range values pass (with rounding).
	for _, tc := range []struct {
		params map[string]any
		want   int
	}{
		{nil, 3}, // default
		{map[string]any{"score": float64(1)}, 1},
		{map[string]any{"score": float64(5)}, 5},
		{map[string]any{"score": float64(4.9)}, 5}, // rounded, not truncated to 4
		{map[string]any{"score": float64(1.4)}, 1},
		{map[string]any{"score": "4"}, 3}, // non-float ignored -> default
	} {
		got, err := knightScoreFromParams(tc.params)
		if err != nil {
			t.Errorf("knightScoreFromParams(%v) unexpected error: %v", tc.params, err)
			continue
		}
		if got != tc.want {
			t.Errorf("knightScoreFromParams(%v) = %d, want %d", tc.params, got, tc.want)
		}
	}

	// out-of-range values are rejected.
	for _, bad := range []map[string]any{
		{"score": float64(-100)},
		{"score": float64(0)},
		{"score": float64(6)},
		{"score": 1e9},
	} {
		if _, err := knightScoreFromParams(bad); err == nil {
			t.Errorf("knightScoreFromParams(%v) expected error, got nil", bad)
		}
	}
}
