package im

import (
	"strings"
	"unicode/utf8"
)

var ByteLimitPlatforms = map[Platform]bool{
	PlatformWeCom:  true,
	PlatformWechat: true,
	PlatformFeishu: true,
}

var PlatformLimits = map[Platform]int{
	PlatformDiscord:    2000,
	PlatformSlack:      12000,
	PlatformDingTalk:   4000,
	PlatformTelegram:   4096,
	PlatformQQ:         3000,
	PlatformFeishu:     28000,
	PlatformIRC:        400,
	PlatformTwitch:     500,
	PlatformSignal:     16000,
	PlatformNostr:      2000,
	PlatformMatrix:     60000,
	PlatformMattermost: 16383,
	PlatformWhatsApp:   65536,
	PlatformWeCom:      2048,
	PlatformWechat:     2048,
	PlatformDummy:      50000,
}

func SplitMessage(text string, maxLen int) []string {
	return splitMessageRunes(text, maxLen, false, false, false)
}

func SplitMessageForPlatform(text string, p Platform) []string {
	maxLen, ok := PlatformLimits[p]
	if !ok {
		maxLen = 4000
	}
	if ByteLimitPlatforms[p] {
		return splitMessageBytes(text, maxLen, true)
	}
	return SplitMessage(text, maxLen)
}

func splitMessageRunes(text string, maxLen int, trim bool, allowSpace bool, requireBalancedBreak bool) []string {
	if trim {
		text = strings.TrimSpace(text)
	}
	if text == "" || maxLen <= 0 {
		return []string{text}
	}
	runes := []rune(text)
	if len(runes) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(runes) > 0 {
		if len(runes) <= maxLen {
			chunks = append(chunks, string(runes))
			break
		}
		splitAt := preferredRuneSplit(runes, maxLen, allowSpace, requireBalancedBreak)
		chunks = append(chunks, string(runes[:splitAt]))
		runes = runes[splitAt:]
	}

	return chunks
}

func splitMessageBytes(text string, maxBytes int, trim bool) []string {
	if trim {
		text = strings.TrimSpace(text)
	}
	if text == "" || maxBytes <= 0 {
		return []string{text}
	}
	if len(text) <= maxBytes {
		return []string{text}
	}

	runes := []rune(text)
	var chunks []string
	start := 0
	byteCount := 0

	for i, r := range runes {
		runeBytes := utf8.RuneLen(r)
		if byteCount+runeBytes > maxBytes {

			end := preferredByteSplit(runes[start:i], maxBytes)
			chunks = append(chunks, string(runes[start:start+end]))
			start += end
			byteCount = 0

			for j := start; j <= i; j++ {
				byteCount += utf8.RuneLen(runes[j])
				if byteCount > maxBytes {

					end2 := preferredByteSplit(runes[start:j], maxBytes)
					chunks = append(chunks, string(runes[start:start+end2]))
					start += end2

					byteCount = 0
					for k := start; k <= i; k++ {
						byteCount += utf8.RuneLen(runes[k])
					}
					break
				}
			}
			if start > i {
				byteCount = 0
				continue
			}
			continue
		}
		byteCount += runeBytes
	}

	if start < len(runes) {
		remaining := string(runes[start:])
		if len(remaining) > maxBytes {

			chunks = append(chunks, splitMessageBytes(remaining, maxBytes, trim)...)
		} else {
			chunks = append(chunks, remaining)
		}
	}

	return chunks
}

func preferredByteSplit(runes []rune, maxBytes int) int {
	best := 0
	lastSpace := -1
	byteCount := 0
	for i, r := range runes {
		rb := utf8.RuneLen(r)
		if byteCount+rb > maxBytes {
			break
		}
		byteCount += rb
		if r == '\n' {
			return i + 1
		}
		if r == ' ' {
			lastSpace = i + 1
		}
		best = i + 1
	}
	if lastSpace > 0 {
		return lastSpace
	}
	if best == 0 {
		best = 1
	}
	return best
}

func preferredRuneSplit(runes []rune, maxLen int, allowSpace bool, requireBalancedBreak bool) int {
	end := maxLen
	if end > len(runes) {
		end = len(runes)
	}
	if idx := lastRuneIndex(runes[:end], '\n'); idx >= 0 && (!requireBalancedBreak || idx > end/2) {
		return idx + 1
	}
	if allowSpace {
		if idx := lastRuneIndex(runes[:end], ' '); idx >= 0 && (!requireBalancedBreak || idx > end/2) {
			return idx + 1
		}
	}
	return end
}

func lastRuneIndex(runes []rune, target rune) int {
	for i := len(runes) - 1; i >= 0; i-- {
		if runes[i] == target {
			return i
		}
	}
	return -1
}

func truncateRunes(text string, maxLen int, suffix string) string {
	runes := []rune(text)
	if maxLen <= 0 {
		return ""
	}
	if len(runes) <= maxLen {
		return text
	}
	suffixRunes := []rune(suffix)
	if len(suffixRunes) >= maxLen {
		return string(suffixRunes[:maxLen])
	}
	return string(runes[:maxLen-len(suffixRunes)]) + suffix
}

func SplitMarkdown(text string, maxLen int) []string {

	splitLen := maxLen
	if strings.Contains(text, "```") {
		splitLen = maxLen - 20
		if splitLen < 1 {
			splitLen = 1
		}
	}
	chunks := SplitMessage(text, splitLen)
	if len(chunks) <= 1 {
		return chunks
	}

	var result []string
	inCodeBlock := false
	codeLang := ""

	for _, chunk := range chunks {

		prefixLang := codeLang

		scanInBlock := inCodeBlock
		for _, line := range strings.Split(chunk, "\n") {
			trimmed := strings.TrimLeft(line, " \t")
			if strings.HasPrefix(trimmed, "```") {
				if !scanInBlock {

					codeLang = strings.TrimSpace(trimmed[3:])
				}
				scanInBlock = !scanInBlock
			}
		}

		if inCodeBlock {
			if prefixLang != "" {
				chunk = "```" + prefixLang + "\n" + chunk
			} else {
				chunk = "```\n" + chunk
			}
		}

		fenceCount := strings.Count(chunk, "```")
		inCodeBlock = (fenceCount % 2) == 1

		if inCodeBlock {
			chunk += "\n```"
		}

		result = append(result, chunk)
	}

	return result
}
