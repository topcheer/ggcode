package chat

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// simulateTerminalTabExpand mimics how a terminal renders TAB characters:
// advancing the cursor to the next 8-column tab stop per tab (the exact
// behavior width libraries fail to model - they count '\t' as 0-1 columns).
// Used by the #995 invariant test so assertions do NOT share the same
// distorted measurement function as production code.
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

// TestIssue995ErrorBodyTabAndCRNormalized pins the fix: the isError branch
// of RenderBody must run the same CR normalization + TAB expansion as
// FormatBody before wrapping, so tab-indented compiler errors and CRLF
// command output no longer render past the width invariant (scroll desync).
func TestIssue995ErrorBodyTabAndCRNormalized(t *testing.T) {
	// Go compiler error with tab-indicated caret lines, plus a CRLF line.
	raw := "./foo.go:12:3: undefined: x\r\n\tif x != nil {\n\t^\n"
	item := &BaseToolItem{}
	item.SetResult(raw, true)
	item.styles = DefaultStyles()

	const width = 40
	rendered := item.RenderBody(width)

	// Strip ANSI styling to inspect the visible text.
	plain := stripANSI(rendered)
	for _, line := range strings.Split(plain, "\n") {
		if strings.ContainsRune(line, '\t') {
			t.Errorf("raw TAB survived error-body render: %q", line)
		}
		if strings.ContainsRune(line, '\r') {
			t.Errorf("raw CR survived error-body render: %q", line)
		}
		// The decisive invariant: expand tabs the way a TERMINAL would and
		// assert the line still fits. Width libraries (lipgloss.Width /
		// ansi.StringWidth) count '\t' as 0-1 cols - using them here would
		// reproduce the production blind spot this test exists to pin.
		expanded := simulateTerminalTabExpand(line)
		if n := utf8Len(expanded); n > width {
			t.Errorf("terminal-expanded error line = %d cols, exceeds width %d: %q", n, width, expanded)
		}
	}
}

func utf8Len(s string) int { return len([]rune(s)) }

// TestIssue995ErrorBodyStillWraps guards the regression surface: plain
// error output keeps wrapping identically to before the fix.
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

// TestSystemItemTabWidthInvariant pins the #995 tab-expansion contract for
// SystemItem: verification results embed raw build/test output whose fields
// are TAB-separated ("FAIL\tpkg", "ok\tpkg 0.5s"). lipgloss.Width counts '\t'
// as 0 columns, so without expansion those lines measure as fitting the
// viewport but the terminal advances to the next tab stop and wraps them -
// one more displayed line than Height() counted, corrupting the viewport
// during verification (ghost blank rows, lost panel border).
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
