package im

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// Code block and inline code protection (same pattern as Slack)
	waCodeBlockRe  = regexp.MustCompile("(?s)```.*?```")
	waInlineCodeRe = regexp.MustCompile("`[^`]+`")

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

	// Protect code blocks and inline code from delimiter conversion.
	// Without this, characters like #, *, ~, []() inside code would be
	// incorrectly converted (e.g., Python `# comment` → `*comment*` bold).
	var codeSegments []string
	protectCode := func(match string) string {
		idx := len(codeSegments)
		codeSegments = append(codeSegments, match)
		return fmt.Sprintf("\x01C%d\x01", idx)
	}
	text = waCodeBlockRe.ReplaceAllStringFunc(text, protectCode)
	text = waInlineCodeRe.ReplaceAllStringFunc(text, protectCode)

	text = mdTaskCheckedRe.ReplaceAllString(text, "$1 ✓ ")
	text = mdTaskUncheckedRe.ReplaceAllString(text, "$1 ○ ")

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

	text = waImageRe.ReplaceAllString(text, "")

	text = waLinkRe.ReplaceAllString(text, "$1 ($2)")

	text = waBoldRe.ReplaceAllString(text, "\x00${1}\x00")

	text = waUnderscoreBoldRe.ReplaceAllString(text, "\x00${1}\x00")

	text = mdAsteriskItalicRe.ReplaceAllString(text, "_${1}_")

	text = strings.ReplaceAll(text, "\x00", "*")

	text = waStrikeRe.ReplaceAllString(text, "~$1~")

	text = waHeaderRe.ReplaceAllString(text, "*$1*")

	text = waHRRe.ReplaceAllString(text, "—")

	for i, seg := range codeSegments {
		text = strings.ReplaceAll(text, fmt.Sprintf("\x01C%d\x01", i), seg)
	}

	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}

	return strings.TrimSpace(text)
}
