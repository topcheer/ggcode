package extract

import (
	"strings"
	"testing"
)

func Test1542C_RTFInvalidHexOnlyConsumesEscape(t *testing.T) {

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
