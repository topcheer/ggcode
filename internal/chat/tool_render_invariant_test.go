package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func assertNoLineExceedsWidth(t *testing.T, rendered string, width int, ctx string) {
	t.Helper()
	for i, line := range strings.Split(rendered, "\n") {
		if w := ansi.StringWidth(line); w > width {
			t.Fatalf("%s: line %d visual width %d exceeds width %d: %q", ctx, i, w, width, truncateForLog(line))
		}
	}
}

func truncateForLog(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

func TestFormatBodyCRNormalization(t *testing.T) {

	crlf := FormatBodyPlain("alpha\r\nbeta\r\ngamma", 60, 0)
	lf := FormatBodyPlain("alpha\nbeta\ngamma", 60, 0)
	if crlf != lf {
		t.Fatalf("CRLF not normalized to LF:\n got %q\nwant %q", crlf, lf)
	}
	if strings.ContainsRune(crlf, '\r') {
		t.Fatalf("stray CR survived normalization: %q", crlf)
	}

	cr := FormatBodyPlain("downloading 10%\rdownloading 99%", 60, 0)
	if strings.ContainsRune(cr, '\r') {
		t.Fatalf("bare CR survived normalization: %q", cr)
	}
	if !strings.Contains(cr, "downloading 99%") {
		t.Fatalf("CR-rewritten tail missing: %q", cr)
	}
}

func TestFormatBodyTabExpansion(t *testing.T) {

	out := FormatBodyPlain("a\tb\tc", 60, 0)
	if strings.ContainsRune(out, '\t') {
		t.Fatalf("tab survived expansion: %q", out)
	}

	out = FormatBodyPlain("M\tinternal/chat/styles.go\n??\tzz.go", 60, 0)
	if strings.ContainsRune(out, '\t') {
		t.Fatalf("tabs survived expansion: %q", out)
	}

	if got := expandTabs("a\tb"); got != "a   b" {
		t.Fatalf("expandTabs col tracking wrong: %q", got)
	}
	if got := expandTabs("abcd\te"); got != "abcd    e" {
		t.Fatalf("expandTabs stop boundary wrong: %q", got)
	}

}

func TestFormatBodyWideLinesInvariant(t *testing.T) {
	cases := []struct {
		name  string
		input string
		width int
	}{
		{"ascii long", strings.Repeat("x", 500), 40},
		{"no spaces", strings.Repeat("aB!", 300), 37},
		{"wide runes", strings.Repeat("世界你好", 200), 41},
		{"single huge token", strings.Repeat("W", 2000), 20},
		{"mixed with newlines", "short\n" + strings.Repeat("m", 300) + "\ntail", 33},
		{"space-run", strings.Repeat(" ", 400), 25},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := FormatBody(tc.input, tc.width, 0)
			assertNoLineExceedsWidth(t, out, tc.width, "FormatBody("+tc.name+")")
			if strings.ContainsRune(out, '\r') {
				t.Fatalf("%s: stray CR in output", tc.name)
			}
		})
	}
}

func FormatBodyPlain(s string, width, maxLines int) string {
	out, _ := FormatBody(s, width, maxLines)
	return out
}

func TestToolHeaderNarrowWidthInvariant(t *testing.T) {
	styles := DefaultStyles()

	for _, width := range []int{12, 16, 20, 24, 40} {
		for _, params := range []string{
			"go build -tags goolm ./internal/... && go test ./internal/chat/ -run TestSomethingVeryLong",
			strings.Repeat("p", 300),
			"",
		} {
			h := styles.ToolHeader(StatusRunning, "run_command", width, params)
			assertNoLineExceedsWidth(t, h, width, "ToolHeader(width="+itoa(width)+")")
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestBashItemStreamedRenderInvariant(t *testing.T) {
	styles := DefaultStyles()
	item := NewBashToolItem("t1", "run_command", "", StatusRunning, styles)
	item.SetStreamingBody("")
	for _, chunk := range []string{
		"go test ./...\r\nok  pkg1  1.2s\r\nok  pkg2  3.4s\r\n",
		strings.Repeat("progress \r", 5) + "done\n",
		strings.Repeat("compiler-error-without-spaces-", 40) + "\n",
		strings.Repeat("世", 300),
	} {
		item.SetStreamingBody(chunk)
		for _, width := range []int{20, 48, 80, 120} {
			rendered := item.Render(width)
			assertNoLineExceedsWidth(t, rendered, width, "BashToolItem streaming render")
			if strings.ContainsRune(rendered, '\r') {
				t.Fatalf("streaming render leaked CR at width %d: %q", width, truncateForLog(rendered))
			}
		}
	}
}
