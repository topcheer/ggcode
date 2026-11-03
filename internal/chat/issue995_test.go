package chat

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func simulateTerminalTabExpand(s string) string {
	var b strings.Builder
	col := 0
	for _, r := range s {
		switch r {
		case '\t':
			next := (col/8 + 1) * 8
			for i := col; i < next; i++ {
				b.WriteByte(' ')
			}
			col = next
		case '\n':
			b.WriteByte('\n')
			col = 0
		default:
			b.WriteRune(r)
			col++
		}
	}
	return b.String()
}

func TestIssue995ErrorBodyTabAndCRNormalized(t *testing.T) {

	raw := "./foo.go:12:3: undefined: x\r\n\tif x != nil {\n\t^\n"
	item := &BaseToolItem{}
	item.SetResult(raw, true)
	item.styles = DefaultStyles()

	const width = 40
	rendered := item.RenderBody(width)

	plain := stripANSI(rendered)
	for _, line := range strings.Split(plain, "\n") {
		if strings.ContainsRune(line, '\t') {
			t.Errorf("raw TAB survived error-body render: %q", line)
		}
		if strings.ContainsRune(line, '\r') {
			t.Errorf("raw CR survived error-body render: %q", line)
		}

		expanded := simulateTerminalTabExpand(line)
		if n := utf8Len(expanded); n > width {
			t.Errorf("terminal-expanded error line = %d cols, exceeds width %d: %q", n, width, expanded)
		}
	}
}

func utf8Len(s string) int { return len([]rune(s)) }

func TestIssue995ErrorBodyStillWraps(t *testing.T) {
	item := &BaseToolItem{}
	item.SetResult(strings.Repeat("x", 100), true)
	item.styles = DefaultStyles()
	rendered := item.RenderBody(40)
	plain := stripANSI(rendered)
	for _, line := range strings.Split(plain, "\n") {
		if utf8Len(line) > 40 {
			t.Errorf("plain error line not wrapped: %d cols", utf8Len(line))
		}
	}
}

func TestSystemItemTabWidthInvariant(t *testing.T) {
	st := DefaultStyles()
	msgs := []string{
		"Running verification: `make verify-ci`…",
		"❌ [Verification failed: `go test ./...`]\n```\nFAIL\tgithub.com/topcheer/ggcode/internal/agent [build failed]\nok\tgithub.com/topcheer/ggcode/internal/chat\t0.6s\n```",
		"✅ [Verification passed: `go build ./...`]",
	}
	for _, msg := range msgs {
		for _, w := range []int{30, 40, 60, 80, 120} {
			it := NewSystemItem("id", msg, st)
			r := it.Render(w)
			h := it.Height(w)
			phys := len(splitVisualLines(r))
			if h != phys {
				t.Errorf("width=%d: Height=%d != physical=%d", w, h, phys)
			}
			for i, ln := range strings.Split(r, "\n") {
				if vw := lipgloss.Width(ln); vw > w {
					shown := ln
					if len(shown) > 50 {
						shown = shown[:50]
					}
					t.Errorf("width=%d line %d overflows: %d > %d: %q", w, i, vw, w, shown)
				}
			}
		}
	}
}

func TestWrapLinesChokePointNormalization(t *testing.T) {
	cases := []struct {
		name string
		in   string
		w    int
	}{
		{"bare CR becomes newline", "progress 50%\rprogress 99%", 40},
		{"CRLF normalized", "line one\r\nline two", 40},
		{"tab expanded", "FAIL\tgithub.com/x/y", 20},
		{"tabs in code", "func main() {\n\treturn\n}", 20},
		{"mixed", "ok\tpkg\t0.5s\rFAIL\tpkg2", 30},
	}
	for _, c := range cases {
		got := wrapLines(c.in, c.w)
		for i, ln := range got {
			if strings.ContainsRune(ln, '\t') {
				t.Errorf("%s: line %d still contains TAB: %q", c.name, i, ln)
			}
			if strings.ContainsRune(ln, '\r') {
				t.Errorf("%s: line %d still contains CR: %q", c.name, i, ln)
			}
			if vw := lipgloss.Width(ln); vw > c.w {
				t.Errorf("%s: line %d width %d > %d: %q", c.name, i, vw, c.w, ln)
			}
		}
	}
}

func TestUserItemTabAndCRInvariant(t *testing.T) {
	st := DefaultStyles()
	texts := []string{
		"func main() {\n\tif true {\n\t\tfmt.Println(\"hi\")\n\t}\n}",
		"npm output\rdone\r100%",
		"M\tinternal/chat/messages.go\n??\tnew.go",
	}
	for _, txt := range texts {
		for _, w := range []int{30, 40, 60, 80} {
			it := NewUserItem("id", txt, st)
			r := it.Render(w)
			if h, phys := it.Height(w), len(splitVisualLines(r)); h != phys {
				t.Errorf("width=%d: Height=%d != physical=%d", w, h, phys)
			}
			for i, ln := range strings.Split(r, "\n") {
				if vw := lipgloss.Width(ln); vw > w {
					shown := ln
					if len(shown) > 50 {
						shown = shown[:50]
					}
					t.Errorf("width=%d line %d overflows: %d > %d: %q", w, i, vw, w, shown)
				}
			}
		}
	}
}
