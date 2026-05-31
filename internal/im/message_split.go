package im

import "strings"

// PlatformLimits defines the maximum message length for each IM platform.
// These limits are based on official API documentation as of 2025.
var PlatformLimits = map[Platform]int{
	PlatformDiscord:  2000,
	PlatformSlack:    4000, // Slack blocks can be longer, but text is ~4000
	PlatformDingTalk: 4000, // Markdown message limit; was changed to 12000 in some API versions
	PlatformTelegram: 4096,
	PlatformQQ:       3000,  // QQ official bot API text limit
	PlatformFeishu:   4000,  // Feishu interactive card text content limit
	PlatformDummy:    50000, // No practical limit for dummy adapter
}

// SplitMessage splits a long message into chunks that fit within the
// platform's message length limit. It tries to split at line boundaries
// to preserve readability.
//
// The splitting strategy:
//  1. If the message fits within maxLen, return it as a single chunk.
//  2. Otherwise, try to find the last newline before maxLen.
//  3. If no newline found, split at maxLen (hard cut).
//  4. Repeat until the entire message is processed.
//
// Each chunk is guaranteed to be at most maxLen runes long.
func SplitMessage(text string, maxLen int) []string {
	return splitMessageRunes(text, maxLen, false, false, false)
}

// SplitMessageForPlatform is a convenience wrapper that looks up the
// platform's limit and calls SplitMessage.
func SplitMessageForPlatform(text string, p Platform) []string {
	maxLen, ok := PlatformLimits[p]
	if !ok {
		maxLen = 4000 // safe default
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
