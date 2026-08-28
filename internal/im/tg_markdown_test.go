package im

import (
	"strings"
	"testing"
)

func TestEscapeMarkdownV2_PlainText(t *testing.T) {
	input := "hello world"
	got := EscapeMarkdownV2(input)
	if got != "hello world" {
		t.Fatalf("expected unchanged, got %q", got)
	}
}

func TestEscapeMarkdownV2_DotsAndExclamations(t *testing.T) {
	input := "Hello. World! See section 3.2."
	want := "Hello\\. World\\! See section 3\\.2\\."
	got := EscapeMarkdownV2(input)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestEscapeMarkdownV2_AllSpecialChars(t *testing.T) {
	// All MarkdownV2 special chars outside of formatting should be escaped
	input := "_ * [ ] ( ) ~ ` > # + - = | { } . !"
	got := EscapeMarkdownV2(input)
	// Each special char should be preceded by \
	for _, ch := range mdv2Special {
		expected := "\\" + string(ch)
		if ch == '`' {
			// backtick without matching pair gets escaped
			continue
		}
		if !strings.Contains(got, expected) {
			t.Fatalf("expected %q to be escaped in %q", string(ch), got)
		}
	}
}

func TestEscapeMarkdownV2_Bold(t *testing.T) {
	input := "This is **bold** text."
	want := "This is **bold** text\\."
	got := EscapeMarkdownV2(input)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestEscapeMarkdownV2_BoldWithSpecialChars(t *testing.T) {
	input := "**hello.world**"
	want := "**hello\\.world**"
	got := EscapeMarkdownV2(input)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestEscapeMarkdownV2_Italic(t *testing.T) {
	input := "This is __italic__ text."
	want := "This is __italic__ text\\."
	got := EscapeMarkdownV2(input)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestEscapeMarkdownV2_Strikethrough(t *testing.T) {
	input := "This is ~~deleted~~ text."
	want := "This is ~~deleted~~ text\\."
	got := EscapeMarkdownV2(input)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestEscapeMarkdownV2_InlineCode(t *testing.T) {
	input := "Use `fmt.Println()` here."
	want := "Use `fmt.Println()` here\\."
	got := EscapeMarkdownV2(input)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestEscapeMarkdownV2_InlineCodeNoEscape(t *testing.T) {
	// Content inside backticks should NOT be escaped
	input := "Run `echo hello.world` now."
	want := "Run `echo hello.world` now\\."
	got := EscapeMarkdownV2(input)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestEscapeMarkdownV2_CodeBlock(t *testing.T) {
	input := "Before.\n```\nhello.world!\n```\nAfter."
	want := "Before\\.\n```\nhello.world!\n```\nAfter\\."
	got := EscapeMarkdownV2(input)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestEscapeMarkdownV2_CodeBlockMultiLine(t *testing.T) {
	input := "```\nline 1.\nline 2!\nline 3?\n```\nDone."
	want := "```\nline 1.\nline 2!\nline 3?\n```\nDone\\."
	got := EscapeMarkdownV2(input)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestEscapeMarkdownV2_Link(t *testing.T) {
	input := "Visit [Google](https://google.com) now."
	want := "Visit [Google](https://google.com) now\\."
	got := EscapeMarkdownV2(input)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestEscapeMarkdownV2_LinkWithSpecialCharsInText(t *testing.T) {
	input := "See [section 3.2](https://example.com)."
	want := "See [section 3\\.2](https://example.com)\\."
	got := EscapeMarkdownV2(input)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestEscapeMarkdownV2_Image(t *testing.T) {
	input := "Here is an image ![alt](https://example.com/img.png) end."
	want := "Here is an image ![alt](https://example.com/img.png) end\\."
	got := EscapeMarkdownV2(input)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestEscapeMarkdownV2_ImageWithSpecialAltText(t *testing.T) {
	input := "![pic.1](https://example.com/img.png)"
	want := "![pic\\.1](https://example.com/img.png)"
	got := EscapeMarkdownV2(input)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestEscapeMarkdownV2_Mixed(t *testing.T) {
	input := "**Bold** text with `code.inline` and a [link.](https://example.com).\n```\nno.escape.here!\n```\nEnd."
	want := "**Bold** text with `code.inline` and a [link\\.](https://example.com)\\.\n```\nno.escape.here!\n```\nEnd\\."
	got := EscapeMarkdownV2(input)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestEscapeMarkdownV2_ListItems(t *testing.T) {
	input := "- item one\n- item two\n- item three."
	want := "\\- item one\n\\- item two\n\\- item three\\."
	got := EscapeMarkdownV2(input)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestEscapeMarkdownV2_Headers(t *testing.T) {
	input := "# Header 1\n## Header 2."
	want := "\\# Header 1\n\\#\\# Header 2\\."
	got := EscapeMarkdownV2(input)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestEscapeMarkdownV2_UnevenBackticks(t *testing.T) {
	// Single backtick without closing should be escaped
	input := "code: `no closing backtick."
	want := "code: \\`no closing backtick\\."
	got := EscapeMarkdownV2(input)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestEscapeMarkdownV2_EmptyString(t *testing.T) {
	got := EscapeMarkdownV2("")
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestEscapeMarkdownV2_Parentheses(t *testing.T) {
	input := "Use func(arg) here."
	want := "Use func\\(arg\\) here\\."
	got := EscapeMarkdownV2(input)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

// TestEscapeMarkdownV2_LinkURLParensAndBackslash pins #1246: the URL part
// of a MarkdownV2 inline link/image must escape ')' and '\' per the Bot API
// spec - a raw ')' (Wikipedia-style URLs) failed the WHOLE message with
// 400 "character ')' is reserved".
func TestEscapeMarkdownV2_LinkURLParensAndBackslash(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			name: "link with parenthesized URL",
			in:   "[Go](https://en.wikipedia.org/wiki/Go_(programming_language))",
			want: "[Go](https://en.wikipedia.org/wiki/Go_(programming_language\\))",
		},
		{
			name: "image with parenthesized URL",
			in:   "![alt](https://example.com/file_(1).png)",
			want: "![alt](https://example.com/file_(1\\).png)",
		},
		{
			name: "URL with backslash",
			in:   "[x](https://example.com/a\\b)",
			want: "[x](https://example.com/a\\\\b)",
		},
		{
			name: "plain URL untouched",
			in:   "[Go](https://go.dev/)",
			want: "[Go](https://go.dev/)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EscapeMarkdownV2(tc.in); got != tc.want {
				t.Fatalf("EscapeMarkdownV2(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
