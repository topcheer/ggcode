package im

import "strings"

// PlatformLimits defines the maximum message length for each IM platform.
// All values are verified against official API documentation.
//
// Sources:
//   - Discord: https://discord.com/developers/docs/resources/channel#create-message (2000 chars)
//   - Slack: https://api.slack.com/reference/block-kit/blocks (4000 chars per text block)
//   - DingTalk: https://open.dingtalk.com/document/orgapp/robot-message-types (markdown ~5000 chars)
//   - Telegram: https://core.telegram.org/bots/api#sendmessage (4096 chars)
//   - QQ: QQ Bot API (text ~3000 chars)
//   - Feishu: https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/im-v1/message/create (post text ~30000 chars, conservative 4000)
//   - IRC: RFC 2812 §2.3.1 — 512 bytes total minus overhead (~400 usable)
//   - Twitch: https://discuss.dev.twitch.tv/t/message-character-limit/7793 (500 chars)
//   - Signal: https://github.com/signalapp/Signal-Desktop/issues/724 (2000 chars)
//   - Nostr: NIP-04 — no protocol limit; 2000 is conservative (relay-dependent)
//   - Matrix: Matrix spec — total event ≤ 65KB; 4000 is conservative for body text
//   - Mattermost: https://docs.mattermost.com/administration-guide/manage/product-limits.html (16383 chars)
//   - WhatsApp: https://developers.facebook.com/docs/whatsapp/cloud-api/messages/text-messages (4096 chars)
//   - WeCom: https://developer.work.weixin.qq.com/document/path/90236 (2048 bytes)
//   - WeChat: WeChat Official Account API (2048 bytes, same engine as WeCom)
//   - Dummy: No practical limit
var PlatformLimits = map[Platform]int{
	PlatformDiscord:    2000,
	PlatformSlack:      4000,
	PlatformDingTalk:   4000,
	PlatformTelegram:   4096,
	PlatformQQ:         3000,
	PlatformFeishu:     4000,
	PlatformIRC:        400,
	PlatformTwitch:     500,
	PlatformSignal:     2000,
	PlatformNostr:      2000,
	PlatformMatrix:     4000,
	PlatformMattermost: 16383,
	PlatformWhatsApp:   4096,
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
