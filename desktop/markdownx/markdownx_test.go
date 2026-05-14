package markdownx

import (
	"strings"
	"testing"
)

// ── Block parsing tests ────────────────────────────

func TestParseHeading(t *testing.T) {
	blocks := parseBlocks("# Hello World\n")
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	h, ok := blocks[0].(*headingBlock)
	if !ok {
		t.Fatalf("expected headingBlock, got %T", blocks[0])
	}
	if h.level != 1 {
		t.Errorf("level = %d, want 1", h.level)
	}
	if len(h.runs) == 0 {
		t.Errorf("runs = %v", h.runs)
	}
	text := runsText(h.runs)
	if text != "Hello World" {
		t.Errorf("text = %q, want 'Hello World'", text)
	}
}

func TestParseParagraph(t *testing.T) {
	blocks := parseBlocks("Hello world\n")
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	_, ok := blocks[0].(*paragraphBlock)
	if !ok {
		t.Fatalf("expected paragraphBlock, got %T", blocks[0])
	}
}

func TestParseBold(t *testing.T) {
	blocks := parseBlocks("This is **bold** text\n")
	p := blocks[0].(*paragraphBlock)
	text := runsText(p.runs)
	if !strings.Contains(text, "bold") {
		t.Errorf("expected 'bold' in runs: %v", p.runs)
	}
	// Check that bold run has bold style.
	found := false
	for _, r := range p.runs {
		if r.text == "bold" && r.style.bold {
			found = true
		}
	}
	if !found {
		t.Errorf("expected bold style on 'bold' run: %v", p.runs)
	}
}

func TestParseItalic(t *testing.T) {
	blocks := parseBlocks("This is *italic* text\n")
	p := blocks[0].(*paragraphBlock)
	found := false
	for _, r := range p.runs {
		if r.text == "italic" && r.style.italic {
			found = true
		}
	}
	if !found {
		t.Errorf("expected italic style: %v", p.runs)
	}
}

func TestParseCodeSpan(t *testing.T) {
	blocks := parseBlocks("Use `fmt.Println` here\n")
	p := blocks[0].(*paragraphBlock)
	found := false
	for _, r := range p.runs {
		if strings.Contains(r.text, "fmt.Println") && r.style.monospace {
			found = true
		}
	}
	if !found {
		t.Errorf("expected code style: %v", p.runs)
	}
}

func TestParseCodeBlock(t *testing.T) {
	blocks := parseBlocks("```go\nfmt.Println(\"hello\")\n```\n")
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	cb, ok := blocks[0].(*codeBlock)
	if !ok {
		t.Fatalf("expected codeBlock, got %T", blocks[0])
	}
	if cb.lang != "go" {
		t.Errorf("lang = %q, want 'go'", cb.lang)
	}
	if len(cb.lines) != 1 || cb.lines[0] != "fmt.Println(\"hello\")" {
		t.Errorf("lines = %v", cb.lines)
	}
}

func TestParseUnorderedList(t *testing.T) {
	blocks := parseBlocks("- item1\n- item2\n- item3\n")
	lb, ok := blocks[0].(*listBlock)
	if !ok {
		t.Fatalf("expected listBlock, got %T", blocks[0])
	}
	if lb.ordered {
		t.Error("expected unordered list")
	}
	if len(lb.items) != 3 {
		t.Errorf("items = %d, want 3", len(lb.items))
	}
}

func TestParseOrderedList(t *testing.T) {
	blocks := parseBlocks("1. first\n2. second\n")
	lb, ok := blocks[0].(*listBlock)
	if !ok {
		t.Fatalf("expected listBlock, got %T", blocks[0])
	}
	if !lb.ordered {
		t.Error("expected ordered list")
	}
}

func TestParseBlockquote(t *testing.T) {
	blocks := parseBlocks("> This is a quote\n> Second line\n")
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	_, ok := blocks[0].(*blockquoteBlock)
	if !ok {
		t.Fatalf("expected blockquoteBlock, got %T", blocks[0])
	}
}

func TestParseHR(t *testing.T) {
	blocks := parseBlocks("above\n\n---\n\nbelow\n")
	if len(blocks) < 3 {
		t.Errorf("expected at least 3 blocks, got %d", len(blocks))
	}
	if _, ok := blocks[1].(*hrBlock); !ok {
		t.Fatalf("expected hrBlock at index 1, got %T", blocks[1])
	}
}

func TestParseTable(t *testing.T) {
	blocks := parseBlocks("| Name | Age |\n| --- | --- |\n| Alice | 30 |\n")
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	tb, ok := blocks[0].(*tableBlock)
	if !ok {
		t.Fatalf("expected tableBlock, got %T", blocks[0])
	}
	if len(tb.headers) != 2 || tb.headers[0] != "Name" {
		t.Errorf("headers = %v", tb.headers)
	}
	if len(tb.rows) != 1 || tb.rows[0][0] != "Alice" {
		t.Errorf("rows = %v", tb.rows)
	}
}

func TestParseMixedContent(t *testing.T) {
	input := "# Title\n\n**bold** text\n\n- item 1\n\n```go\ncode()\n```\n\n> quote\n"
	blocks := parseBlocks(input)
	if len(blocks) < 5 {
		t.Errorf("expected at least 5 blocks, got %d", len(blocks))
	}
}

// ── Widget API tests ───────────────────────────────

func TestCloseOpenCodeBlocks(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"hello", "hello"},
		{"```\ncode", "```\ncode\n```"},
		{"```\ncode\n```", "```\ncode\n```"},
	}
	for _, tt := range tests {
		got := closeOpenCodeBlocks(tt.input)
		if got != tt.expect {
			t.Errorf("got %q, want %q", got, tt.expect)
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

func TestNewMarkdownWidget(t *testing.T) {
	w := NewMarkdownWidget()
	if w == nil {
		t.Fatal("expected widget")
	}
	if w.Content() != "" {
		t.Errorf("expected empty, got %q", w.Content())
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

// ── Helpers ────────────────────────────────────────

func runsText(runs []textRun) string {
	var sb strings.Builder
	for _, r := range runs {
		sb.WriteString(r.text)
	}
	return sb.String()
}
