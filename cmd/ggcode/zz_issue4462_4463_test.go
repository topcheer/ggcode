package main

import (
	"testing"
	"time"
)

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

func TestKnightScoreFromParamsBounds(t *testing.T) {

	for _, tc := range []struct {
		params map[string]any
		want   int
	}{
		{nil, 3},
		{map[string]any{"score": float64(1)}, 1},
		{map[string]any{"score": float64(5)}, 5},
		{map[string]any{"score": float64(4.9)}, 5},
		{map[string]any{"score": float64(1.4)}, 1},
		{map[string]any{"score": "4"}, 3},
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
