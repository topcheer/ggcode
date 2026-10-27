package main

import (
	"testing"
	"time"
)

// zz_issue4462_test.go - companion test for #4462.
// parseA2ATimeout accepted negative durations (time.ParseDuration("-5m")
// parses fine); every handler-side guard only covered timeout == 0, so a
// negative value reached time.NewTimer, fired instantly, and 100% of A2A
// tasks timed out (-32060) with no diagnostic pointing at the config.
// Non-positive must normalize to the 5m default, same shape as
// parseA2AMaxTasks (#1422-A).
func TestIssue4462ParseA2ATimeoutNonPositive(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 5 * time.Minute},      // unset -> default
		{"5m", 5 * time.Minute},    // valid passthrough
		{"90s", 90 * time.Second},  // valid non-default
		{"-5m", 5 * time.Minute},   // #4462: negative -> default
		{"-1s", 5 * time.Minute},   // #4462: any negative -> default
		{"0s", 5 * time.Minute},    // #4462: zero -> default (was: 0 passed through as "no timeout" config intent, but consumers already fell back on ==0; normalize here keeps one source of truth)
		{"90sec", 5 * time.Minute}, // #4397: unparsable -> default
		{"5", 5 * time.Minute},     // #4397: unitless -> default
	}
	for _, c := range cases {
		if got := parseA2ATimeout(c.in); got != c.want {
			t.Errorf("parseA2ATimeout(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
