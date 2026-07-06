package im

import (
	"testing"
)

func TestMarkdownToWhatsApp_Bold(t *testing.T) {
	got := markdownToWhatsApp("This is **bold** text")
	want := "This is *bold* text"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMarkdownToWhatsApp_Strikethrough(t *testing.T) {
	got := markdownToWhatsApp("This is ~~deleted~~ text")
	want := "This is ~deleted~ text"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMarkdownToWhatsApp_Italic(t *testing.T) {
	// Italic uses _ which is the same in both markdown and WhatsApp
	got := markdownToWhatsApp("This is _italic_ text")
	want := "This is _italic_ text"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMarkdownToWhatsApp_InlineCode(t *testing.T) {
	// Inline code uses ` which is the same in both
	got := markdownToWhatsApp("Use `fmt.Println` here")
	want := "Use `fmt.Println` here"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMarkdownToWhatsApp_CodeBlock(t *testing.T) {
	// Code blocks use triple backticks, same in both
	input := "```go\nfmt.Println(\"hi\")\n```"
	got := markdownToWhatsApp(input)
	want := "```go\nfmt.Println(\"hi\")\n```"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMarkdownToWhatsApp_Headers(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"# Title", "*Title*"},
		{"## Section", "*Section*"},
		{"### Subsection", "*Subsection*"},
	}
	for _, tc := range tests {
		got := markdownToWhatsApp(tc.input)
		if got != tc.want {
			t.Errorf("markdownToWhatsApp(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestMarkdownToWhatsApp_Links(t *testing.T) {
	// WhatsApp doesn't support markdown links — keep text, drop URL
	got := markdownToWhatsApp("See [the docs](https://example.com) for more")
	want := "See the docs for more"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMarkdownToWhatsApp_Images(t *testing.T) {
	got := markdownToWhatsApp("Before ![logo](https://example.com/logo.png) After")
	want := "Before  After"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMarkdownToWhatsApp_Blockquote(t *testing.T) {
	// Blockquotes use > which is the same in both
	got := markdownToWhatsApp("> This is a quote")
	want := "> This is a quote"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMarkdownToWhatsApp_EmptyString(t *testing.T) {
	got := markdownToWhatsApp("")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestMarkdownToWhatsApp_NoMarkdown(t *testing.T) {
	input := "This is just plain text without any formatting."
	got := markdownToWhatsApp(input)
	if got != input {
		t.Errorf("plain text changed: got %q, want %q", got, input)
	}
}

func TestMarkdownToWhatsApp_Combined(t *testing.T) {
	input := "## Report\n\n**Important:** See [docs](https://example.com) and ~~old~~ info.\n\n`code` here."
	got := markdownToWhatsApp(input)
	want := "*Report*\n\n*Important:* See docs and ~old~ info.\n\n`code` here."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMarkdownToWhatsApp_HorizontalRule(t *testing.T) {
	got := markdownToWhatsApp("Above\n---\nBelow")
	want := "Above\n—\nBelow"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMarkdownToWhatsApp_PreservesWhatsAppNativeBold(t *testing.T) {
	// Single *text* is WhatsApp native bold — should not be converted
	input := "This is *already* WhatsApp bold"
	got := markdownToWhatsApp(input)
	if got != input {
		t.Errorf("WhatsApp native bold changed: got %q, want %q", got, input)
	}
}

func TestMarkdownToWhatsApp_PreservesListBullets(t *testing.T) {
	input := "- Item one\n- Item two\n- Item three"
	got := markdownToWhatsApp(input)
	if got != input {
		t.Errorf("list changed: got %q, want %q", got, input)
	}
}

func TestMarkdownToWhatsApp_Table(t *testing.T) {
	input := "| **Name** | Value |\n|----------|--------|\n| foo      | bar    |"
	got := markdownToWhatsApp(input)
	// Bold should convert to WhatsApp *bold*, pipes and separator removed
	want := "*Name*  Value\nfoo  bar"
	if got != want {
		t.Errorf("table: got %q, want %q", got, want)
	}
}
