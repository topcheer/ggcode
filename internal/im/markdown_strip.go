package im

import (
	"regexp"
	"strings"
)

var (
	mdCodeFenceRe      = regexp.MustCompile("(?s)```.*?```")
	mdInlineCodeRe     = regexp.MustCompile("`([^`]+)`")
	mdBoldRe           = regexp.MustCompile(`\*\*(.+?)\*\*`)
	mdUnderscoreBoldRe = regexp.MustCompile(`__(.+?)__`)
	mdAsteriskItalicRe = regexp.MustCompile(`\*([^\s*](?:[^*]*?[^\s*])?)\*`)
	mdItalicRe         = regexp.MustCompile(`(?m)\b_([^_]+)_\b`)
	mdStrikeRe         = regexp.MustCompile(`~~(.+?)~~`)
	mdImageRe          = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
	mdLinkRe           = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	mdTaskUncheckedRe  = regexp.MustCompile(`(?m)^([-*])\s+\[ \]\s+`)
	mdTaskCheckedRe    = regexp.MustCompile(`(?m)^([-*])\s+\[[xX]\]\s+`)
	mdBulletRe         = regexp.MustCompile(`(?m)^[-*]\s+`)

	mdHeaderRe       = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	mdBlockquoteRe   = regexp.MustCompile(`(?m)^>\s?(.+)$`)
	mdHRRe           = regexp.MustCompile(`(?m)^(-{3,}|\*{3,})$`)
	mdUnderscoreHRRe = regexp.MustCompile(`(?m)^_{3,}$`)
	mdTableRe        = regexp.MustCompile(`(?m)^(?:\|[^\n]*\|\s*\n)+\|[^\n]*\|`)
)

func stripMarkdown(text string) string {
	if text == "" {
		return text
	}

	text = mdCodeFenceRe.ReplaceAllStringFunc(text, func(match string) string {
		lines := strings.Split(match, "\n")
		if len(lines) < 2 {
			return strings.Trim(match, "`")
		}

		inner := strings.Join(lines[1:len(lines)-1], "\n")
		return inner
	})

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

	text = mdTaskCheckedRe.ReplaceAllString(text, "$1 ✓ ")
	text = mdTaskUncheckedRe.ReplaceAllString(text, "$1 ○ ")

	text = mdBulletRe.ReplaceAllString(text, "• ")

	text = mdInlineCodeRe.ReplaceAllString(text, "$1")

	text = mdBoldRe.ReplaceAllString(text, "$1")

	text = mdUnderscoreHRRe.ReplaceAllString(text, "—")

	text = mdUnderscoreBoldRe.ReplaceAllString(text, "$1")

	text = mdAsteriskItalicRe.ReplaceAllString(text, "$1")

	text = mdStrikeRe.ReplaceAllString(text, "$1")

	text = mdImageRe.ReplaceAllString(text, "$2")

	text = mdLinkRe.ReplaceAllString(text, "$1 ($2)")

	text = mdItalicRe.ReplaceAllString(text, "$1")

	text = mdHeaderRe.ReplaceAllString(text, "$1")

	text = mdBlockquoteRe.ReplaceAllString(text, "$1")

	text = mdHRRe.ReplaceAllString(text, "—")

	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}

	return strings.TrimSpace(text)
}

func signalMarkdown(text string) string {
	if text == "" {
		return text
	}

	text = mdCodeFenceRe.ReplaceAllStringFunc(text, func(match string) string {
		lines := strings.Split(match, "\n")
		if len(lines) < 2 {
			return strings.Trim(match, "`")
		}
		return strings.Join(lines[1:len(lines)-1], "\n")
	})

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

	text = mdTaskCheckedRe.ReplaceAllString(text, "$1 ✓ ")
	text = mdTaskUncheckedRe.ReplaceAllString(text, "$1 ○ ")

	text = mdBulletRe.ReplaceAllString(text, "• ")

	text = mdUnderscoreHRRe.ReplaceAllString(text, "—")

	text = mdBoldRe.ReplaceAllString(text, "\x00b\x00$1\x00/b\x00")

	text = mdUnderscoreBoldRe.ReplaceAllString(text, "\x00b\x00$1\x00/b\x00")

	text = mdStrikeRe.ReplaceAllString(text, "\x00s\x00$1\x00/s\x00")

	text = mdAsteriskItalicRe.ReplaceAllString(text, "_${1}_")

	text = strings.ReplaceAll(text, "\x00b\x00", "*")
	text = strings.ReplaceAll(text, "\x00/b\x00", "*")
	text = strings.ReplaceAll(text, "\x00s\x00", "~")
	text = strings.ReplaceAll(text, "\x00/s\x00", "~")

	text = mdImageRe.ReplaceAllString(text, "$2")

	text = mdLinkRe.ReplaceAllString(text, "$1 ($2)")

	text = mdHeaderRe.ReplaceAllString(text, "$1")

	text = mdBlockquoteRe.ReplaceAllString(text, "$1")

	text = mdHRRe.ReplaceAllString(text, "—")

	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}

	return strings.TrimSpace(text)
}

func isTableSeparator(line string) bool {

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
