package tool

// #2159 regression: extractFirstTag closed on the FIRST </tag> with no
// depth counting - a nested same-name block (HTML5's own article-in-
// article comment pattern) silently replaced the real body. And the
// id/attr selector tails had no delimiter anchor: data-id/data-role
// satisfied id=/role= and the WRONG (or empty) container won, starving
// the real one of its retry.

import (
	"strings"
	"testing"
)

func TestExtractFirstTagNestedSameName(t *testing.T) {
	inner := "<article><p>" + strings.Repeat("comment ", 20) + "</p></article>"
	main := "<p>MAINBODY " + strings.Repeat("real ", 40) + "</p>"
	html := "<article>" + inner + main + "</article>"

	got := extractFirstTag(html, "article")
	if got == "" {
		t.Fatal("extraction failed")
	}
	if !strings.Contains(got, "MAINBODY") {
		t.Fatalf("nested same-name comment block replaced the real body: %q...", got[:zz2159min(80, len(got))])
	}
	if !strings.Contains(got, "comment") {
		t.Fatal("inner block must still be INSIDE the capture")
	}
}

func zz2159min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestExtractByIDDoesNotMatchDataID(t *testing.T) {
	wrong := `<div data-id="content">` + strings.Repeat("WRONG ", 40) + `</div>`
	real := `<div id="content">` + strings.Repeat("REAL ", 40) + `</div>`
	html := "<body>" + wrong + real + "</body>"

	got := extractByID(html, "content")
	if got == "" {
		t.Fatal("real id container starved by a data-id false match")
	}
	if strings.Contains(got, "WRONG") || !strings.Contains(got, "REAL") {
		t.Fatalf("data-id container won over the real id container: %q...", got[:zz2159min(60, len(got))])
	}
}

func TestExtractAttrMatchDoesNotMatchDataRole(t *testing.T) {
	wrong := `<div data-role="main">` + strings.Repeat("WRONG ", 40) + `</div>`
	real := `<div role="main">` + strings.Repeat("REAL ", 40) + `</div>`
	html := "<body>" + wrong + real + "</body>"

	got := extractAttrMatch(html, "role", "main")
	if got == "" || !strings.Contains(got, "REAL") || strings.Contains(got, "WRONG") {
		t.Fatalf("data-role container won: %q", got)
	}
}
