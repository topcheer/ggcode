package tool

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestIssue4880_RenderRefTreeRuneSafeTruncation(t *testing.T) {
	longName := strings.Repeat("界", 80)
	entries := []browserRefEntry{{Ref: "@e1", Role: "button", Name: longName}}
	out := renderRefTree(entries)
	for _, line := range strings.Split(out, "\n") {
		if !utf8.ValidString(line) {
			t.Fatalf("rendered line is not valid UTF-8 (split rune): %q", line)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "…") {
		t.Fatalf("truncated line must end with the ellipsis rune:\n%s", out)
	}
}
