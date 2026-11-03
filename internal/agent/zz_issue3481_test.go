package agent

import (
	"strings"
	"testing"
)

func TestIssue3481_CdPrefixedVerifyCommands(t *testing.T) {
	positive := []string{
		"cd /repo && make verify-ci",
		"cd /repo\nmake verify-ci",
		"cd /repo && npm run test",
		"cd app && go test ./...",
		"cd /repo && make check",
		"cd /repo && ./gradlew check",
	}
	for _, cmd := range positive {
		if !psIsVerifyCommand(cmd) {
			t.Errorf("cd-prefixed verify command must be recognized: %q", cmd)
		}
	}
	negative := []string{
		"cd /repo && make clean",
		"cd /repo && npm run dev",
		"cd /repo && echo done",
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
		{"make test", []string{"make", "test"}},
		{"cd", nil},
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
