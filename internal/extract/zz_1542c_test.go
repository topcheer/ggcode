package extract

import (
	"strings"
	"testing"
)

// #1542-C pins:
//   - rtf: an invalid hex escape (\' followed by non-hex) must only consume
//     the escape prefix + one char, never the two bytes after the quote.
//   - svg: textPath and anchor text are visible text, not script/style noise.

func Test1542C_RTFInvalidHexOnlyConsumesEscape(t *testing.T) {
	// \' :x — invalid hex (space is not a hex digit). Old code ate 3 bytes
	// (":x" included); the fix consumes only \' plus the space, so "x"
	// survives in the output.
	got, err := Extract("t.rtf", []byte("{\\rtf1\\': x}"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "x") {
		t.Fatalf("byte after an invalid hex escape must survive, got %q", got.Text)
	}
}

func Test1542C_SVGTextPathAndAnchorKept(t *testing.T) {
	src := `<svg xmlns="http://www.w3.org/2000/svg"><text><textPath href="#p">curved</textPath></text><a xlink:href="u">linktext</a></svg>`
	got, err := Extract("t.svg", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "curved") {
		t.Fatalf("textPath text must be kept, got %q", got.Text)
	}
	if !strings.Contains(got.Text, "linktext") {
		t.Fatalf("anchor text must be kept, got %q", got.Text)
	}
}
