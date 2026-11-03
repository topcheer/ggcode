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
	PlatformWeCom:  true,
	PlatformWechat: true,
	PlatformFeishu: true,
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

// splitMessageBytes splits text so that each chunk's UTF-8 byte length
// does not exceed maxBytes. It prefers splitting at newline boundaries,
// then at word (space) boundaries, falling back to hard cuts when necessary.
//
// trim controls whether the WHOLE input is TrimSpace'd first. The WeCom
// family pass true (their legacy behavior); the IRC path passes false
// (#1660 case 1: an unconditional trim stripped leading indentation on
// every line - including lines that never needed splitting - so Python/Go
// code blocks arrived on IRC/Twitch with their syntax-semantic indentation
// silently gone; the old rune-based IRC splitter had trim=false by design).
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

// preferredByteSplit finds the best rune index to split at within runes[0:maxBytes].
// Prefers newline boundaries, then the last space inside the budget (word
// boundaries - #1660 case 3: the old version only matched \n, so long
// single-line text was hard-cut mid-word like "supercalifragilistic|s"),
// then falls back to a byte-budget-limited hard cut.
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
