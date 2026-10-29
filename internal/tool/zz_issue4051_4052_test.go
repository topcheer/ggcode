package tool

import (
	"os"
	"strings"
	"testing"
)

// #4052: appendCriterion's TrimLeft cutset included all digits and dots, so
// metric-shaped acceptance lines lost their numeric prefix ("2GB memory
// limit" -> "GB memory limit", "3.5s p99" -> "s p99"). stripListPrefix
// keeps them and still strips real list indices.
func TestIssue4052MetricPrefixPreserved(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2GB memory limit under load", "2GB memory limit under load"},
		{"3.5s p99 latency", "3.5s p99 latency"},
		{"-100% coverage", "100% coverage"},      // bullet, no space
		{"1. tests pass", "tests pass"},          // real index
		{"12) no panics", "no panics"},           // paren index
		{"• all gates green", "all gates green"}, // bullet+space
	}
	for _, c := range cases {
		if got := stripListPrefix(c.in); got != c.want {
			t.Errorf("stripListPrefix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// #4051: string params substitute their raw value; the old Marshal+trim
// left inner JSON escapes as literal two-byte sequences so quoted patterns
// matched nothing.
func TestIssue4051StringParamRawSubstitution(t *testing.T) {
	src := readFileFor4051(t, "define_tool.go")
	anchor := strings.Index(src, "#4051: string params substitute their RAW value")
	if anchor < 0 {
		t.Fatal("raw-string substitution branch missing (#4051)")
	}
	window := src[anchor : anchor+800]
	if !strings.Contains(window, "if s, ok := val.(string); ok {") {
		t.Fatal("string fast-path branch missing (#4051)")
	}
	// Marshal path must still exist for numbers/bools.
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
