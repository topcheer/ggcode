package agent

// #3481 probe: a cd-prefixed build-system verify command (`cd /repo &&
// make verify-ci`, or the multi-line flattened form) left `cd` as
// tokens[0], so psBuildSystemVerify never dispatched on the real command
// and hyphen/underscore variants never reached segment-first position.

import (
	"strings"
	"testing"
)

func TestIssue3481_CdPrefixedVerifyCommands(t *testing.T) {
	positive := []string{
		"cd /repo && make verify-ci",  // the issue's headline case
		"cd /repo\nmake verify-ci",    // multi-line flattened
		"cd /repo && npm run test",    // npm dispatcher via runner
		"cd app && go test ./...",     // cd + runner still recognized
		"cd /repo && make check",      // whitelisted target
		"cd /repo && ./gradlew check", // gradle wrapper
	}
	for _, cmd := range positive {
		if !psIsVerifyCommand(cmd) {
			t.Errorf("cd-prefixed verify command must be recognized: %q", cmd)
		}
	}
	negative := []string{
		"cd /repo && make clean",  // hygiene target is not verification
		"cd /repo && npm run dev", // service target is not verification
		"cd /repo && echo done",   // not a verify command at all
	}
	for _, cmd := range negative {
		if psIsVerifyCommand(cmd) {
			t.Errorf("cd-prefixed non-verify command must stay unrecognized: %q", cmd)
		}
	}
}

func TestIssue3481_StripCdPrefixShapes(t *testing.T) {
	cases := []struct {
		in  string
		out []string
	}{
		{"cd /repo && make verify-ci", []string{"make", "verify-ci"}},
		{"cd /repo\nmake test", []string{"make", "test"}},
		{"cd .. && cd sub && go build ./...", []string{"go", "build", "./..."}},
		{"make test", []string{"make", "test"}}, // no cd: untouched
		{"cd", nil},                             // bare cd: everything consumed
	}
	for _, tc := range cases {
		got := psStripCdPrefix(strings.Fields(tc.in))
		if len(got) != len(tc.out) {
			t.Errorf("strip(%q) = %v, want %v", tc.in, got, tc.out)
			continue
		}
		for i := range got {
			if got[i] != tc.out[i] {
				t.Errorf("strip(%q) = %v, want %v", tc.in, got, tc.out)
				break
			}
		}
	}
}
