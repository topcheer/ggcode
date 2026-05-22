package markdownx

import (
	"image/color"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
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
	if blocks[0].items[0].text != "item1" {
		t.Errorf("item[0] = %q", blocks[0].items[0].text)
	}
}

func TestParseOrderedList(t *testing.T) {
	blocks := parseBlocks("1. first\n2. second\n")
	if blocks[0].kind != blockList || !blocks[0].ordered {
		t.Error("expected ordered list")
	}
}

func TestParseNestedList(t *testing.T) {
	input := "- item1\n  - sub1\n  - sub2\n- item2\n"
	blocks := parseBlocks(input)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	l := blocks[0]
	if len(l.items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(l.items))
	}
	if l.items[0].text != "item1" {
		t.Errorf("item[0].text = %q", l.items[0].text)
	}
	if l.items[0].children == nil {
		t.Fatal("expected nested list under item1")
	}
	if len(l.items[0].children.items) != 2 {
		t.Errorf("nested items = %d", len(l.items[0].children.items))
	}
	if l.items[0].children.items[0].text != "sub1" {
		t.Errorf("nested item[0] = %q", l.items[0].children.items[0].text)
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

type staticTheme struct {
	fyne.Theme
	colors map[fyne.ThemeColorName]color.Color
}

func (t staticTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if c, ok := t.colors[name]; ok {
		return c
	}
	return t.Theme.Color(name, variant)
}

func TestThemeAwareBlockColorsStayLightInLightTheme(t *testing.T) {
	th := staticTheme{
		Theme: theme.DefaultTheme(),
		colors: map[fyne.ThemeColorName]color.Color{
			theme.ColorNameBackground:      color.NRGBA{R: 250, G: 250, B: 252, A: 255},
			theme.ColorNameInputBackground: color.NRGBA{R: 242, G: 244, B: 248, A: 255},
			theme.ColorNameForeground:      color.NRGBA{R: 30, G: 35, B: 45, A: 255},
			theme.ColorNamePrimary:         color.NRGBA{R: 50, G: 100, B: 200, A: 255},
		},
	}

	assertLightColor(t, "code background", codeBlockBackgroundColorForTheme(th, theme.VariantLight), 180)
	assertLightColor(t, "quote background", quoteBackgroundColorForTheme(th, theme.VariantLight), 180)
	assertLightColor(t, "table alt background", tableAlternateBackgroundColorForTheme(th, theme.VariantLight), 180)
	assertLightColor(t, "table header background", tableHeaderBackgroundColorForTheme(th, theme.VariantLight), 150)
}

func TestThemeAwareBlockColorsStayDarkInDarkTheme(t *testing.T) {
	th := staticTheme{
		Theme: theme.DefaultTheme(),
		colors: map[fyne.ThemeColorName]color.Color{
			theme.ColorNameBackground:      color.NRGBA{R: 11, G: 16, B: 31, A: 255},
			theme.ColorNameInputBackground: color.NRGBA{R: 19, G: 28, B: 48, A: 255},
			theme.ColorNameForeground:      color.NRGBA{R: 245, G: 247, B: 251, A: 255},
			theme.ColorNamePrimary:         color.NRGBA{R: 110, G: 168, B: 255, A: 255},
		},
	}

	assertDarkColor(t, "code background", codeBlockBackgroundColorForTheme(th, theme.VariantDark), 90)
	assertDarkColor(t, "quote background", quoteBackgroundColorForTheme(th, theme.VariantDark), 90)
	assertDarkColor(t, "table alt background", tableAlternateBackgroundColorForTheme(th, theme.VariantDark), 90)
}

func assertLightColor(t *testing.T, name string, c color.Color, min uint8) {
	t.Helper()
	got := toNRGBA(c)
	if got.R < min || got.G < min || got.B < min {
		t.Fatalf("%s = %#v, want RGB channels >= %d", name, got, min)
	}
}

func assertDarkColor(t *testing.T, name string, c color.Color, max uint8) {
	t.Helper()
	got := toNRGBA(c)
	if got.R > max || got.G > max || got.B > max {
		t.Fatalf("%s = %#v, want RGB channels <= %d", name, got, max)
	}
}
