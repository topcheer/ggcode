package tui

// R201: padPanelLine's blanket control-char expansion replaced the ESC byte
// of legitimate lipgloss SGR sequences (panel rows are rendered through
// lipgloss BEFORE padding - summaries are SGR 90, the cursor row 1;94), so
// the /sessions panel showed "[90m ... [m" as literal text on every row.
// The expansion must copy escape sequences through verbatim while still
// expanding real control chars (TAB/CR/0x7f - the #1014 concern).

import (
	"strings"
	"testing"
)

func TestPadPanelLinePreservesSGRSequences(t *testing.T) {
	styled := "\x1b[90m  20260714-200440-27b7cb75f\x1b[m"
	got := padPanelLine(styled, 60)
	if !strings.Contains(got, "\x1b[90m") {
		t.Fatalf("SGR sequence must survive padding, got %q", got)
	}
	if !strings.Contains(got, "\x1b[m") {
		t.Fatalf("reset sequence must survive intact, got %q", got)
	}
	// No literal residue: "[90m" must only ever appear preceded by ESC.
	if strings.Contains(strings.ReplaceAll(got, "\x1b[90m", ""), "[90m") {
		t.Fatalf("literal ANSI residue detected: %q", got)
	}
}

func TestPadPanelLinePreservesBoldCursorSequence(t *testing.T) {
	// The cursor row is Bold + Color("12") = SGR 1;94 - the exact sequence
	// visible as "[1;94m" in the broken panel.
	styled := "\x1b[1;94m› title\x1b[0m"
	got := padPanelLine(styled, 40)
	if !strings.Contains(got, "\x1b[1;94m") {
		t.Fatalf("bold SGR sequence must survive, got %q", got)
	}
}

func TestPadPanelLineStillExpandsRealControlChars(t *testing.T) {
	// #1014 must keep working: TAB/CR/0x7f expand to spaces.
	got := padPanelLine("a\tb\rc\x7fd", 20)
	if strings.ContainsAny(got, "\t\r\x7f") {
		t.Fatalf("control chars must still expand, got %q", got)
	}
	if !strings.Contains(got, "a b c d") {
		t.Fatalf("expanded text wrong: %q", got)
	}
}

func TestPadPanelLineANSIWidthPadding(t *testing.T) {
	// The ESC sequence is zero-width: padding must reach the visible width.
	styled := "\x1b[90mab\x1b[m"
	got := padPanelLine(styled, 6)
	if !strings.Contains(got, "\x1b[90m") {
		t.Fatalf("sequence must survive, got %q", got)
	}
	// 2 visible chars + 4 padding spaces.
	plain := strings.ReplaceAll(strings.ReplaceAll(got, "\x1b[90m", ""), "\x1b[m", "")
	if len(plain) != 6 || !strings.HasSuffix(plain, "    ") {
		t.Fatalf("padding must be based on visible width, got %q (plain=%q)", got, plain)
	}
}

func TestExpandControlCharsANSIAwareEdgeCases(t *testing.T) {
	// Lone trailing ESC expands to a space.
	if got := expandControlCharsANSIAware("ab\x1b"); got != "ab " {
		t.Fatalf("lone ESC must expand, got %q", got)
	}
	// Unterminated CSI copies to end without mangling.
	if got := expandControlCharsANSIAware("a\x1b[9"); got != "a\x1b[9" {
		t.Fatalf("unterminated CSI must copy verbatim, got %q", got)
	}
	// OSC terminated by BEL survives.
	if got := expandControlCharsANSIAware("\x1b]0;title\x07ab"); got != "\x1b]0;title\x07ab" {
		t.Fatalf("OSC must survive, got %q", got)
	}
}
