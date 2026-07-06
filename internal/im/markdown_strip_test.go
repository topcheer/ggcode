package im

import (
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
