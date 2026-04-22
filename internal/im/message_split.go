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
// Each chunk is guaranteed to be at most maxLen bytes long.
func SplitMessage(text string, maxLen int) []string {
	if text == "" || len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}

		splitAt := maxLen
		// Try to split at a newline boundary for readability
		if idx := strings.LastIndex(text[:maxLen], "\n"); idx > 0 {
			splitAt = idx + 1
		}
		chunks = append(chunks, text[:splitAt])
		text = text[splitAt:]
	}

	return chunks
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
