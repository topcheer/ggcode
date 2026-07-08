package im

import (
	"regexp"
	"strings"
)

// Platforms without any markdown rendering support.
// These adapters should call stripMarkdown() on outbound text.
//
// Verified via official documentation:
//   - IRC: https://modern.ircdocs.horse/formatting
//     (uses control characters, not markdown)
//   - Twitch: no text formatting support in chat
//   - WeCom: https://developer.work.weixin.qq.com/document/path/90236
//     (text type only, no formatting)
//   - WeChat: Official Account API text type, no formatting
//   - Nostr: NIP-04 DMs are plain text; client rendering varies
//
// Signal now supports text formatting (*bold*, _italic_, ~strike~, `code`).
// Use signalMarkdown() instead of stripMarkdown() for the Signal adapter.

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
	// Images: ![alt](url) → url (preserve URL so text-only platforms show the link)
	mdImageRe = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
	// Links: [text](url) → text (url)
	mdLinkRe = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	// GFM task lists: - [ ] item → ○ item, - [x] item → ✓ item
	// Processed after code blocks (so code containing [ ] is preserved)
	// and before links (so [ ] isn't confused with link syntax).
	mdTaskUncheckedRe = regexp.MustCompile(`(?m)^([-*])\s+\[ \]\s+`)
	mdTaskCheckedRe   = regexp.MustCompile(`(?m)^([-*])\s+\[[xX]\]\s+`)
	// Bullet lists: - item or * item at start of line → • item
	// Processed AFTER task lists (task lines already have ○/✓ prefixes).
	mdBulletRe = regexp.MustCompile(`(?m)^[-*]\s+`)

	// Headers: ### Header → Header
	mdHeaderRe = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	// Blockquotes: > text → text
	mdBlockquoteRe = regexp.MustCompile(`(?m)^>\s?(.+)$`)
	// Horizontal rules (dash and asterisk): --- or *** → —
	// Underscore HRs are handled separately at step 3a (before __bold__).
	mdHRRe = regexp.MustCompile(`(?m)^(-{3,}|\*{3,})$`)
	// Underscore horizontal rules: ___ → —
	// Must be processed BEFORE __bold__ to prevent ________ being consumed.
	mdUnderscoreHRRe = regexp.MustCompile(`(?m)^_{3,}$`)
	// GFM tables: consecutive lines starting with | (bordered table syntax)
	mdTableRe = regexp.MustCompile(`(?m)^(?:\|[^\n]*\|\s*\n)+\|[^\n]*\|`)
)

// stripMarkdown converts markdown-formatted text to plain text for platforms
// that don't support markdown rendering. It preserves readability by:
//   - Removing formatting markers (**, *, _, ~~, `)
//   - Converting headers to plain text
//   - Converting links to "text (url)" format
//   - Converting images to their URL (text-only platforms show the link)
//   - Converting bullet lists to Unicode bullets (• item)
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

	// 1d. Bullet lists: convert - or * at line start to Unicode bullet •
	// Must come AFTER task list processing (those lines already have ○/✓).
	text = mdBulletRe.ReplaceAllString(text, "• ")

	// 2. Inline code: `code` → code
	text = mdInlineCodeRe.ReplaceAllString(text, "$1")

	// 3. Bold: **text** → text (before italic to avoid ** being seen as *)
	text = mdBoldRe.ReplaceAllString(text, "$1")

	// 3a. Underscore horizontal rules: ___ (3+) on its own line → em dash
	// Must be before __bold__ to prevent _____ being consumed as __ + _ + __.
	text = mdUnderscoreHRRe.ReplaceAllString(text, "—")

	// 3b. Underscore bold: __text__ → text (before italic _text_ to avoid partial match)
	text = mdUnderscoreBoldRe.ReplaceAllString(text, "$1")

	// 4. Asterisk italic: *text* → text (after bold so ** is already stripped)
	text = mdAsteriskItalicRe.ReplaceAllString(text, "$1")

	// 5. Strikethrough: ~~text~~ → text (before italic)
	text = mdStrikeRe.ReplaceAllString(text, "$1")

	// 6. Images: ![alt](url) → url (preserve URL so text-only platforms show the link)
	text = mdImageRe.ReplaceAllString(text, "$2")

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

// signalMarkdown converts standard markdown to Signal's text formatting syntax.
// Signal supports: *bold*, _italic_, ~strike~, `code`, ||spoiler||.
//
// Key differences from standard markdown:
//   - Bold uses single * (not **)
//   - Italic uses _ (not *, since * means bold in Signal)
//   - Strikethrough uses single ~ (not ~~)
//   - Inline code uses ` (same as markdown)
//
// Non-formatting elements (headers, links, lists, tables) are processed the
// same way as stripMarkdown since Signal has no equivalent rendering for them.
func signalMarkdown(text string) string {
	if text == "" {
		return text
	}

	// 1. Fenced code blocks: preserve content (Signal renders `code` inline,
	// but doesn't have code blocks — extract inner content)
	text = mdCodeFenceRe.ReplaceAllStringFunc(text, func(match string) string {
		lines := strings.Split(match, "\n")
		if len(lines) < 2 {
			return strings.Trim(match, "`")
		}
		return strings.Join(lines[1:len(lines)-1], "\n")
	})

	// 2. Tables → plain text (same as stripMarkdown)
	text = mdTableRe.ReplaceAllStringFunc(text, func(match string) string {
		lines := strings.Split(match, "\n")
		var result []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || isTableSeparator(trimmed) {
				continue
			}
			core := strings.Trim(trimmed, "|")
			cells := strings.Split(core, "|")
			for i := range cells {
				cells[i] = strings.TrimSpace(cells[i])
			}
			result = append(result, strings.Join(cells, "  "))
		}
		return strings.Join(result, "\n")
	})

	// 3. Task lists (same as stripMarkdown)
	text = mdTaskCheckedRe.ReplaceAllString(text, "$1 ✓ ")
	text = mdTaskUncheckedRe.ReplaceAllString(text, "$1 ○ ")

	// 4. Bullet lists (same as stripMarkdown)
	text = mdBulletRe.ReplaceAllString(text, "• ")

	// 5. Underscore HR (before __bold__)
	text = mdUnderscoreHRRe.ReplaceAllString(text, "—")

	// 6. Convert **bold** → *bold* (Signal bold)
	// Use placeholder to distinguish from *italic* processing later.
	text = mdBoldRe.ReplaceAllString(text, "\x00b\x00$1\x00/b\x00")
	// 6b. Convert __bold__ → *bold* (Signal bold)
	text = mdUnderscoreBoldRe.ReplaceAllString(text, "\x00b\x00$1\x00/b\x00")

	// 7. Strikethrough: ~~text~~ → ~text~ (Signal strike)
	text = mdStrikeRe.ReplaceAllString(text, "\x00s\x00$1\x00/s\x00")

	// 8. Convert remaining *italic* → _italic_ (Signal italic, not bold)
	// Use ${1} not $1 because $1_ is parsed as capture group named "1_" in Go regexp.
	text = mdAsteriskItalicRe.ReplaceAllString(text, "_${1}_")

	// 9. Restore placeholders → Signal formatting syntax
	text = strings.ReplaceAll(text, "\x00b\x00", "*")
	text = strings.ReplaceAll(text, "\x00/b\x00", "*")
	text = strings.ReplaceAll(text, "\x00s\x00", "~")
	text = strings.ReplaceAll(text, "\x00/s\x00", "~")

	// 10. Images: ![alt](url) → url
	text = mdImageRe.ReplaceAllString(text, "$2")

	// 11. Links: [text](url) → text (url)
	text = mdLinkRe.ReplaceAllString(text, "$1 ($2)")

	// 12. Headers: ### Header → Header
	text = mdHeaderRe.ReplaceAllString(text, "$1")

	// 13. Blockquotes: > text → text
	text = mdBlockquoteRe.ReplaceAllString(text, "$1")

	// 14. Horizontal rules → em dash
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
