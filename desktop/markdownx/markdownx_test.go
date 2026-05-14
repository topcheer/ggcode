package markdownx

import (
	"strings"
	"testing"
)

// ── renderMarkdown returns CanvasObjects ───────────

func TestRenderHeading(t *testing.T) {
	objs := renderMarkdown("# Hello World\n", newNodeRenderers())
	if len(objs) == 0 {
		t.Fatal("expected objects")
	}
}

func TestRenderParagraph(t *testing.T) {
	objs := renderMarkdown("Hello world\n", newNodeRenderers())
	if len(objs) == 0 {
		t.Fatal("expected objects")
	}
}

func TestRenderBold(t *testing.T) {
	objs := renderMarkdown("This is **bold** text\n", newNodeRenderers())
	if len(objs) == 0 {
		t.Fatal("expected objects")
	}
}

func TestRenderItalic(t *testing.T) {
	objs := renderMarkdown("This is *italic* text\n", newNodeRenderers())
	if len(objs) == 0 {
		t.Fatal("expected objects")
	}
}

func TestRenderCodeSpan(t *testing.T) {
	objs := renderMarkdown("Use `fmt.Println` here\n", newNodeRenderers())
	if len(objs) == 0 {
		t.Fatal("expected objects")
	}
}

func TestRenderCodeBlock(t *testing.T) {
	input := "```go\nfmt.Println(\"hello\")\n```\n"
	objs := renderMarkdown(input, newNodeRenderers())
	if len(objs) == 0 {
		t.Fatal("expected code block")
	}
}

func TestRenderUnorderedList(t *testing.T) {
	input := "- item1\n- item2\n- item3\n"
	objs := renderMarkdown(input, newNodeRenderers())
	if len(objs) == 0 {
		t.Fatal("expected list")
	}
}

func TestRenderOrderedList(t *testing.T) {
	input := "1. first\n2. second\n3. third\n"
	objs := renderMarkdown(input, newNodeRenderers())
	if len(objs) == 0 {
		t.Fatal("expected list")
	}
}

func TestRenderBlockquote(t *testing.T) {
	input := "> This is a quote\n> Second line\n"
	objs := renderMarkdown(input, newNodeRenderers())
	if len(objs) == 0 {
		t.Fatal("expected blockquote")
	}
}

func TestRenderLink(t *testing.T) {
	input := "[Click here](https://example.com)\n"
	objs := renderMarkdown(input, newNodeRenderers())
	if len(objs) == 0 {
		t.Fatal("expected paragraph with link")
	}
}

func TestRenderThematicBreak(t *testing.T) {
	input := "above\n\n---\n\nbelow\n"
	objs := renderMarkdown(input, newNodeRenderers())
	if len(objs) < 3 {
		t.Errorf("expected at least 3 objects, got %d", len(objs))
	}
}

func TestRenderTable(t *testing.T) {
	input := "| Name | Age |\n| --- | --- |\n| Alice | 30 |\n| Bob | 25 |\n"
	objs := renderMarkdown(input, newNodeRenderers())
	if len(objs) == 0 {
		t.Fatal("expected table")
	}
}

func TestRenderMixedContent(t *testing.T) {
	input := "# Title\n\n**bold** text\n\n- item 1\n\n```go\ncode()\n```\n\n> quote\n\n| A | B |\n|---|---|\n| 1 | 2 |\n"
	objs := renderMarkdown(input, newNodeRenderers())
	if len(objs) < 5 {
		t.Errorf("expected at least 5 objects for mixed content, got %d", len(objs))
	}
}

func TestRenderMultipleHeadings(t *testing.T) {
	input := "# H1\n\n## H2\n\n### H3\n"
	objs := renderMarkdown(input, newNodeRenderers())
	if len(objs) != 3 {
		t.Errorf("expected 3 heading objects, got %d", len(objs))
	}
}

// ── closeOpenCodeBlocks ────────────────────────────

func TestCloseOpenCodeBlocks(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"hello", "hello"},
		{"```\ncode", "```\ncode\n```"},
		{"```\ncode\n```", "```\ncode\n```"},
		{"```\na\n```\n```\nb", "```\na\n```\n```\nb\n```"},
	}
	for _, tt := range tests {
		got := closeOpenCodeBlocks(tt.input)
		if got != tt.expect {
			t.Errorf("closeOpenCodeBlocks(%q) = %q, want %q", tt.input, got, tt.expect)
		}
	}
}

// ── runeWidth ──────────────────────────────────────

func TestRuneWidth(t *testing.T) {
	tests := []struct {
		input  string
		expect int
	}{
		{"abc", 3},
		{"你好", 4},
		{"a你好b", 6},
		{"", 0},
		{"  ", 2},
	}
	for _, tt := range tests {
		got := runeWidth(tt.input)
		if got != tt.expect {
			t.Errorf("runeWidth(%q) = %d, want %d", tt.input, got, tt.expect)
		}
	}
}

// ── Widget API ─────────────────────────────────────

func TestNewMarkdownWidget(t *testing.T) {
	w := NewMarkdownWidget()
	if w == nil {
		t.Fatal("expected non-nil widget")
	}
	if w.Content() != "" {
		t.Errorf("expected empty content, got: %q", w.Content())
	}
}

func TestSetMarkdownContent(t *testing.T) {
	w := NewMarkdownWidget()
	// SetMarkdown calls rebuild which needs Fyne app, so test via buffer directly.
	w.mu.Lock()
	w.buffer.Reset()
	w.buffer.WriteString("# Hello")
	w.mu.Unlock()
	if w.Content() != "# Hello" {
		t.Errorf("expected '# Hello', got: %q", w.Content())
	}
}

func TestAppendChunk(t *testing.T) {
	w := NewMarkdownWidget()
	w.AppendChunk("Hello ")
	w.AppendChunk("World")
	if w.Content() != "Hello World" {
		t.Errorf("expected 'Hello World', got: %q", w.Content())
	}
}

// ── Helper ─────────────────────────────────────────

func TestExtractText(t *testing.T) {
	input := "# Hello **world**\n"
	objs := renderMarkdown(input, newNodeRenderers())
	if len(objs) == 0 {
		t.Fatal("expected objects")
	}
	_ = objs
}

func TestIsCJK(t *testing.T) {
	if !isCJK('中') {
		t.Error("expected 中 to be CJK")
	}
	if isCJK('a') {
		t.Error("expected a to not be CJK")
	}
}

// Ensure strings import.
var _ = strings.TrimSpace
