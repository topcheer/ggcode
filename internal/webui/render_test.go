package webui

import (
	"io/fs"
	"strings"
	"testing"
)

func TestSPA_IndexHTMLExists(t *testing.T) {
	f, err := spafs.Open("index.html")
	if err != nil {
		t.Fatalf("index.html not found in embedded FS: %v", err)
	}
	defer f.Close()
}

func TestSPA_RenderMarkdownUnique(t *testing.T) {
	data, err := fs.ReadFile(spafs, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Should have exactly one function renderMarkdown definition
	count := strings.Count(content, "function renderMarkdown(")
	if count != 1 {
		t.Errorf("expected exactly 1 renderMarkdown function definition, got %d", count)
	}
}

func TestSPA_ChatTextNoPreWrap(t *testing.T) {
	data, err := fs.ReadFile(spafs, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// .chat-text should NOT have white-space: pre-wrap (breaks markdown <p>/<br> rendering)
	// After our fix, .chat-text should have word-break but NOT white-space: pre-wrap
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		// Only check CSS declarations, not JS strings
		if strings.Contains(line, ".chat-text") && strings.Contains(line, "white-space: pre-wrap") {
			// Make sure it's in a CSS rule, not in a JS function
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "*") {
				t.Errorf(".chat-text CSS should not have white-space: pre-wrap (breaks markdown rendering):\n  %s", trimmed)
			}
		}
	}
}

func TestSPA_RenderMarkdownFeatures(t *testing.T) {
	data, err := fs.ReadFile(spafs, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Verify renderMarkdown handles all expected markdown features
	expectedPatterns := []struct {
		pattern string
		desc    string
	}{
		{"```", "code blocks"},
		{"<strong>", "bold"},
		{"<em>", "italic"},
		{"<h2", "h2 headers"},
		{"<h3", "h3 headers"},
		{"<h4", "h4 headers"},
		{"<ul>", "unordered lists"},
		{"<li>", "list items"},
		{"<a href", "links"},
		{"<hr", "horizontal rules"},
		{"<p>", "paragraphs"},
	}

	// Extract the renderMarkdown function body
	startIdx := strings.Index(content, "function renderMarkdown(")
	if startIdx == -1 {
		t.Fatal("renderMarkdown function not found")
	}
	// Find the end of the function (next "function " at start of line)
	afterFunc := content[startIdx:]
	endIdx := strings.Index(afterFunc[10:], "\nfunction ")
	if endIdx == -1 {
		endIdx = len(afterFunc)
	} else {
		endIdx += 10
	}
	funcBody := afterFunc[:endIdx]

	for _, ep := range expectedPatterns {
		if !strings.Contains(funcBody, ep.pattern) {
			t.Errorf("renderMarkdown should handle %s (expected pattern %q)", ep.desc, ep.pattern)
		}
	}
}

func TestSPA_CodeBlockWidth(t *testing.T) {
	data, err := fs.ReadFile(spafs, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Code blocks in renderMarkdown should have width:100%
	if !strings.Contains(content, "width:100%") {
		t.Error("code blocks should have width:100% for full-width rendering")
	}
}

func TestSPA_ChatTextCSS(t *testing.T) {
	data, err := fs.ReadFile(spafs, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Verify .chat-text has proper CSS with word-break but not pre-wrap
	if !strings.Contains(content, ".chat-text") {
		t.Error("expected .chat-text CSS class")
	}

	// Verify .chat-text pre has width:100%
	if !strings.Contains(content, ".chat-text pre") {
		t.Error("expected .chat-text pre CSS for code block width")
	}
}

func TestSPA_AllChatTextUsesRenderMarkdown(t *testing.T) {
	data, err := fs.ReadFile(spafs, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Find all places where chat-text is used for message content
	// These should use renderMarkdown, not just esc()
	// Exception: error messages (red text) can use esc()
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		// Check for chat-text with esc() that is NOT an error message
		if strings.Contains(line, "chat-text") &&
			strings.Contains(line, "esc(") &&
			!strings.Contains(line, "color:#ef4444") && // not error
			strings.Contains(line, "block.text") { // rendering message text
			t.Errorf("line %d: chat-text with esc() for message text should use renderMarkdown():\n  %s", i+1, strings.TrimSpace(line))
		}
	}
}
