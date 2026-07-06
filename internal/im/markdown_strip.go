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
	mdCodeFenceRe = regexp.MustCompile("(?s)```[^`]*```")
	// Inline code: `code` → code
	mdInlineCodeRe = regexp.MustCompile("`([^`]+)`")
	// Bold: **text** → text
	mdBoldRe = regexp.MustCompile(`\*\*(.+?)\*\*`)
	// Italic: _text_ → text (avoid matching __ or word_middle)
	mdItalicRe = regexp.MustCompile(`(?m)\b_([^_]+)_\b`)
	// Strikethrough: ~~text~~ → text
	mdStrikeRe = regexp.MustCompile(`~~(.+?)~~`)
	// Images: ![alt](url) → (remove)
	mdImageRe = regexp.MustCompile(`!\[([^\]]*)\]\([^)]+\)`)
	// Links: [text](url) → text (url)
	mdLinkRe = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	// Headers: ### Header → Header
	mdHeaderRe = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	// Blockquotes: > text → text
	mdBlockquoteRe = regexp.MustCompile(`(?m)^>\s?(.+)$`)
	// Horizontal rules: --- or *** → —
	mdHRRe = regexp.MustCompile(`(?m)^(-{3,}|\*{3,})$`)
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

	// 2. Inline code: `code` → code
	text = mdInlineCodeRe.ReplaceAllString(text, "$1")

	// 3. Bold: **text** → text (before italic to avoid ** being seen as *)
	text = mdBoldRe.ReplaceAllString(text, "$1")

	// 4. Strikethrough: ~~text~~ → text (before italic)
	text = mdStrikeRe.ReplaceAllString(text, "$1")

	// 5. Images: ![alt](url) → remove entirely
	text = mdImageRe.ReplaceAllString(text, "")

	// 6. Links: [text](url) → text (url)
	text = mdLinkRe.ReplaceAllString(text, "$1 ($2)")

	// 7. Italic: _text_ → text
	text = mdItalicRe.ReplaceAllString(text, "$1")

	// 8. Headers: ### Header → Header
	text = mdHeaderRe.ReplaceAllString(text, "$1")

	// 9. Blockquotes: > text → text
	text = mdBlockquoteRe.ReplaceAllString(text, "$1")

	// 10. Horizontal rules → em dash
	text = mdHRRe.ReplaceAllString(text, "—")

	// Clean up: collapse multiple blank lines
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}

	return strings.TrimSpace(text)
}
