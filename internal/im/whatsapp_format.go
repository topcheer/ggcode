package im

import (
	"regexp"
	"strings"
)

// WhatsApp formatting syntax (verified from official docs):
//   - Bold:          *text*   (single asterisk)
//   - Italic:        _text_   (underscore)
//   - Strikethrough: ~text~   (single tilde)
//   - Monospace:     ```text```  (triple backtick)
//   - Inline code:   `text`   (single backtick)
//   - Block quote:   > text   (at start of line)
//   - Lists:         - item / * item / 1. item
//   - Headers:       NOT supported (convert to bold)
//   - Links:         NOT rendered as markdown (convert to plain text with URL)
//
// Sources:
//   - https://faq.whatsapp.com/539178204879377 (official FAQ)
//   - https://sendpulse.com/blog/whatsapp-text-formatting (comprehensive guide)
//
// markdownToWhatsApp converts standard markdown to WhatsApp's formatting syntax.
// Key conversions:
//   - **bold**    → *bold*     (double → single asterisk)
//   - ~~strike~~  → ~strike~   (double → single tilde)
//   - _italic_    → _italic_   (already same)
//   - `code`      → `code`     (already same)
//   - ```code```  → ```code``` (already same)
//   - ### Header  → *Header*   (no header support, use bold)
//   - [text](url) → text       (no link support, keep text only)
//   - ![alt](url) → removed    (no image syntax in text)
//   - > quote     → > quote    (already same)

var (
	// WhatsApp-specific patterns
	// Match **bold** but not *bold* (which WhatsApp uses natively)
	waBoldRe = regexp.MustCompile(`\*\*(.+?)\*\*`)
	// Match __bold__ (CommonMark underscore strong emphasis)
	waUnderscoreBoldRe = regexp.MustCompile(`__(.+?)__`)
	// Match ~~strikethrough~~ but not ~text~ (which WhatsApp uses natively)
	waStrikeRe = regexp.MustCompile(`~~(.+?)~~`)
	// Markdown headers: ### Text → *Text* (WhatsApp bold)
	waHeaderRe = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	// Markdown images: ![alt](url) → remove
	waImageRe = regexp.MustCompile(`!\[([^\]]*)\]\([^)]+\)`)
	// Markdown links: [text](url) → text
	waLinkRe = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	// Markdown horizontal rules
	waHRRe = regexp.MustCompile(`(?m)^(-{3,}|\*{3,})$`)
)

// markdownToWhatsApp converts standard markdown formatting to WhatsApp's
// native formatting syntax. Unlike stripMarkdown(), this preserves
// formatting intent (bold stays bold, strikethrough stays strikethrough)
// by adapting the marker syntax.
func markdownToWhatsApp(text string) string {
	if text == "" {
		return text
	}

	// 0a. GFM task lists: convert checkboxes to Unicode symbols
	// - [ ] item → ○ item, - [x] item → ✓ item
	// WhatsApp doesn't render checkboxes; use symbols for visual distinction.
	text = mdTaskCheckedRe.ReplaceAllString(text, "$1 ✓ ")
	text = mdTaskUncheckedRe.ReplaceAllString(text, "$1 ○ ")

	// 0b. GFM tables: convert to plain text (WhatsApp doesn't render tables)
	text = mdTableRe.ReplaceAllStringFunc(text, func(match string) string {
		lines := strings.Split(match, "\n")
		var result []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if isTableSeparator(trimmed) {
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
	// 1. Images: ![alt](url) → remove entirely (before link processing)
	text = waImageRe.ReplaceAllString(text, "")

	// 2. Links: [text](url) → text (url)
	// WhatsApp doesn't render markdown link syntax, but the client
	// auto-hyperlinks plain URLs in the body text.
	// Source: https://developers.facebook.com/documentation/business-messaging/whatsapp/messages/text-messages/
	text = waLinkRe.ReplaceAllString(text, "$1 ($2)")

	// 3. Bold: **text** → \x00text\x00 (temporary placeholder)
	// Uses null-byte placeholder to protect bold from italic regex below.
	// The italic regex would otherwise match the inner *text* of **text**.
	text = waBoldRe.ReplaceAllString(text, "\x00${1}\x00")

	// 3b. Underscore bold: __text__ → \x00text\x00 (CommonMark strong emphasis)
	// Same placeholder as **bold** above. Must run before italic step.
	text = waUnderscoreBoldRe.ReplaceAllString(text, "\x00${1}\x00")

	// 4. Italic: *text* → _text_ (markdown single-asterisk italic → WhatsApp underscore)
	// WhatsApp uses *text* for bold, so markdown *italic* must become _italic_.
	// Now safe because **bold** has been replaced with \x00bold\x00 placeholders.
	// The mdAsteriskItalicRe regex requires [^\s*] after the opening *, so it
	// correctly excludes * list items and * * * horizontal rules.
	text = mdAsteriskItalicRe.ReplaceAllString(text, "_${1}_")

	// 5. Restore bold: \x00text\x00 → *text* (WhatsApp bold)
	text = strings.ReplaceAll(text, "\x00", "*")

	// 6. Strikethrough: ~~text~~ → ~text~ (double → single tilde)
	text = waStrikeRe.ReplaceAllString(text, "~$1~")

	// 7. Headers: ### Header → *Header* (WhatsApp has no header syntax)
	text = waHeaderRe.ReplaceAllString(text, "*$1*")

	// 8. Horizontal rules: --- or *** → —
	text = waHRRe.ReplaceAllString(text, "—")

	// Note: The following are already compatible with WhatsApp and need no conversion:
	//   - _italic_  (underscore syntax is the same; markdown *italic* is handled in step 4)
	//   - `code`    (single backtick is the same)
	//   - ```code``` (triple backtick is the same)
	//   - > quote   (blockquote syntax is the same)
	//   - - item / * item (list syntax is the same)

	// Clean up: collapse multiple blank lines
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}

	return strings.TrimSpace(text)
}
