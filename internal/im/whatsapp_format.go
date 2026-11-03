package im

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	waCodeBlockRe  = regexp.MustCompile("(?s)```.*?```")
	waInlineCodeRe = regexp.MustCompile("`[^`]+`")

	waBoldRe           = regexp.MustCompile(`\*\*(.+?)\*\*`)
	waUnderscoreBoldRe = regexp.MustCompile(`__(.+?)__`)
	waStrikeRe         = regexp.MustCompile(`~~(.+?)~~`)
	waHeaderRe         = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	waImageRe          = regexp.MustCompile(`!\[([^\]]*)\]\([^)]+\)`)
	waLinkRe           = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	waHRRe             = regexp.MustCompile(`(?m)^(-{3,}|\*{3,})$`)
)

func markdownToWhatsApp(text string) string {
	if text == "" {
		return text
	}

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
