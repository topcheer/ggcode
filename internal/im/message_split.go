package im

import (
	"strings"
	"unicode/utf8"
)

// ByteLimitPlatforms are platforms whose API message length limit is measured
// in bytes rather than characters/runes. For these platforms, the splitter
// must account for UTF-8 multi-byte encoding (e.g. Chinese characters are
// 3 bytes each).
var ByteLimitPlatforms = map[Platform]bool{
	PlatformWeCom:  true, // 2048 bytes
	PlatformWechat: true, // 2048 bytes
}

// PlatformLimits defines the maximum message length for each IM platform.
// All values are verified against official API documentation.
//
// Sources:
//   - Discord: https://discord.com/developers/docs/resources/channel#create-message (2000 chars)
//   - Slack: https://api.slack.com/messaging/retrieving#text (markdown_text 12000 chars)
//   - DingTalk: https://open.dingtalk.com/document/orgapp/robot-message-types (markdown text field ~5000 chars; 4000 conservative for JSON overhead)
//   - Telegram: https://core.telegram.org/bots/api#sendmessage (4096 chars)
//   - QQ: QQ Bot API (text ~3000 chars)
//   - Feishu: https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/im-v1/message/create (interactive card body ~30KB; 28000 chars leaves room for card JSON structure)
//   - IRC: RFC 2812 §2.3.1 — 512 bytes total minus overhead (~400 usable)
//   - Twitch: https://discuss.dev.twitch.tv/t/message-character-limit/7793 (500 chars)
//   - Signal: https://github.com/AsamK/signal-cli/issues/1598 (65536 bytes; 16000 chars conservative for CJK)
//   - Nostr: NIP-04 — no protocol limit; 2000 is conservative (relay-dependent)
//   - Matrix: Synapse default max_event_size 100KB; 60K chars leaves room for event JSON metadata
//   - Mattermost: https://docs.mattermost.com/administration-guide/manage/product-limits.html (16383 chars)
//   - WhatsApp: https://faq.whatsapp.com/539171692017362527 (personal accounts 65536 chars)
//   - WeCom: https://developer.work.weixin.qq.com/document/path/90236 (2048 bytes)
//   - WeChat: WeChat Official Account API (2048 bytes, same engine as WeCom)
//   - Dummy: No practical limit
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
	if ByteLimitPlatforms[p] {
		return splitMessageBytes(text, maxLen)
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

// splitMessageBytes splits text so that each chunk's UTF-8 byte length
// does not exceed maxBytes. It prefers splitting at newline boundaries
// for readability, falling back to hard cuts when necessary.
func splitMessageBytes(text string, maxBytes int) []string {
	text = strings.TrimSpace(text)
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
			// Find the best split point in runes[start:i]
			end := preferredByteSplit(runes[start:i], maxBytes)
			chunks = append(chunks, string(runes[start:start+end]))
			start += end
			byteCount = 0
			// Re-sync byte count from start
			for j := start; j <= i; j++ {
				byteCount += utf8.RuneLen(runes[j])
				if byteCount > maxBytes {
					// Current rune pushes over; flush and restart
					end2 := preferredByteSplit(runes[start:j], maxBytes)
					chunks = append(chunks, string(runes[start:start+end2]))
					start += end2
					byteCount = utf8.RuneLen(runes[start])
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
			// Recursively split the tail in case of very large remaining text
			chunks = append(chunks, splitMessageBytes(remaining, maxBytes)...)
		} else {
			chunks = append(chunks, remaining)
		}
	}

	return chunks
}

// preferredByteSplit finds the best rune index to split at within runes[0:maxBytes].
// Prefers newline boundaries, then falls back to a byte-budget-limited hard cut.
func preferredByteSplit(runes []rune, maxBytes int) int {
	best := 0
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
		best = i + 1
	}
	if best == 0 {
		best = 1 // at least one rune
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

// SplitMarkdown splits text for markdown-capable platforms (Discord, Slack,
// Mattermost, Matrix), ensuring fenced code blocks are not broken across
// chunk boundaries. When a split point falls inside a ``` code block, the
// block is closed at the end of the current chunk and reopened at the start
// of the next chunk.
//
// This prevents broken rendering where a code block's opening ``` appears
// in one message and the closing ``` in the next, causing the platform to
// render non-code text as monospace or vice versa.
//
// Language tags (e.g. ```go, ```python) are preserved across chunk boundaries
// so that syntax highlighting is maintained on platforms that support it
// (Discord, Slack, Matrix).
func SplitMarkdown(text string, maxLen int) []string {
	// Reserve space for code block markers that SplitMarkdown adds to chunks
	// crossing fenced code block boundaries: "```<lang>\n" prefix + "\n```"
	// suffix. The prefix is 3 + len(lang) + 1 runes; the suffix is 4 runes.
	// Common language tags are 0-10 chars (go, python, javascript), so 20 runes
	// of reserve (8 for bare markers + 12 for language tag) covers all practical
	// cases. Without this, a chunk that fills maxLen and then gets both markers
	// could exceed the platform's hard limit (e.g. Discord rejects messages >
	// 2000 chars with HTTP 400).
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
	codeLang := "" // language tag from the most recent opening fence

	for _, chunk := range chunks {
		// Capture the language tag for the prefix BEFORE scanning this chunk.
		// The scan below may update codeLang to a different language if the chunk
		// contains a close+reopen transition (e.g. end of go block, start of python
		// block), but the prefix needs the language that was active when entering
		// this chunk, not what it transitions to later.
		prefixLang := codeLang

		// Scan the original chunk for code fences to track the active language
		// for FUTURE chunks. We update codeLang each time we encounter an opening
		// fence (transition from outside to inside a code block).
		scanInBlock := inCodeBlock
		for _, line := range strings.Split(chunk, "\n") {
			trimmed := strings.TrimLeft(line, " \t")
			if strings.HasPrefix(trimmed, "```") {
				if !scanInBlock {
					// Opening fence — capture language tag
					codeLang = strings.TrimSpace(trimmed[3:])
				}
				scanInBlock = !scanInBlock
			}
		}

		// If continuing a code block from the previous chunk, reopen it with
		// the original language tag (captured before scan) to preserve syntax
		// highlighting
		if inCodeBlock {
			if prefixLang != "" {
				chunk = "```" + prefixLang + "\n" + chunk
			} else {
				chunk = "```\n" + chunk
			}
		}

		// Count triple-backtick fences to determine if we end inside a code block
		fenceCount := strings.Count(chunk, "```")
		inCodeBlock = (fenceCount % 2) == 1

		// If ending inside a code block, close it
		if inCodeBlock {
			chunk += "\n```"
		}

		result = append(result, chunk)
	}

	return result
}
