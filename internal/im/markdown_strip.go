package im

import (
	"regexp"
	"strings"
)

// Platforms without any markdown rendering support.
// These adapters should call stripMarkdown() on outbound text.
//
// Verified via official documentation:
//   - Signal: https://support.signal.org/hc/en-us/articles/6325622209178
//     (select-and-format only, no text-based syntax)
//   - IRC: https://modern.ircdocs.horse/formatting
//     (uses control characters, not markdown)
//   - Twitch: no text formatting support in chat
//   - WeCom: https://developer.work.weixin.qq.com/document/path/90236
//     (text type only, no formatting)
//   - WeChat: Official Account API text type, no formatting
//   - Nostr: NIP-04 DMs are plain text; client rendering varies

// Markdown element regex patterns.
// Ordered for correct processing: code blocks → bold → italic → other.
var (
	// Fenced code blocks: ```lang\ncode\n``` → code
	// Use .*? (non-greedy) with (?s) so code containing backtick chars
	// (Go raw strings, inline code) is handled correctly. The old [^`]*
	// pattern would break on any backtick inside the code block.
	mdCodeFenceRe = regexp.MustCompile("(?s)```.*?```")
	// Inline code: `code` → code
	mdInlineCodeRe = regexp.MustCompile("`([^`]+)`")
	// Bold: **text** → text
	mdBoldRe = regexp.MustCompile(`\*\*(.+?)\*\*`)
	// Underscore bold: __text__ → text (CommonMark strong emphasis)
	mdUnderscoreBoldRe = regexp.MustCompile(`__(.+?)__`)
	// Asterisk italic: *text* → text (processed after bold ** is stripped)
	// Requires non-space immediately after opening * and before closing * to
	// avoid matching bullet lists (* item), math (5 * 3), or literal asterisks.
	mdAsteriskItalicRe = regexp.MustCompile(`\*([^\s*](?:[^*]*?[^\s*])?)\*`)
	// Italic: _text_ → text (avoid matching __ or word_middle)
	mdItalicRe = regexp.MustCompile(`(?m)\b_([^_]+)_\b`)
	// Strikethrough: ~~text~~ → text
	mdStrikeRe = regexp.MustCompile(`~~(.+?)~~`)
	// Images: ![alt](url) → (remove)
	mdImageRe = regexp.MustCompile(`!\[([^\]]*)\]\([^)]+\)`)
	// Links: [text](url) → text (url)
	mdLinkRe = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	// GFM task lists: - [ ] item → ○ item, - [x] item → ✓ item
	// Processed after code blocks (so code containing [ ] is preserved)
	// and before links (so [ ] isn't confused with link syntax).
	mdTaskUncheckedRe = regexp.MustCompile(`(?m)^([-*])\s+\[ \]\s+`)
	mdTaskCheckedRe   = regexp.MustCompile(`(?m)^([-*])\s+\[[xX]\]\s+`)

	// Headers: ### Header → Header
	mdHeaderRe = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	// Blockquotes: > text → text
	mdBlockquoteRe = regexp.MustCompile(`(?m)^>\s?(.+)$`)
	// Horizontal rules: --- or *** → —
	mdHRRe = regexp.MustCompile(`(?m)^(-{3,}|\*{3,})$`)
	// GFM tables: consecutive lines starting with | (bordered table syntax)
	mdTableRe = regexp.MustCompile(`(?m)^(?:\|[^\n]*\|\s*\n)+\|[^\n]*\|`)
)

// stripMarkdown converts markdown-formatted text to plain text for platforms
// that don't support markdown rendering. It preserves readability by:
//   - Removing formatting markers (**, *, _, ~~, `)
//   - Converting headers to plain text
//   - Converting links to "text (url)" format
//   - Removing image syntax entirely
//   - Preserving list structure with bullet characters
func stripMarkdown(text string) string {
	if text == "" {
		return text
	}

	// 1. Fenced code blocks: extract content, preserve newlines
	text = mdCodeFenceRe.ReplaceAllStringFunc(text, func(match string) string {
		lines := strings.Split(match, "\n")
		if len(lines) < 2 {
			return strings.Trim(match, "`")
		}
		// First line is ```lang, last is ```
		inner := strings.Join(lines[1:len(lines)-1], "\n")
		return inner
	})

	// 1b. GFM tables: convert to plain text (after code blocks to avoid
	// corrupting code content that contains pipe characters)
	text = mdTableRe.ReplaceAllStringFunc(text, func(match string) string {
		lines := strings.Split(match, "\n")
		var result []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			// Skip separator lines (contain only dashes, colons, pipes, spaces)
			if isTableSeparator(trimmed) {
				continue
			}
			// Strip leading/trailing pipes, split by |, trim each cell
			core := strings.Trim(trimmed, "|")
			cells := strings.Split(core, "|")
			for i := range cells {
				cells[i] = strings.TrimSpace(cells[i])
			}
			result = append(result, strings.Join(cells, "  "))
		}
		return strings.Join(result, "\n")
	})

	// 1c. GFM task lists: convert checkboxes to Unicode symbols
	// - [ ] item → ○ item, - [x] item → ✓ item
	text = mdTaskCheckedRe.ReplaceAllString(text, "$1 ✓ ")
	text = mdTaskUncheckedRe.ReplaceAllString(text, "$1 ○ ")

	// 2. Inline code: `code` → code
	text = mdInlineCodeRe.ReplaceAllString(text, "$1")

	// 3. Bold: **text** → text (before italic to avoid ** being seen as *)
	text = mdBoldRe.ReplaceAllString(text, "$1")

	// 3b. Underscore bold: __text__ → text (before italic _text_ to avoid partial match)
	text = mdUnderscoreBoldRe.ReplaceAllString(text, "$1")

	// 4. Asterisk italic: *text* → text (after bold so ** is already stripped)
	text = mdAsteriskItalicRe.ReplaceAllString(text, "$1")

	// 5. Strikethrough: ~~text~~ → text (before italic)
	text = mdStrikeRe.ReplaceAllString(text, "$1")

	// 6. Images: ![alt](url) → remove entirely
	text = mdImageRe.ReplaceAllString(text, "")

	// 7. Links: [text](url) → text (url)
	text = mdLinkRe.ReplaceAllString(text, "$1 ($2)")

	// 8. Italic: _text_ → text
	text = mdItalicRe.ReplaceAllString(text, "$1")

	// 9. Headers: ### Header → Header
	text = mdHeaderRe.ReplaceAllString(text, "$1")

	// 10. Blockquotes: > text → text
	text = mdBlockquoteRe.ReplaceAllString(text, "$1")

	// 11. Horizontal rules → em dash
	text = mdHRRe.ReplaceAllString(text, "—")

	// Clean up: collapse multiple blank lines
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}

	return strings.TrimSpace(text)
}

// isTableSeparator reports whether a table line is a separator row (contains
// only dashes, colons, pipes, and spaces). GFM separator rows look like:
// |---|---| or |:--:|---:| or | --- | --- |
func isTableSeparator(line string) bool {
	// Remove all pipe characters (both border and internal)
	core := strings.ReplaceAll(line, "|", "")
	core = strings.TrimSpace(core)
	if !strings.Contains(core, "-") {
		return false
	}
	for _, ch := range core {
		if ch == '-' || ch == ':' || ch == ' ' || ch == '\t' {
			continue
		}
		return false
	}
	return true
}
