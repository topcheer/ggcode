package tool

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4052MetricPrefixPreserved(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2GB memory limit under load", "2GB memory limit under load"},
		{"3.5s p99 latency", "3.5s p99 latency"},
		{"-100% coverage", "100% coverage"},
		{"1. tests pass", "tests pass"},
		{"12) no panics", "no panics"},
		{"• all gates green", "all gates green"},
	}
	for _, c := range cases {
		if got := stripListPrefix(c.in); got != c.want {
			t.Errorf("stripListPrefix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIssue4051StringParamRawSubstitution(t *testing.T) {
	src := readFileFor4051(t, "define_tool.go")
	anchor := strings.Index(src, "if s, ok := val.(string); ok {")
	if anchor < 0 {
		t.Fatal("string fast-path branch missing (#4051)")
	}
	window := src[anchor : anchor+800]

	if !strings.Contains(window, "json.Marshal(val)") {
		t.Fatal("Marshal literal path for non-strings must remain (#4051)")
	}
}

func readFileFor4051(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}
