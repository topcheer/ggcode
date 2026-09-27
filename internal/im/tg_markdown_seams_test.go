package im

import (
	"strings"
	"testing"
)

// Pins for the r183 escapeLine decomposition (behavior-preserving seams).

func TestEmitCodeSpan_ClosedSpanVerbatim(t *testing.T) {
	var b strings.Builder
	got := emitCodeSpan(&b, "a`code`b", 1)
	if b.String() != "`code`" {
		t.Fatalf("closed span must be emitted verbatim, got %q", b.String())
	}
	if got != 7 {
		t.Fatalf("next index after closed span = %d, want 7", got)
	}
}

func TestEmitCodeSpan_UnterminatedEscapesBacktick(t *testing.T) {
	var b strings.Builder
	got := emitCodeSpan(&b, "a`abc", 1)
	if b.String() != "\\`" {
		t.Fatalf("unterminated backtick must be escaped, got %q", b.String())
	}
	if got != 2 {
		t.Fatalf("next index = %d, want 2", got)
	}
}

func TestTryEmitDelimited_MatchedPair(t *testing.T) {
	var b strings.Builder
	got := tryEmitDelimited(&b, "**hi.**", 0, '*', "**")
	if b.String() != "**hi\\.**" {
		t.Fatalf("delimiter unescaped + content escaped, got %q", b.String())
	}
	if got != 7 {
		t.Fatalf("next index = %d, want 7", got)
	}
}

func TestTryEmitDelimited_UnmatchedFallsThrough(t *testing.T) {
	var b strings.Builder
	if got := tryEmitDelimited(&b, "*abc", 0, '*', "**"); got != -1 || b.Len() != 0 {
		t.Fatalf("single opener must fall through untouched, got %d %q", got, b.String())
	}
	// Backtick boundary guard: closing pair never found across a backtick.
	if got := tryEmitDelimited(&b, "**a`bc", 0, '*', "**"); got != -1 || b.Len() != 0 {
		t.Fatalf("backtick boundary must fall through, got %d %q", got, b.String())
	}
}

func TestTryEmitLink_BalancedParensURL(t *testing.T) {
	// #1246: parenthesized URL must survive intact with ')' escaped.
	var b strings.Builder
	got := tryEmitLink(&b, "[a](u(v))", 0, "")
	if b.String() != "[a](u(v\\))" {
		t.Fatalf("balanced-paren URL escaping, got %q", b.String())
	}
	if got != len("[a](u(v))") {
		t.Fatalf("next index = %d, want full length", got)
	}
}

func TestTryEmitLink_ImagePrefixEscapesAlt(t *testing.T) {
	var b strings.Builder
	if got := tryEmitLink(&b, "![x*y](u)", 1, "!"); got < 0 || b.String() != "![x\\*y](u)" {
		t.Fatalf("image structure unescaped + alt escaped, got %d %q", got, b.String())
	}
}

func TestTryEmitLink_MalformedFallsThrough(t *testing.T) {
	var b strings.Builder
	if got := tryEmitLink(&b, "[a](no close", 0, ""); got != -1 || b.Len() != 0 {
		t.Fatalf("unclosed link must fall through, got %d %q", got, b.String())
	}
	if got := tryEmitLink(&b, "[a] text", 0, ""); got != -1 || b.Len() != 0 {
		t.Fatalf("non-link bracket must fall through, got %d %q", got, b.String())
	}
}

func TestEscapeLine_MixedLinePin(t *testing.T) {
	var b strings.Builder
	escapeLine(&b, "**b** `c` [t](u(v)) ~~s~~ __i__ x.y")
	want := "**b** `c` [t](u(v\\)) ~~s~~ __i__ x\\.y"
	if b.String() != want {
		t.Fatalf("mixed line pin:\n got %q\nwant %q", b.String(), want)
	}
}
