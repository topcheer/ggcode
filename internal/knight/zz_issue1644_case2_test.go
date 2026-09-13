package knight

// #1644 case 2 regression: the scheduler trust gates route through the
// trustCanWrite whitelist (staged|auto) instead of the fail-open
// '!EqualFold(TrustLevel, "readonly")'. A typo'd trust_level ("read-only",
// "HIGH") must read as NOT writable - matching what the audit panel
// (auto_policy) already displays - so display and behavior never split.
// This pins the whitelist primitive itself (stable since #1604-E); the
// scheduler wrapper normalizes (ToLower+TrimSpace) before calling it.

import (
	"strings"
	"testing"
)

func TestSchedulerCanWriteWhitelist(t *testing.T) {
	// Mirrors Knight.canWrite's normalization then the whitelist call.
	canWrite := func(trust string) bool {
		return trustCanWrite(strings.ToLower(strings.TrimSpace(trust)))
	}
	cases := []struct {
		trust  string
		want   bool
		reason string
	}{
		{"staged", true, "whitelisted"},
		{"auto", true, "whitelisted"},
		{"readonly", false, "explicit read-only"},
		{"read-only", false, "typo'd value must fail closed (#1644-2 core)"},
		{"HIGH", false, "unrecognized value must fail closed"},
		{"", false, "empty must fail closed"},
		{"  Staged  ", true, "wrapper normalizes case+whitespace"},
		{"READONLY", false, "case-normalized read-only stays read-only"},
	}
	for _, c := range cases {
		if got := canWrite(c.trust); got != c.want {
			t.Fatalf("canWrite(%q) = %v, want %v (%s)", c.trust, got, c.want, c.reason)
		}
	}
}
