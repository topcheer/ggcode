# IM UX Research: Message Splitting Audit

## Date: 2025-07-05

## Objective
Audit all IM adapters for proper message length handling and splitting.

## Background
Each IM platform has a maximum message length. Adapters that don't split long messages risk silent truncation or API rejection. The shared infrastructure provides `SplitMessageForPlatform(text, platform)` in `internal/im/message_split.go` with verified limits in `PlatformLimits`.

## Findings

### Adapters WITH proper splitting (verified correct)
| Platform | Limit | Split Function | Source |
|----------|-------|----------------|--------|
| Discord | 2000 | `splitDiscordMessage` | [Discord API](https://discord.com/developers/docs/resources/channel#create-message) |
| Telegram | 4096 | `splitTGMessage` | [Telegram API](https://core.telegram.org/bots/api#sendmessage) |
| Slack | 4000 | `splitSlackMessage` | [Slack API](https://api.slack.com/reference/block-kit/blocks) |
| Feishu | 4000 | `splitFeishuMessage` | [Feishu API](https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/im-v1/message/create) |
| IRC | 400 | `splitIRCMessage` | RFC 2812 |
| Signal | 2000 | `splitSignalMessage` | Signal docs |
| Nostr | 2000 | `splitNostrMessage` | Conservative relay-dependent |
| WhatsApp | 4096 | `chunkWARunes` | [WhatsApp API](https://developers.facebook.com/docs/whatsapp/cloud-api/messages/text-messages) |
| Matrix | 4000 | `chunkText` + goldmark HTML | Matrix spec |
| Mattermost | 16383 | `splitMattermostText` | [Mattermost docs](https://docs.mattermost.com/administration-guide/manage/product-limits.html) |

### Adapters FIXED in this cycle
| Platform | Limit | Previous Behavior | Fix |
|----------|-------|-------------------|-----|
| **DingTalk** | 4000 | No splitting — single API call, silently truncated/rejected | Added `SplitMessageForPlatform` loop |
| **QQ** | 3000 | No splitting — single API call | Added split with incremented `msg_seq` per chunk |
| **WeCom** | 2048 | Truncated at 2048 bytes — **lost content** | Replaced truncation with splitting |

### Adapters NOT requiring splitting
| Platform | Reason |
|----------|--------|
| Twitch | 500 chars — uses `splitIRCMessage` with 500 limit |
| Dummy | 50000 chars — test adapter |

### Markdown handling summary
| Platform | Markdown Support | Handling |
|----------|-----------------|----------|
| Discord | Native | Sends raw markdown |
| Slack | Native (mrkdwn dialect) | `markdownToMrkdwn` conversion |
| Telegram | Native | Sends raw markdown |
| Feishu | Post (rich text) | Tries post, falls back to `stripMarkdown` |
| DingTalk | Native markdown | Sends raw markdown |
| Matrix | HTML rendering | `goldmark.Convert` to HTML |
| QQ | Configurable | Tries markdown, falls back to `stripMarkdown` |
| IRC | No | `stripMarkdown` |
| Twitch | No | `stripMarkdown` |
| Signal | No | `stripMarkdown` |
| Nostr | No | `stripMarkdown` |
| WeCom | No | `stripMarkdown` |
| WhatsApp | Custom | `markdownToWhatsApp` formatting conversion |

### Error response handling (from prior errcode audit)
All adapters that use REST APIs with embedded error codes (DingTalk webhook/API, Feishu API, WeChat iLink, WeCom) now properly check `errcode`/`code`/`ret` fields on HTTP 200 responses.

## Sources
- DingTalk markdown limit ~4000 chars: [openclaw-channel-dingtalk](https://deepwiki.com/cuyua9/openclaw-channel-dingtalk/5.1-send-service:-text-and-markdown-delivery) (uses 3800 as conservative limit)
- WeCom text limit: [WeCom API](https://developer.work.weixin.qq.com/document/path/90236) (2048 bytes)
- QQ Bot API: text ~3000 chars (PlatformLimits)
