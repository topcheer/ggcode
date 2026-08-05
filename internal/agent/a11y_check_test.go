package agent

import (
	"strings"
	"testing"
)

func TestCheckAccessibility_MissingAlt(t *testing.T) {
	warnings := checkAccessibility("test.html", "", `<img src="logo.png">`)
	if len(warnings) == 0 {
		t.Fatal("expected warning for missing alt attribute")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "missing 'alt'") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing alt warning not found in: %v", warnings)
	}
}

func TestCheckAccessibility_HasAlt(t *testing.T) {
	warnings := checkAccessibility("test.html", "", `<img src="logo.png" alt="Company Logo">`)
	for _, w := range warnings {
		if strings.Contains(w, "missing 'alt'") {
			t.Fatalf("unexpected alt warning when alt is present: %s", w)
		}
	}
}

func TestCheckAccessibility_EmptyAltOK(t *testing.T) {
	// alt="" is valid for decorative images
	warnings := checkAccessibility("test.html", "", `<img src="spacer.gif" alt="">`)
	for _, w := range warnings {
		if strings.Contains(w, "missing 'alt'") {
			t.Fatalf("alt=\"\" should not trigger missing alt warning: %s", w)
		}
	}
}

func TestCheckAccessibility_ClickableDiv(t *testing.T) {
	warnings := checkAccessibility("test.html", "", `<div onclick="doSomething()">Click me</div>`)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "onclick") && strings.Contains(w, "role") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected clickable div warning, got: %v", warnings)
	}
}

func TestCheckAccessibility_ClickableDivWithRole(t *testing.T) {
	html := `<div onclick="submit()" role="button" tabindex="0">Submit</div>`
	warnings := checkAccessibility("test.html", "", html)
	for _, w := range warnings {
		if strings.Contains(w, "onclick") {
			t.Fatalf("should not warn when role+tabindex present: %s", w)
		}
	}
}

func TestCheckAccessibility_InputWithoutLabel(t *testing.T) {
	warnings := checkAccessibility("test.html", "", `<input type="text" id="name">`)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "label") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected input-without-label warning, got: %v", warnings)
	}
}

func TestCheckAccessibility_InputWithLabel(t *testing.T) {
	html := `<label for="email">Email</label><input type="email" id="email">`
	warnings := checkAccessibility("test.html", "", html)
	for _, w := range warnings {
		if strings.Contains(w, "label") {
			t.Fatalf("should not warn when label for= is present: %s", w)
		}
	}
}

func TestCheckAccessibility_InputWithAriaLabel(t *testing.T) {
	html := `<input type="text" aria-label="Search">`
	warnings := checkAccessibility("test.html", "", html)
	for _, w := range warnings {
		if strings.Contains(w, "label") {
			t.Fatalf("aria-label should not trigger label warning: %s", w)
		}
	}
}

func TestCheckAccessibility_HiddenInputSkipped(t *testing.T) {
	warnings := checkAccessibility("test.html", "", `<input type="hidden" id="token">`)
	for _, w := range warnings {
		if strings.Contains(w, "label") {
			t.Fatalf("hidden input should not trigger label warning: %s", w)
		}
	}
}

func TestCheckAccessibility_SubmitInputSkipped(t *testing.T) {
	warnings := checkAccessibility("test.html", "", `<input type="submit" value="Go">`)
	for _, w := range warnings {
		if strings.Contains(w, "label") {
			t.Fatalf("submit input should not trigger label warning: %s", w)
		}
	}
}

func TestCheckAccessibility_HeadingSkip(t *testing.T) {
	html := `<h1>Title</h1><h3>Subtitle</h3>`
	warnings := checkAccessibility("test.html", "", html)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Heading level skip") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected heading skip warning, got: %v", warnings)
	}
}

func TestCheckAccessibility_NoHeadingSkip(t *testing.T) {
	html := `<h1>Title</h1><h2>Subtitle</h2><h3>Section</h3>`
	warnings := checkAccessibility("test.html", "", html)
	for _, w := range warnings {
		if strings.Contains(w, "Heading level skip") {
			t.Fatalf("h1->h2->h3 should not trigger skip warning: %s", w)
		}
	}
}

func TestCheckAccessibility_InvalidAriaRole(t *testing.T) {
	html := `<div role="buton">Click</div>`
	warnings := checkAccessibility("test.html", "", html)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Invalid ARIA role") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected invalid ARIA role warning for 'buton', got: %v", warnings)
	}
}

func TestCheckAccessibility_ValidAriaRole(t *testing.T) {
	html := `<div role="button">Click</div>`
	warnings := checkAccessibility("test.html", "", html)
	for _, w := range warnings {
		if strings.Contains(w, "Invalid ARIA role") {
			t.Fatalf("valid role 'button' should not trigger warning: %s", w)
		}
	}
}

func TestCheckAccessibility_BooleanAriaValue(t *testing.T) {
	html := `<div aria-hidden="yes">Content</div>`
	warnings := checkAccessibility("test.html", "", html)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, `aria-hidden`) && strings.Contains(w, "true") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected invalid boolean aria value warning, got: %v", warnings)
	}
}

func TestCheckAccessibility_ValidBooleanAria(t *testing.T) {
	html := `<div aria-hidden="false">Content</div>`
	warnings := checkAccessibility("test.html", "", html)
	for _, w := range warnings {
		if strings.Contains(w, "aria-hidden") && strings.Contains(w, "Invalid") {
			t.Fatalf("valid aria-hidden=false should not trigger warning: %s", w)
		}
	}
}

func TestCheckAccessibility_JSXFile(t *testing.T) {
	jsx := `export default function Foo() { return <img src="x.png" /> }`
	warnings := checkAccessibility("component.jsx", "", jsx)
	if len(warnings) == 0 {
		t.Fatal("expected a11y warnings in JSX file")
	}
}

func TestCheckAccessibility_TSXFile(t *testing.T) {
	tsx := `export default function Foo() { return <img src="x.png" /> }`
	warnings := checkAccessibility("component.tsx", "", tsx)
	if len(warnings) == 0 {
		t.Fatal("expected a11y warnings in TSX file")
	}
}

func TestCheckAccessibility_VueFile(t *testing.T) {
	vue := `<template><div onclick="go()">Click</div></template>`
	warnings := checkAccessibility("comp.vue", "", vue)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "onclick") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a11y warning in Vue file, got: %v", warnings)
	}
}

func TestCheckAccessibility_NonHTMLFile(t *testing.T) {
	// Go files should not trigger a11y checks
	warnings := checkAccessibility("main.go", "", `package main\nfunc main() {}`)
	if warnings != nil {
		t.Fatalf("Go file should not trigger a11y checks, got: %v", warnings)
	}
}

func TestCheckAccessibility_NoHTMLContent(t *testing.T) {
	warnings := checkAccessibility("test.html", "", `Just plain text, no tags`)
	if warnings != nil {
		t.Fatalf("content without HTML tags should return nil, got: %v", warnings)
	}
}

func TestCheckAccessibility_CleanHTML(t *testing.T) {
	html := `<h1>Title</h1>
<img src="logo.png" alt="Logo">
<label for="name">Name</label>
<input type="text" id="name">
<button onclick="submit()">Submit</button>`
	warnings := checkAccessibility("test.html", "", html)
	if len(warnings) > 0 {
		t.Fatalf("clean HTML should produce no warnings, got: %v", warnings)
	}
}

func TestCheckAccessibility_MultipleIssues(t *testing.T) {
	html := `<h1>Title</h1>
<h3>Skip</h3>
<img src="a.png">
<div onclick="x()">Click</div>
<input type="text" id="no-label">`
	warnings := checkAccessibility("test.html", "", html)
	if len(warnings) < 3 {
		t.Fatalf("expected at least 3 warnings for multiple issues, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckAccessibility_WarningCap(t *testing.T) {
	// Generate many img tags to test the cap
	var html strings.Builder
	html.WriteString("<h1>Title</h1><h3>Skip</h3>")
	for i := 0; i < 20; i++ {
		html.WriteString(`<img src="img`)
		html.WriteString(string(rune('0' + i)))
		html.WriteString(`.png">`)
	}
	warnings := checkAccessibility("test.html", "", html.String())
	// Should be capped (7 = 6 warnings + 1 truncation notice)
	if len(warnings) > maxA11yWarnings+2 {
		t.Fatalf("warnings should be capped, got %d", len(warnings))
	}
}

func TestCheckAccessibility_ClickableSpan(t *testing.T) {
	warnings := checkAccessibility("test.html", "", `<span onclick="go()">link</span>`)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "span") && strings.Contains(w, "onclick") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected clickable span warning, got: %v", warnings)
	}
}

func TestCheckAccessibility_JSXSelfClosingImg(t *testing.T) {
	// JSX uses self-closing tags
	jsx := `<img src="avatar.png" />`
	warnings := checkAccessibility("comp.jsx", "", jsx)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "missing 'alt'") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected missing alt warning in JSX self-closing img, got: %v", warnings)
	}
}
