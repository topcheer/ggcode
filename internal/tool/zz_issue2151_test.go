package tool

import (
	"strings"
	"testing"
)

func TestExtractByIDKeepsInnerSameNameBlocks(t *testing.T) {
	inner := `<div class="a">` + strings.Repeat("alpha ", 45) + `</div>`
	second := `<p>` + strings.Repeat("beta ", 45) + `</p>`
	html := `<body><div id="content">` + inner + second + `</div></body>`

	got := extractByID(html, "content")
	if got == "" {
		t.Fatal("extraction failed outright")
	}
	if !strings.Contains(got, `class="a"`) {
		t.Fatal("inner same-name block was dropped (capture started at the advanced rest pointer)")
	}
	if !strings.Contains(got, "beta") {
		t.Fatal("tail sibling dropped")
	}
	if !strings.Contains(got, "alpha") {
		t.Fatal("inner block content dropped")
	}
}

func TestExtractAttrMatchKeepsInnerSameNameBlocks(t *testing.T) {
	inner := `<section>` + strings.Repeat("gamma ", 45) + `</section>`
	html := `<body><div role="main">` + inner + `</div></body>`
	got := extractAttrMatch(html, "role", "main")
	if got == "" || !strings.Contains(got, "gamma") {
		t.Fatalf("inner same-name section dropped: %q", got)
	}
}
