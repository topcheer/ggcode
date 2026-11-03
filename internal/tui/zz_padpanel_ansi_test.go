package tui

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

	if strings.Contains(strings.ReplaceAll(got, "\x1b[90m", ""), "[90m") {
		t.Fatalf("literal ANSI residue detected: %q", got)
	}
}

func TestPadPanelLinePreservesBoldCursorSequence(t *testing.T) {

	styled := "\x1b[1;94m› title\x1b[0m"
	got := padPanelLine(styled, 40)
	if !strings.Contains(got, "\x1b[1;94m") {
		t.Fatalf("bold SGR sequence must survive, got %q", got)
	}
}

func TestPadPanelLineStillExpandsRealControlChars(t *testing.T) {

	got := padPanelLine("a\tb\rc\x7fd", 20)
	if strings.ContainsAny(got, "\t\r\x7f") {
		t.Fatalf("control chars must still expand, got %q", got)
	}
	if !strings.Contains(got, "a b c d") {
		t.Fatalf("expanded text wrong: %q", got)
	}
}

func TestPadPanelLineANSIWidthPadding(t *testing.T) {

	styled := "\x1b[90mab\x1b[m"
	got := padPanelLine(styled, 6)
	if !strings.Contains(got, "\x1b[90m") {
		t.Fatalf("sequence must survive, got %q", got)
	}

	plain := strings.ReplaceAll(strings.ReplaceAll(got, "\x1b[90m", ""), "\x1b[m", "")
	if len(plain) != 6 || !strings.HasSuffix(plain, "    ") {
		t.Fatalf("padding must be based on visible width, got %q (plain=%q)", got, plain)
	}
}

func TestExpandControlCharsANSIAwareEdgeCases(t *testing.T) {

	if got := expandControlCharsANSIAware("ab\x1b"); got != "ab " {
		t.Fatalf("lone ESC must expand, got %q", got)
	}

	if got := expandControlCharsANSIAware("a\x1b[9"); got != "a\x1b[9" {
		t.Fatalf("unterminated CSI must copy verbatim, got %q", got)
	}

	if got := expandControlCharsANSIAware("\x1b]0;title\x07ab"); got != "\x1b]0;title\x07ab" {
		t.Fatalf("OSC must survive, got %q", got)
	}
}
