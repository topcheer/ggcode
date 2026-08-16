package agent

import (
	"strings"
	"testing"
)

func TestCheckTagBalance_Balanced(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		content string
	}{
		{
			name: "balanced HTML",
			path: "index.html",
			content: `<html>
<head><title>Test</title></head>
<body>
  <div class="main">
    <p>Hello</p>
    <br>
    <img src="test.png">
  </div>
</body>
</html>`,
		},
		{
			name: "balanced JSX with fragments",
			path: "App.jsx",
			content: `function App() {
  return (
    <>
      <div className="container">
        <span>Hello</span>
        <br />
      </div>
    </>
  );
}`,
		},
		{
			name: "balanced XML",
			path: "config.xml",
			content: `<root>
  <item id="1">text</item>
  <item id="2">text2</item>
</root>`,
		},
		{
			name:    "empty file",
			path:    "empty.html",
			content: "",
		},
		{
			name:    "non-markup file",
			path:    "main.go",
			content: "package main\nfunc main() {}",
		},
		{
			name: "void elements only",
			path: "page.html",
			content: `<div>
  <br>
  <hr>
  <img src="a.png">
  <input type="text">
  <meta charset="utf-8">
</div>`,
		},
		{
			name: "self-closing tags",
			path: "comp.tsx",
			content: `<Component>
  <Child prop="val" />
  <SelfClose />
</Component>`,
		},
		{
			name: "HTML comment",
			path: "page.html",
			content: `<div>
  <!-- <p>commented out</p> -->
  <p>actual</p>
</div>`,
		},
		{
			name: "Vue template balanced",
			path: "App.vue",
			content: `<template>
  <div>
    <span>{{ message }}</span>
  </div>
</template>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checkTagBalance(tt.path, tt.content)
			if result != "" {
				t.Errorf("expected no warning for balanced markup, got: %s", result)
			}
		})
	}
}

func TestCheckTagBalance_Unclosed(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		content  string
		contains string
	}{
		{
			name: "unclosed div - mismatch detected first",
			path: "index.html",
			content: `<html>
<body>
  <div class="main">
    <p>Hello</p>
</body>
</html>`,
			contains: "does not match opening <div>",
		},
		{
			name: "unclosed span - mismatch detected first",
			path: "App.jsx",
			content: `<div>
  <span>Hello
</div>`,
			contains: "does not match opening <span>",
		},
		{
			name: "extra closing tag",
			path: "page.html",
			content: `<div>
  <p>text</p>
</div>
</div>`,
			contains: "closing tag </div> with no matching opening",
		},
		{
			name: "mismatched nesting",
			path: "page.html",
			content: `<div>
  <span>
  </div>
</span>`,
			contains: "does not match",
		},
		{
			name: "unclosed JSX fragment",
			path: "App.jsx",
			content: `<>
  <div>Hello</div>`,
			contains: "unclosed",
		},
		{
			name: "fragment close without open",
			path: "App.jsx",
			content: `<div>Hello</div>
</>`,
			contains: "</> with no matching opening",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checkTagBalance(tt.path, tt.content)
			if result == "" {
				t.Fatalf("expected warning containing %q, got empty", tt.contains)
			}
			if !strings.Contains(result, tt.contains) {
				t.Errorf("expected warning containing %q, got: %s", tt.contains, result)
			}
		})
	}
}

func TestCheckTagBalance_SkipsNonMarkup(t *testing.T) {
	result := checkTagBalance("script.py", "def foo():\n  pass")
	if result != "" {
		t.Errorf("expected empty for Python file, got: %s", result)
	}
}

func TestCheckTagBalance_SkipsLarge(t *testing.T) {
	// Build content just over the limit - use pre-allocated slice for speed.
	large := make([]byte, maxTagScanSize+100)
	for i := range large {
		large[i] = 'x'
	}
	result := checkTagBalance("big.html", "<div>"+string(large)+"</div>")
	if result != "" {
		t.Errorf("expected empty for large file, got: %s", result)
	}
}

// fix #236: void elements are an HTML-family concept. XML files (.xml/.svg)
// must pair every element strictly — RSS <link> is a container, not a void.
func TestCheckTagBalance_XMLStrictPairing(t *testing.T) {
	content := `<?xml version="1.0"?>
<rss version="2.0">
<channel>
<title>Feed</title>
<link>https://example.com</link>
<description>example feed</description>
</channel>
</rss>`
	if got := checkTagBalance("feed.xml", content); got != "" {
		t.Errorf("expected no warning for balanced RSS feed (link is a container in XML), got: %s", got)
	}
}

func TestCheckTagBalance_SVGSourceElementPaired(t *testing.T) {
	content := `<svg xmlns="http://www.w3.org/2000/svg">
<source>media</source>
<meta>meta</meta>
</svg>`
	if got := checkTagBalance("icon.svg", content); got != "" {
		t.Errorf("expected no warning for paired <source>/<meta> in SVG, got: %s", got)
	}
}

func TestCheckTagBalance_HTMLVoidElementsStillSkipped(t *testing.T) {
	content := "<html><head><meta charset=\"utf-8\"><link rel=\"stylesheet\" href=\"a.css\"></head>" +
		"<body><br><img src=\"a.png\"><input type=\"text\"></body></html>"
	if got := checkTagBalance("page.html", content); got != "" {
		t.Errorf("expected no warning for HTML void elements, got: %s", got)
	}
}

func TestCheckTagBalance_ClosingTagInsideAttributeValue(t *testing.T) {
	// fix #236: a </div> inside a quoted attribute value is not a real tag.
	content := `<div data-template="</div>"><span>ok</span></div>`
	if got := checkTagBalance("page.html", content); got != "" {
		t.Errorf("expected no warning for closing tag inside attribute value, got: %s", got)
	}
}

// fix #275: the old whole-file attrValueRe pass paired quotes across the
// document. An orphan `= "` in body text chained into a later tag's attribute
// quotes and blanked real markup (</p>, <div) — producing a false "closing
// tag no match" warning (and masking real imbalance). Attribute blanking is
// now scoped to opening tags only, so body-text quotes are harmless.
func TestCheckTagBalance_OrphanQuoteInBodyText(t *testing.T) {
	content := `<p>result = "pending</p><div class="x"></div>`
	if got := checkTagBalance("page.html", content); got != "" {
		t.Errorf("expected no warning for orphan quote in body text, got: %s", got)
	}
}

// fix #275 companion: real imbalance must still be reported when unrelated
// quotes appear in body text. The inner element is a non-optional <span> —
// since #545, </div> legally closes an open <p> (HTML5 optional end tag), so
// a <p> here would not be an imbalance anymore.
func TestCheckTagBalance_OrphanQuotePlusRealImbalance(t *testing.T) {
	content := `<div><span>result = "pending</div>`
	got := checkTagBalance("page.html", content)
	if got == "" {
		t.Fatal("expected warning for unclosed <span>, got empty")
	}
	if !strings.Contains(got, "does not match opening <span>") {
		t.Errorf("expected mismatch warning about <span>, got: %s", got)
	}
}

// fix #277: a closing-tag-looking string inside a JSX text-level expression
// container is not a real closing tag.
func TestCheckTagBalance_JSXTextExpressionString(t *testing.T) {
	content := `<div>{"</div>"}</div>`
	if got := checkTagBalance("App.jsx", content); got != "" {
		t.Errorf("expected no warning for JSX text expression string, got: %s", got)
	}
}

// fix #277: JSX attribute expression containers (attr={"</div>"}) must also
// be blanked — the old attrValueRe only matched ="..." / ='...'.
func TestCheckTagBalance_JSXAttributeExpressionContainer(t *testing.T) {
	content := `<div attr={"</div>"}><span>ok</span></div>`
	if got := checkTagBalance("App.jsx", content); got != "" {
		t.Errorf("expected no warning for JSX attribute expression container, got: %s", got)
	}
}

// (duplicate contains function removed - using strings.Contains instead)
