package tool

// #2151 regression: extractTaggedBlock (#2146's two-step same-name scan)
// sliced its capture from the ADVANCED rest pointer - rest had moved past
// every intermediate same-name tag, so each inner same-name block was
// silently dropped and only the container's tail survived (probe: a 427B
// container with an inner div yielded just its last 207B child; the
// #2146 regression tests only used differently-named children, so the
// depth++ branch had zero coverage).

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
