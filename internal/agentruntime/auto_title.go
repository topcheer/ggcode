package agentruntime

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/util"
)

const (
	titleMaxRunes = 60
	titleMinRunes = 3
)

var (
	fencedCodeBlockRe    = regexp.MustCompile("(?s)```[\\s\\S]*?```|~~~[\\s\\S]*?~~~")
	inlineCodeRe         = regexp.MustCompile("`([^`]+)`")
	urlRe                = regexp.MustCompile(`https?://\S+`)
	markdownHeadingRe    = regexp.MustCompile(`^#{1,6}\s+`)
	markdownBoldItalicRe = regexp.MustCompile(`\*{1,3}([^*]+)\*{1,3}|(^|[\s（(\[【{"'，、])_{1,3}([^_]+)_{1,3}($|[\s，。！？)\]】}"'.,;:!?])`)
	longFilePathRe       = regexp.MustCompile(`[\w\-./]+\b\.\w{1,5}\b`)
	commandPrefixRe      = regexp.MustCompile(`^(#|\$|>|%)\s*`)
	bracketedTagRe       = regexp.MustCompile(`^[\[(<][\w\- ]+[\])>]\s*`)
	excessiveSpacesRe    = regexp.MustCompile(`\s+`)
)

func GenerateTitle(userMessage string) string {
	s := userMessage

	s = fencedCodeBlockRe.ReplaceAllString(s, " ")

	s = inlineCodeRe.ReplaceAllString(s, "$1")

	s = urlRe.ReplaceAllString(s, "")

	s = markdownHeadingRe.ReplaceAllString(s, "")

	s = markdownBoldItalicRe.ReplaceAllString(s, "$1$2$3$4")

	s = longFilePathRe.ReplaceAllStringFunc(s, func(m string) string {
		parts := strings.Split(m, "/")
		return parts[len(parts)-1]
	})

	s = commandPrefixRe.ReplaceAllString(s, "")
	s = bracketedTagRe.ReplaceAllString(s, "")

	if idx := strings.IndexByte(s, '\n'); idx > 0 {
		firstLine := strings.TrimSpace(s[:idx])
		if utf8.RuneCountInString(firstLine) >= titleMinRunes {
			s = firstLine
		}
	}

	s = strings.TrimSpace(excessiveSpacesRe.ReplaceAllString(s, " "))

	if utf8.RuneCountInString(s) < titleMinRunes {
		return ""
	}

	if utf8.RuneCountInString(s) > titleMaxRunes {
		s = firstSentence(s)
	}

	s = truncateTitle(s, titleMaxRunes)

	return s
}

func firstSentence(s string) string {
	endChars := ".!?。！？"
	runeIdx := 0
	for i, r := range s {

		if r == '.' {
			hasPrev := i > 0 && isASCIIDigit(s[i-1])
			hasNext := i+1 < len(s) && isASCIIDigit(s[i+1])
			if hasPrev && hasNext {
				runeIdx++
				if runeIdx >= titleMaxRunes*2 {
					break
				}
				continue
			}
		}
		if strings.ContainsRune(endChars, r) {
			candidate := strings.TrimSpace(s[:i+utf8.RuneLen(r)])
			if utf8.RuneCountInString(candidate) >= titleMinRunes {
				return strings.TrimRight(candidate, ".!?。！？")
			}
		}
		runeIdx++
		if runeIdx >= titleMaxRunes*2 {
			break
		}
	}
	return s
}

func isASCIIDigit(b byte) bool { return b >= '0' && b <= '9' }

func truncateTitle(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}

	runes := []rune(s)
	truncated := string(runes[:maxRunes])

	if idx := strings.LastIndex(truncated, " "); idx > maxRunes/2 {
		truncated = truncated[:idx]
	}

	truncated = strings.TrimRight(truncated, " ,;:、，；：")

	return truncated + "…"
}

func ShouldAutoTitle(currentTitle string) bool {
	t := strings.TrimSpace(currentTitle)
	return t == "" || t == "New session" || t == "新会话"
}

func RefineTitleAfterRun(currentTitle, firstUserMessage, agentSummary string) string {

	if !ShouldAutoTitle(currentTitle) && !isGenericTitle(currentTitle) {
		return ""
	}

	cleaned := GenerateTitle(firstUserMessage)
	if !isGenericTitle(cleaned) {

		if cleaned != "" && cleaned != currentTitle {
			return cleaned
		}
		return ""
	}

	if agentSummary != "" {
		return util.Truncate(agentSummary, titleMaxRunes)
	}

	return ""
}

func isGenericTitle(title string) bool {
	t := strings.ToLower(strings.TrimSpace(title))
	if t == "" || t == "new session" || t == "新会话" {
		return true
	}

	generics := map[string]bool{
		"hi": true, "hello": true, "hey": true, "help": true,
		"test": true, "ok": true, "你好": true, "测试": true,
		"start": true, "begin": true, "question": true,
	}
	if generics[t] {
		return true
	}

	if !strings.Contains(t, " ") && utf8.RuneCountInString(t) < 6 && !containsCJK(t) {
		return true
	}
	return false
}

func containsCJK(s string) bool {
	for _, r := range s {
		if (r >= 0x2E80 && r <= 0x9FFF) ||
			(r >= 0x3040 && r <= 0x30FF) ||
			(r >= 0xAC00 && r <= 0xD7AF) ||
			(r >= 0xF900 && r <= 0xFAFF) ||
			(r >= 0xFF01 && r <= 0xFF60) {
			return true
		}
	}
	return false
}
