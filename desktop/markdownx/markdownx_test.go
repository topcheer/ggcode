package markdownx

import (
	"strings"
	"testing"
)

// ── Parser tests ───────────────────────────────────

func TestParseHeading(t *testing.T) {
	blocks := parseBlocks("# Hello World\n")
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].kind != blockHeading {
		t.Errorf("expected heading, got %d", blocks[0].kind)
	}
	if blocks[0].level != 1 {
		t.Errorf("level = %d", blocks[0].level)
	}
	if blocks[0].content != "Hello World" {
		t.Errorf("content = %q", blocks[0].content)
	}
}

func TestParseParagraph(t *testing.T) {
	blocks := parseBlocks("Hello world\n")
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].kind != blockParagraph {
		t.Errorf("expected paragraph")
	}
}

func TestParseBold(t *testing.T) {
	blocks := parseBlocks("This is **bold** text\n")
	p := blocks[0]
	found := false
	for _, r := range p.runs {
		if r.text == "bold" && r.bold {
			found = true
		}
	}
	if !found {
		t.Errorf("bold not found in runs: %v", p.runs)
	}
}

func TestParseItalic(t *testing.T) {
	blocks := parseBlocks("This is *italic* text\n")
	p := blocks[0]
	found := false
	for _, r := range p.runs {
		if r.text == "italic" && r.italic {
			found = true
		}
	}
	if !found {
		t.Errorf("italic not found in runs: %v", p.runs)
	}
}

func TestParseCodeSpan(t *testing.T) {
	blocks := parseBlocks("Use `fmt.Println` here\n")
	p := blocks[0]
	found := false
	for _, r := range p.runs {
		if strings.Contains(r.text, "fmt.Println") && r.code {
			found = true
		}
	}
	if !found {
		t.Errorf("code not found in runs: %v", p.runs)
	}
}

func TestParseCodeBlock(t *testing.T) {
	blocks := parseBlocks("```go\nfmt.Println(\"hello\")\n```\n")
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].kind != blockCode {
		t.Errorf("expected code block")
	}
	if blocks[0].lang != "go" {
		t.Errorf("lang = %q", blocks[0].lang)
	}
	if len(blocks[0].lines) != 1 {
		t.Errorf("lines = %v", blocks[0].lines)
	}
}

func TestParseUnorderedList(t *testing.T) {
	blocks := parseBlocks("- item1\n- item2\n- item3\n")
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].kind != blockList {
		t.Errorf("expected list")
	}
	if blocks[0].ordered {
		t.Error("expected unordered")
	}
	if len(blocks[0].items) != 3 {
		t.Errorf("items = %d", len(blocks[0].items))
	}
}

func TestParseOrderedList(t *testing.T) {
	blocks := parseBlocks("1. first\n2. second\n")
	if blocks[0].kind != blockList || !blocks[0].ordered {
		t.Error("expected ordered list")
	}
}

func TestParseBlockquote(t *testing.T) {
	blocks := parseBlocks("> This is a quote\n> Second line\n")
	if len(blocks) != 1 || blocks[0].kind != blockBlockquote {
		t.Error("expected blockquote")
	}
}

func TestParseHR(t *testing.T) {
	blocks := parseBlocks("above\n\n---\n\nbelow\n")
	found := false
	for _, b := range blocks {
		if b.kind == blockHR {
			found = true
		}
	}
	if !found {
		t.Error("expected HR block")
	}
}

func TestParseTable(t *testing.T) {
	blocks := parseBlocks("| Name | Age |\n| --- | --- |\n| Alice | 30 |\n")
	if len(blocks) != 1 || blocks[0].kind != blockTable {
		t.Fatal("expected table")
	}
	tb := blocks[0]
	if len(tb.headers) != 2 || tb.headers[0] != "Name" {
		t.Errorf("headers = %v", tb.headers)
	}
	if len(tb.rows) != 1 || tb.rows[0][0] != "Alice" {
		t.Errorf("rows = %v", tb.rows)
	}
}

func TestParseMixed(t *testing.T) {
	input := "# Title\n\n**bold** text\n\n- item 1\n\n```\ncode()\n```\n\n> quote\n"
	blocks := parseBlocks(input)
	if len(blocks) < 5 {
		t.Errorf("expected >= 5 blocks, got %d", len(blocks))
	}
}

// ── Helper tests ───────────────────────────────────

func TestCloseOpenCodeBlocks(t *testing.T) {
	tests := []struct{ in, want string }{
		{"hello", "hello"},
		{"```\ncode", "```\ncode\n```"},
		{"```\ncode\n```", "```\ncode\n```"},
	}
	for _, tt := range tests {
		got := closeOpenCodeBlocks(tt.in)
		if got != tt.want {
			t.Errorf("got %q, want %q", got, tt.want)
		}
	}
}

func TestRuneWidth(t *testing.T) {
	if runeWidth("abc") != 3 {
		t.Error("abc should be 3")
	}
	if runeWidth("你好") != 4 {
		t.Error("你好 should be 4")
	}
}

// ── Widget tests ───────────────────────────────────

func TestNewMarkdownWidget(t *testing.T) {
	w := NewMarkdownWidget()
	if w == nil || w.Content() != "" {
		t.Error("expected empty widget")
	}
}

func TestAppendChunk(t *testing.T) {
	w := NewMarkdownWidget()
	w.AppendChunk("Hello ")
	w.AppendChunk("World")
	if w.Content() != "Hello World" {
		t.Errorf("got %q", w.Content())
	}
}
