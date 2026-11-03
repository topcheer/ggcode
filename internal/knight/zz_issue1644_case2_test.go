package knight

import (
	"strings"
	"testing"
)

func TestSchedulerCanWriteWhitelist(t *testing.T) {

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
