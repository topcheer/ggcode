package im

import (
	"strings"
	"testing"
)

func TestStripMarkdown_Bold(t *testing.T) {
	got := stripMarkdown("This is **bold** text")
	want := "This is bold text"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripMarkdown_Italic(t *testing.T) {
	got := stripMarkdown("This is _italic_ text")
	want := "This is italic text"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripMarkdown_Strikethrough(t *testing.T) {
	got := stripMarkdown("This is ~~deleted~~ text")
	want := "This is deleted text"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripMarkdown_InlineCode(t *testing.T) {
	got := stripMarkdown("Use `fmt.Println` here")
	want := "Use fmt.Println here"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripMarkdown_CodeBlock(t *testing.T) {
	input := "Here:\n```go\nfmt.Println(\"hi\")\n```\nDone"
	got := stripMarkdown(input)
	want := "Here:\nfmt.Println(\"hi\")\nDone"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripMarkdown_Headers(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"# Title", "Title"},
		{"## Section", "Section"},
		{"### Subsection", "Subsection"},
		{"#### Deep", "Deep"},
		{"Some # not a header", "Some # not a header"},
	}
	for _, tc := range tests {
		got := stripMarkdown(tc.input)
		if got != tc.want {
			t.Errorf("stripMarkdown(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestStripMarkdown_Links(t *testing.T) {
	got := stripMarkdown("See [the docs](https://example.com) for more")
	want := "See the docs (https://example.com) for more"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripMarkdown_Images(t *testing.T) {
	got := stripMarkdown("Before ![logo](https://example.com/logo.png) After")
	want := "Before  After"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripMarkdown_Blockquote(t *testing.T) {
	got := stripMarkdown("> This is a quote")
	want := "This is a quote"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripMarkdown_HorizontalRule(t *testing.T) {
	got := stripMarkdown("Above\n---\nBelow")
	want := "Above\n—\nBelow"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripMarkdown_EmptyString(t *testing.T) {
	got := stripMarkdown("")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestStripMarkdown_NoMarkdown(t *testing.T) {
	input := "This is just plain text without any formatting."
	got := stripMarkdown(input)
	if got != input {
		t.Errorf("plain text changed: got %q, want %q", got, input)
	}
}

func TestStripMarkdown_Combined(t *testing.T) {
	input := "## Report\n\n**Important:** See [docs](https://example.com) and ~~old~~ info.\n\n`code` here."
	got := stripMarkdown(input)
	want := "Report\n\nImportant: See docs (https://example.com) and old info.\n\ncode here."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripMarkdown_PreservesListBullets(t *testing.T) {
	input := "- Item one\n- Item two\n- Item three"
	got := stripMarkdown(input)
	// List items with - are not stripped (no regex for lists)
	if got != input {
		t.Errorf("list changed: got %q, want %q", got, input)
	}
}

func TestStripMarkdown_AsteriskItalic(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"basic", "This is *italic* text", "This is italic text"},
		{"single_char", "Use *x* for variable", "Use x for variable"},
		{"multiple", "*one* and *two*", "one and two"},
		{"no_space_after_open", "5 * 3 = 15", "5 * 3 = 15"}, // math, not italic
		{"bullet_list", "* Item one", "* Item one"},         // bullet, not italic
		{"no_match_space_inside", "* not italic *", "* not italic *"},
		{"combined_bold_italic", "**bold** and *italic*", "bold and italic"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stripMarkdown(tc.input)
			if got != tc.want {
				t.Errorf("stripMarkdown(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestStripMarkdown_Table(t *testing.T) {
	input := "| Name | Value |\n|------|-------|\n| foo  | bar   |\n| baz  | qux   |"
	got := stripMarkdown(input)
	want := "Name  Value\nfoo  bar\nbaz  qux"
	if got != want {
		t.Errorf("stripMarkdown table = %q, want %q", got, want)
	}
}

func TestStripMarkdown_TableWithFormatting(t *testing.T) {
	input := "| **Bold** | `code` |\n|----------|--------|\n| **data** | `val`  |"
	got := stripMarkdown(input)
	want := "Bold  code\ndata  val"
	if got != want {
		t.Errorf("stripMarkdown table with formatting = %q, want %q", got, want)
	}
}

func TestStripMarkdown_TableWithLinks(t *testing.T) {
	input := "| [Docs](https://example.com) | text |\n|--------|--------|\n| cell   | data   |"
	got := stripMarkdown(input)
	// Links become "text (url)" format
	if !strings.Contains(got, "Docs") || !strings.Contains(got, "cell") {
		t.Errorf("stripMarkdown table with links = %q, expected Docs and cell", got)
	}
}

func TestStripMarkdown_TableWithColons(t *testing.T) {
	// GFM alignment colons in separator should be handled
	input := "| Left | Center | Right |\n|:-----|:------:|------:|\n| a    | b      | c      |"
	got := stripMarkdown(input)
	want := "Left  Center  Right\na  b  c"
	if got != want {
		t.Errorf("stripMarkdown table with colons = %q, want %q", got, want)
	}
}

func TestStripMarkdown_TablePreservesSurroundingText(t *testing.T) {
	input := "Before table\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\nAfter table"
	got := stripMarkdown(input)
	if !strings.Contains(got, "Before table") || !strings.Contains(got, "After table") {
		t.Errorf("stripMarkdown = %q, expected surrounding text preserved", got)
	}
	if strings.Contains(got, "|") {
		t.Errorf("stripMarkdown = %q, expected no pipe characters", got)
	}
	if strings.Contains(got, "---") {
		t.Errorf("stripMarkdown = %q, expected no separator dashes", got)
	}
}

func TestStripMarkdown_NotATableJustPipes(t *testing.T) {
	// Single line with pipe should not be treated as a table
	input := "Use the pipe | command"
	got := stripMarkdown(input)
	if got != "Use the pipe | command" {
		t.Errorf("stripMarkdown single pipe = %q, want unchanged", got)
	}
}
