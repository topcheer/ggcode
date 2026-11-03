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

	lines := strings.Split(content, "\n")
	for _, line := range lines {

		if strings.Contains(line, ".chat-text") && strings.Contains(line, "white-space: pre-wrap") {

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

	startIdx := strings.Index(content, "function renderMarkdown(")
	if startIdx == -1 {
		t.Fatal("renderMarkdown function not found")
	}

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

	if !strings.Contains(content, ".chat-text") {
		t.Error("expected .chat-text CSS class")
	}

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

	lines := strings.Split(content, "\n")
	for i, line := range lines {

		if strings.Contains(line, "chat-text") &&
			strings.Contains(line, "esc(") &&
			!strings.Contains(line, "color:#ef4444") &&
			strings.Contains(line, "block.text") {
			t.Errorf("line %d: chat-text with esc() for message text should use renderMarkdown():\n  %s", i+1, strings.TrimSpace(line))
		}
	}
}
