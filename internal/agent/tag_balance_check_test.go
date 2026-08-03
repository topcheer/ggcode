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

// (duplicate contains function removed - using strings.Contains instead)
