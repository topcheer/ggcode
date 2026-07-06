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
//   - Links:         NOT supported (convert to plain text)
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

	// 1. Images: ![alt](url) → remove entirely (before link processing)
	text = waImageRe.ReplaceAllString(text, "")

	// 2. Links: [text](url) → text (WhatsApp doesn't render markdown links)
	text = waLinkRe.ReplaceAllString(text, "$1")

	// 3. Bold: **text** → *text* (double → single asterisk)
	// Must run before strikethrough to avoid ** interfering with ~~ patterns.
	text = waBoldRe.ReplaceAllString(text, "*$1*")

	// 4. Strikethrough: ~~text~~ → ~text~ (double → single tilde)
	text = waStrikeRe.ReplaceAllString(text, "~$1~")

	// 5. Headers: ### Header → *Header* (WhatsApp has no header syntax)
	text = waHeaderRe.ReplaceAllString(text, "*$1*")

	// 6. Horizontal rules: --- or *** → —
	text = waHRRe.ReplaceAllString(text, "—")

	// Note: The following are already compatible with WhatsApp and need no conversion:
	//   - _italic_  (underscore syntax is the same)
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
