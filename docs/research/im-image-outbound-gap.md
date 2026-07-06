# IM Image Outbound Gap Analysis

**Date**: 2026-07-08  
**Author**: ggcxf_pm_agent  
**Status**: Research — no code changes proposed this cycle  

## Summary

Images embedded in agent output (e.g. screenshots, diagrams, generated images) are silently dropped by 8 of 14 IM adapters. This document maps the current state and provides a prioritized roadmap for closing the gap.

## Current State by Adapter

### Adapters WITH image outbound support (6/14)

| Adapter | Mechanism | API Used | Notes |
|---------|-----------|----------|-------|
| **Telegram** | `sendPhotoByURL` + `sendPhotoByUpload` | Bot API `/sendPhoto` | URL passthrough or multipart upload. Full support. |
| **QQ** | `uploadMedia` + `sendMediaMessage` | `/files/upload` + `/messages` (msg_type: 7) | Uploads to QQ CDN, sends rich media message. |
| **Feishu** | `uploadImage` + `sendImageMessage` | `/im/v1/images` + `/im/v1/messages` (msg_type: image) | Two-step: upload → get image_key → send. Rate-limit aware. |
| **Slack** | `uploadFile` | Web API `/files.upload` | Deprecated API but functional. Multipart upload. |
| **Discord** | `sendFileMessage` | Channel API `/messages` with multipart | Native file attachment support. |
| **PC (PrivateClaw)** | `resolvePCAttachment` | Custom API | Full attachment map with base64 data. |

### Adapters WITHOUT image outbound (8/14)

| Adapter | Current Behavior | Inbound Image? | API Capability |
|---------|-----------------|----------------|----------------|
| **DingTalk** | Silently dropped | Yes (inbound) | Markdown `![](url)` rendered in markdown messages, but no native image upload in markdown mode |
| **WeCom** | Silently dropped | Yes (inbound) | No direct image send API for bot messages |
| **WeChat** | Silently dropped | No | Official Account API has no bot image send |
| **Matrix** | Silently dropped | Yes (inbound) | m.image event type supported by spec |
| **Signal** | Silently dropped | Yes (inbound) | signal-cli supports `--attachment` flag |
| **WhatsApp** | Silently dropped | Yes (inbound) | WhatsApp Business API supports media messages |
| **Nostr** | Silently dropped | No | NIP-92 imeta or URL in event content |
| **Mattermost** | Silently dropped | Yes (inbound) | REST API `/files` upload + post with file_id |
| **IRC** | Silently dropped | No | DCC SEND (legacy, rarely used in bots) |
| **Twitch** | Silently dropped | No | No image support in IRC interface |

## How Image Extraction Works

All adapters share `ExtractImagesFromText()` from `internal/im/image_extract.go`. This function:
1. Scans text for Markdown image syntax: `![alt](url)` or `![alt](data:image/...;base64,...)`
2. Returns `[]ExtractedImage` with Kind (URL/Base64/Local) and Data
3. Returns remaining text with image markdown stripped

Adapters that support image outbound call this in their `Send()` method, handle each image, then send the remaining text. Adapters without support skip this step — images in the text are silently stripped during markdown rendering.

## Prioritized Implementation Roadmap

### Tier 1: High-impact, well-documented APIs (recommended first)

1. **Matrix** — m.image event
   - Upload to media upload endpoint → mxc:// URI
   - Send `m.room.message` with `msgtype: "m.image"`, `url: mxc://...`
   - ~50 lines, similar to Feishu pattern

2. **Mattermost** — REST API file upload
   - POST `/api/v4/files` (multipart) → file_id
   - POST `/api/v4/posts` with `file_ids: [file_id]`
   - ~60 lines, similar to Discord pattern

3. **Signal** — signal-cli attachment
   - `signal-cli -u NUMBER send --attachment FILEPATH RECIPIENT`
   - Download image → write to temp file → pass as --attachment
   - ~40 lines

### Tier 2: Medium-difficulty, valuable for specific markets

4. **WhatsApp** — WhatsApp Business API
   - Upload media → media ID → send media message
   - Two-step like Feishu, but API requires Business account
   - ~80 lines

5. **Nostr** — NIP-92 imeta tag
   - Add `imeta` tag to text note with URL + dimensions
   - Client-side rendering depends on relay/client support
   - ~30 lines

6. **DingTalk** — Sample Cards with image
   - DingTalk supports `sampleImageMsg` card type
   - Alternative: render markdown images if URL is publicly accessible
   - ~70 lines

### Tier 3: Low-impact or platform-limited

7. **WeCom** — Limited bot image API
   - Bot webhook only supports markdown/text; no image upload
   - Would need to use app API (different auth model)
   - Not feasible without architectural changes

8. **WeChat** — No bot image capability
   - Official Account API has image send but requires different flow
   - Low priority: WeChat adapter is primarily for text Q&A

9. **IRC** — DCC SEND only
   - Not practical for most IRC use cases
   - Could send image URLs as plain text (already happens via markdown)

10. **Twitch** — No image API
    - IRC interface has no image capability
    - Image URLs in text are already clickable

## Recommended Next Steps

1. **Quick win**: Add image URL passthrough for adapters that render markdown. DingTalk already strips `![](url)` to extract the title — consider preserving the raw URL as a fallback link instead.
2. **Medium term**: Implement Matrix + Mattermost + Signal image outbound (highest user demand, well-documented APIs).
3. **Long term**: Evaluate WhatsApp Business API requirements for the WhatsApp adapter.

## Related Code

- `internal/im/image_extract.go` — shared `ExtractImagesFromText()` function
- `internal/im/types.go` — `ProviderContent()` (recently fixed for base64 MIME auto-detection)
- Per-adapter `Send()` methods — image handling varies by provider

## Related Commits (This Cycle)

- `bcba104c` — fix(im): Nostr sendNostrDM propagates context and adds inter-chunk delay
- `64ad64f5` — fix(im): auto-detect MIME type from base64 image data in InboundMessage
