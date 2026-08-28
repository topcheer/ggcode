package im

import (
	"strings"
)

// mdv2Special is the set of characters that Telegram MarkdownV2 requires
// to be escaped with a preceding backslash.
const mdv2Special = "_*[]()~`>#+-=|{}.!"

// isMDV2Special reports whether a byte is a MarkdownV2 special character.
func isMDV2Special(c byte) bool {
	return strings.ContainsRune(mdv2Special, rune(c))
}

// EscapeMarkdownV2 converts plain or markdown text to Telegram MarkdownV2 safe text.
//
// It preserves markdown formatting structures (bold, italic, strikethrough, code,
// code blocks, links, images) while escaping all other MarkdownV2 special characters.
func EscapeMarkdownV2(text string) string {
	var b strings.Builder
	b.Grow(len(text) + len(text)/4)

	lines := strings.Split(text, "\n")
	inCodeBlock := false

	for li, line := range lines {
		if li > 0 {
			b.WriteByte('\n')
		}

		// Handle code block boundaries (``` fenced)
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			// Emit the ``` line as-is (no escaping)
			b.WriteString(line)
			continue
		}

		if inCodeBlock {
			// Inside code block: no escaping
			b.WriteString(line)
			continue
		}

		escapeLine(&b, line)
	}

	return b.String()
}

// escapeLine processes a single line outside of code blocks.
// It preserves inline code spans and markdown formatting structures,
// escaping everything else.
func escapeLine(b *strings.Builder, line string) {
	i := 0
	n := len(line)

	for i < n {
		c := line[i]

		// Inline code span: find matching closing backtick
		if c == '`' {
			j := i + 1
			for j < n && line[j] != '`' {
				j++
			}
			if j < n {
				// Found closing backtick — emit the span verbatim
				b.WriteString(line[i : j+1])
				i = j + 1
				continue
			}
			// No closing backtick — escape the opening backtick
			b.WriteString("\\`")
			i++
			continue
		}

		// Bold: **text**
		if c == '*' && i+1 < n && line[i+1] == '*' {
			if end := findClosingSeq(line, i+2, '*'); end >= 0 {
				// Emit ** (unescaped) + escaped content + ** (unescaped)
				b.WriteString("**")
				escapeText(b, line[i+2:end])
				b.WriteString("**")
				i = end + 2
				continue
			}
		}

		// Italic: __text__
		if c == '_' && i+1 < n && line[i+1] == '_' {
			if end := findClosingSeq(line, i+2, '_'); end >= 0 {
				b.WriteString("__")
				escapeText(b, line[i+2:end])
				b.WriteString("__")
				i = end + 2
				continue
			}
		}

		// Strikethrough: ~~text~~
		if c == '~' && i+1 < n && line[i+1] == '~' {
			if end := findClosingSeq(line, i+2, '~'); end >= 0 {
				b.WriteString("~~")
				escapeText(b, line[i+2:end])
				b.WriteString("~~")
				i = end + 2
				continue
			}
		}

		// Image: ![alt](url)
		if c == '!' && i+1 < n && line[i+1] == '[' {
			if end := findLinkEnd(line, i+1); end > 0 {
				// Emit ![ ]( ) structure unescaped, escape alt text
				altStart := i + 2
				altEnd := strings.IndexByte(line[altStart:], ']')
				if altEnd >= 0 {
					altEnd += altStart
					b.WriteString("![")
					escapeText(b, line[altStart:altEnd])
					b.WriteString("](")
					urlEnd := end - 1 // before the closing ')'
					escapeMDV2LinkURL(b, line[altEnd+2:urlEnd+1])
					b.WriteString(")")
					i = end + 1
					continue
				}
			}
		}

		// Link: [text](url)
		if c == '[' {
			if end := findLinkEnd(line, i); end > 0 {
				altEnd := strings.IndexByte(line[i+1:], ']')
				if altEnd >= 0 {
					altEnd += i + 1
					b.WriteString("[")
					escapeText(b, line[i+1:altEnd])
					b.WriteString("](")
					urlEnd := end - 1
					escapeMDV2LinkURL(b, line[altEnd+2:urlEnd+1])
					b.WriteString(")")
					i = end + 1
					continue
				}
			}
		}

		// Default: escape special character
		if isMDV2Special(c) {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
		i++
	}
}

// escapeText escapes special characters in plain text content (inside bold/italic/etc).
// escapeMDV2LinkURL escapes exactly what Telegram's MarkdownV2 spec requires
// inside the (...) part of an inline link: "all ')' and '\' must be escaped
// with the preceding '\' character" (Bot API docs). A raw ')' would end the
// link early and the dangling ')' then fails the WHOLE message with
// 400 "character ')' is reserved" - Wikipedia-style parenthesized URLs are
// the common trigger (#1246). Everything else in the URL stays as-is.
func escapeMDV2LinkURL(b *strings.Builder, url string) {
	for i := 0; i < len(url); i++ {
		if url[i] == ')' || url[i] == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(url[i])
	}
}

func escapeText(b *strings.Builder, text string) {
	for i := 0; i < len(text); i++ {
		c := text[i]
		if isMDV2Special(c) {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
}

// findClosingSeq finds the index of the start of a closing double-char sequence (e.g. **).
// Returns the index of the first char of the pair, or -1 if not found.
func findClosingSeq(line string, start int, ch byte) int {
	for i := start; i+1 < len(line); i++ {
		if line[i] == ch && line[i+1] == ch {
			return i
		}
		// Don't span across backtick boundaries
		if line[i] == '`' {
			return -1
		}
	}
	return -1
}

// findLinkEnd finds the closing ) of a markdown link/image starting from the [ position.
// Returns the index of ), or -1 if not found.
func findLinkEnd(line string, bracketStart int) int {
	// Find ]
	bracketEnd := strings.IndexByte(line[bracketStart:], ']')
	if bracketEnd < 0 {
		return -1
	}
	bracketEnd += bracketStart

	// Check for (
	if bracketEnd+1 >= len(line) || line[bracketEnd+1] != '(' {
		return -1
	}

	// Find the closing ) with BALANCED paren matching (#1246): a plain
	// IndexByte ended the link at the first ')' - truncating every
	// parenthesized URL (Wikipedia's ...Go_(programming_language)) so the
	// link lost its closing paren and the stray ')' escaped into trailing
	// text. A ')' only closes the link when no unmatched '(' precedes it.
	depth := 0
	for i := bracketEnd + 2; i < len(line); i++ {
		switch line[i] {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return i
			}
			depth--
		}
	}
	return -1
}
