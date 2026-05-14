package markdownx

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/widget"
)

// ── Helper ──────────────────────────────────────────

// collectText recursively collects text from segments.
func collectText(segs []widget.RichTextSegment) string {
	var sb strings.Builder
	for _, s := range segs {
		sb.WriteString(s.Textual())
		// ParagraphSegment has child Texts.
		if ps, ok := s.(*widget.ParagraphSegment); ok {
			for _, t := range ps.Texts {
				sb.WriteString(t.Textual())
			}
		}
		// ListSegment has Items.
		if ls, ok := s.(*widget.ListSegment); ok {
			sb.WriteString(collectText(ls.Items))
		}
	}
	return sb.String()
}

// ── Parser tests ────────────────────────────────────

func TestParseHeading(t *testing.T) {
	segs := parseMarkdown("# Hello World\n", newNodeRenderers())
	text := collectText(segs)
	if !strings.Contains(text, "Hello World") {
		t.Errorf("expected 'Hello World', got: %q", text)
	}
}

func TestParseParagraph(t *testing.T) {
	segs := parseMarkdown("Hello world\n", newNodeRenderers())
	text := collectText(segs)
	if !strings.Contains(text, "Hello world") {
		t.Errorf("expected 'Hello world', got: %q", text)
	}
}

func TestParseBold(t *testing.T) {
	segs := parseMarkdown("This is **bold** text\n", newNodeRenderers())
	text := collectText(segs)
	if !strings.Contains(text, "bold") {
		t.Errorf("expected 'bold' in: %q", text)
	}
}

func TestParseItalic(t *testing.T) {
	segs := parseMarkdown("This is *italic* text\n", newNodeRenderers())
	text := collectText(segs)
	if !strings.Contains(text, "italic") {
		t.Errorf("expected 'italic' in: %q", text)
	}
}

func TestParseCodeSpan(t *testing.T) {
	segs := parseMarkdown("Use `fmt.Println` to print\n", newNodeRenderers())
	text := collectText(segs)
	if !strings.Contains(text, "fmt.Println") {
		t.Errorf("expected 'fmt.Println' in: %q", text)
	}
}

func TestParseFencedCodeBlock(t *testing.T) {
	input := "```go\nfmt.Println(\"hello\")\n```\n"
	segs := parseMarkdown(input, newNodeRenderers())
	if len(segs) == 0 {
		t.Fatal("expected segments")
	}
	text := segs[0].Textual()
	if !strings.Contains(text, "fmt.Println") {
		t.Errorf("expected code content, got: %q", text)
	}
}

func TestParseUnorderedList(t *testing.T) {
	input := "- item1\n- item2\n- item3\n"
	segs := parseMarkdown(input, newNodeRenderers())
	text := collectText(segs)
	for _, item := range []string{"item1", "item2", "item3"} {
		if !strings.Contains(text, item) {
			t.Errorf("expected '%s' in list, got: %q", item, text)
		}
	}
}

func TestParseOrderedList(t *testing.T) {
	input := "1. first\n2. second\n3. third\n"
	segs := parseMarkdown(input, newNodeRenderers())
	text := collectText(segs)
	for _, item := range []string{"first", "second", "third"} {
		if !strings.Contains(text, item) {
			t.Errorf("expected '%s' in list, got: %q", item, text)
		}
	}
}

func TestParseBlockquote(t *testing.T) {
	input := "> This is a quote\n> Second line\n"
	segs := parseMarkdown(input, newNodeRenderers())
	text := collectText(segs)
	if !strings.Contains(text, "quote") {
		t.Errorf("expected 'quote' in: %q", text)
	}
}

func TestParseLink(t *testing.T) {
	input := "[Click here](https://example.com)\n"
	segs := parseMarkdown(input, newNodeRenderers())
	text := collectText(segs)
	if !strings.Contains(text, "Click here") {
		t.Errorf("expected 'Click here' in: %q", text)
	}
}

func TestParseAutoLink(t *testing.T) {
	input := "<https://example.com>\n"
	segs := parseMarkdown(input, newNodeRenderers())
	text := collectText(segs)
	if !strings.Contains(text, "example.com") {
		t.Errorf("expected 'example.com' in: %q", text)
	}
}

func TestParseThematicBreak(t *testing.T) {
	input := "above\n\n---\n\nbelow\n"
	segs := parseMarkdown(input, newNodeRenderers())
	if len(segs) < 3 {
		t.Errorf("expected at least 3 segments, got %d", len(segs))
	}
}

func TestParseTable(t *testing.T) {
	input := "| Name | Age |\n| --- | --- |\n| Alice | 30 |\n| Bob | 25 |\n"
	segs := parseMarkdown(input, newNodeRenderers())
	if len(segs) == 0 {
		t.Fatal("expected segments for table")
	}
	text := collectText(segs)
	for _, expected := range []string{"Name", "Age", "Alice", "30", "Bob", "25"} {
		if !strings.Contains(text, expected) {
			t.Errorf("expected '%s' in table, got: %q", expected, text)
		}
	}
}

func TestParseMixedContent(t *testing.T) {
	input := "# Title\n\nThis is a **bold** paragraph.\n\n- item 1\n- item 2\n\n" +
		"```python\nprint('hello')\n```\n\n> A quote\n\n| A | B |\n|---|---|\n| 1 | 2 |\n"
	segs := parseMarkdown(input, newNodeRenderers())
	if len(segs) == 0 {
		t.Fatal("expected segments")
	}
	text := collectText(segs)
	for _, expected := range []string{"Title", "bold", "item 1", "print", "quote"} {
		if !strings.Contains(text, expected) {
			t.Errorf("expected '%s' in mixed content, got: %q", expected, text)
		}
	}
}

func TestParseMultipleHeadings(t *testing.T) {
	input := "# H1\n\n## H2\n\n### H3\n"
	segs := parseMarkdown(input, newNodeRenderers())
	text := collectText(segs)
	for _, h := range []string{"H1", "H2", "H3"} {
		if !strings.Contains(text, h) {
			t.Errorf("expected '%s' in: %q", h, text)
		}
	}
}

// ── Streaming tests ─────────────────────────────────

func TestCloseOpenCodeBlocks(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"hello", "hello"},
		{"```\ncode", "```\ncode\n```"},
		{"```\ncode\n```", "```\ncode\n```"},
		{"```\na\n```\n```\nb", "```\na\n```\n```\nb\n```"},
		{"```\n```\n```", "```\n```\n```\n```"}, // 3 backtick groups = 1 unclosed
	}
	for _, tt := range tests {
		got := closeOpenCodeBlocks(tt.input)
		if got != tt.expect {
			t.Errorf("closeOpenCodeBlocks(%q) = %q, want %q", tt.input, got, tt.expect)
		}
	}
}

// ── runeWidth tests ─────────────────────────────────

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

// ── Table segment tests ─────────────────────────────

func TestTableSegmentCreation(t *testing.T) {
	input := "| H1 | H2 |\n| --- | --- |\n| A | B |\n"
	segs := parseMarkdown(input, newNodeRenderers())
	if len(segs) == 0 {
		t.Fatal("expected table segment")
	}
	// Verify it's a tableSegment.
	ts, ok := segs[0].(*tableSegment)
	if !ok {
		t.Fatalf("expected *tableSegment, got %T", segs[0])
	}
	if len(ts.headers) != 2 || ts.headers[0] != "H1" || ts.headers[1] != "H2" {
		t.Errorf("headers wrong: %v", ts.headers)
	}
	if len(ts.rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(ts.rows))
	}
	if ts.rows[0][0] != "A" || ts.rows[0][1] != "B" {
		t.Errorf("row wrong: %v", ts.rows[0])
	}
}
