package im

import (
	"strings"
)

const mdv2Special = "_*[]()~`>#+-=|{}.!"

func isMDV2Special(c byte) bool {
	return strings.ContainsRune(mdv2Special, rune(c))
}

func EscapeMarkdownV2(text string) string {
	var b strings.Builder
	b.Grow(len(text) + len(text)/4)

	lines := strings.Split(text, "\n")
	inCodeBlock := false

	for li, line := range lines {
		if li > 0 {
			b.WriteByte('\n')
		}

		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock

			b.WriteString(line)
			continue
		}

		if inCodeBlock {

			b.WriteString(line)
			continue
		}

		escapeLine(&b, line)
	}

	return b.String()
}

func escapeLine(b *strings.Builder, line string) {
	i := 0
	n := len(line)

	for i < n {
		c := line[i]

		if c == '`' {
			j := i + 1
			for j < n && line[j] != '`' {
				j++
			}
			if j < n {

				b.WriteString(line[i : j+1])
				i = j + 1
				continue
			}

			b.WriteString("\\`")
			i++
			continue
		}

		if c == '*' && i+1 < n && line[i+1] == '*' {
			if end := findClosingSeq(line, i+2, '*'); end >= 0 {

				b.WriteString("**")
				escapeText(b, line[i+2:end])
				b.WriteString("**")
				i = end + 2
				continue
			}
		}

		if c == '_' && i+1 < n && line[i+1] == '_' {
			if end := findClosingSeq(line, i+2, '_'); end >= 0 {
				b.WriteString("__")
				escapeText(b, line[i+2:end])
				b.WriteString("__")
				i = end + 2
				continue
			}
		}

		if c == '~' && i+1 < n && line[i+1] == '~' {
			if end := findClosingSeq(line, i+2, '~'); end >= 0 {
				b.WriteString("~~")
				escapeText(b, line[i+2:end])
				b.WriteString("~~")
				i = end + 2
				continue
			}
		}

		if c == '!' && i+1 < n && line[i+1] == '[' {
			if end := findLinkEnd(line, i+1); end > 0 {

				altStart := i + 2
				altEnd := strings.IndexByte(line[altStart:], ']')
				if altEnd >= 0 {
					altEnd += altStart
					b.WriteString("![")
					escapeText(b, line[altStart:altEnd])
					b.WriteString("](")
					urlEnd := end - 1
					escapeMDV2LinkURL(b, line[altEnd+2:urlEnd+1])
					b.WriteString(")")
					i = end + 1
					continue
				}
			}
		}

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

		if isMDV2Special(c) {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
		i++
	}
}

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

func findClosingSeq(line string, start int, ch byte) int {
	for i := start; i+1 < len(line); i++ {
		if line[i] == ch && line[i+1] == ch {
			return i
		}

		if line[i] == '`' {
			return -1
		}
	}
	return -1
}

func findLinkEnd(line string, bracketStart int) int {

	bracketEnd := strings.IndexByte(line[bracketStart:], ']')
	if bracketEnd < 0 {
		return -1
	}
	bracketEnd += bracketStart

	if bracketEnd+1 >= len(line) || line[bracketEnd+1] != '(' {
		return -1
	}

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
