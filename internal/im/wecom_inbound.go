package im

import (
	"context"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Inbound seam helpers for the WeCom adapter, extracted r182 from
// wecom_adapter.go handleMessage / extractAttachments (both CC 30, the
// internal/im complexity tops at fork time). Mechanical extraction: logic,
// comment anchors (#974/#1252/#1253/#1255/#1567-D), debug lines and
// append-ordering semantics are byte-identical to the pre-refactor bodies.

// wecomMsgID resolves the message dedup key: the callback body msgid first,
// falling back to the gateway req_id (pre-refactor handleMessage contract).
func wecomMsgID(body, payload map[string]any) string {
	msgID := jsonStringField(body, "msgid")
	if msgID == "" {
		msgID = payloadReqID(payload)
	}
	return msgID
}

// dedupeAndRemember holds a.mu for the whole dedup + req_id bookkeeping
// critical section: it drops already-seen msgids (returns false), records the
// new one, prunes the seen map (TTL eviction plus the #1567-D oldest-entry
// fallback), and remembers req_id for respond_msg replies with oldest-first
// eviction (#974).
func (a *wecomAdapter) dedupeAndRemember(msgID, reqID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, seen := a.seen[msgID]; seen {
		return false
	}
	a.seen[msgID] = time.Now()
	// Evict old entries
	if len(a.seen) > wecomDedupMaxSize {
		cutoff := time.Now().Add(-5 * time.Minute)
		for k, t := range a.seen {
			if t.Before(cutoff) {
				delete(a.seen, k)
			}
		}
		// #1567-D (#974 pattern, missed here): a burst inside the TTL window
		// leaves the map over-capacity with no oldest-entry fallback - it
		// grows unbounded until entries age out. Drop oldest by timestamp.
		for len(a.seen) >= wecomDedupMaxSize {
			var oldestKey string
			var oldestT time.Time
			for k, t := range a.seen {
				if oldestKey == "" || t.Before(oldestT) {
					oldestKey, oldestT = k, t
				}
			}
			delete(a.seen, oldestKey)
		}
	}
	// Remember req_id for respond_msg replies
	if reqID != "" {
		a.replyReqIDs[msgID] = reqID
		a.replyReqOrder = append(a.replyReqOrder, msgID)
		// Evict oldest-first instead of a random map entry (#974): random
		// eviction could drop exactly the req_id we are about to reply to.
		for len(a.replyReqIDs) > wecomDedupMaxSize && len(a.replyReqOrder) > 0 {
			oldest := a.replyReqOrder[0]
			a.replyReqOrder = a.replyReqOrder[1:]
			delete(a.replyReqIDs, oldest)
		}
	}
	return true
}

// wecomInboundSender extracts the sender and chat ids from the callback body;
// a missing chatid falls back to the sender (DMs), and isGroup uses a
// case-insensitive chattype match.
func wecomInboundSender(body map[string]any) (senderID, chatID string, isGroup bool) {
	from, _ := body["from"].(map[string]any)
	senderID = jsonStringField(from, "userid")
	chatID = jsonStringField(body, "chatid")
	if chatID == "" {
		chatID = senderID
	}
	chatType, _ := body["chattype"].(string)
	isGroup = strings.EqualFold(chatType, "group")
	return senderID, chatID, isGroup
}

// passWecomPolicy applies the group/DM access gates, keeping the two
// policy-blocked debug lines verbatim.
func (a *wecomAdapter) passWecomPolicy(chatID, senderID string, isGroup bool) bool {
	if isGroup {
		if !a.isGroupAllowed(chatID, senderID) {
			debug.Log("wecom", "adapter=%s group %s sender %s blocked by policy", a.name, chatID, senderID)
			return false
		}
		return true
	}
	if !a.isDMAllowed(senderID) {
		debug.Log("wecom", "adapter=%s DM sender %s blocked by policy", a.name, senderID)
		return false
	}
	return true
}

// wecomInboundMessage assembles the platform inbound envelope for a WeCom
// callback (SenderName mirrors SenderID, per the pre-refactor literal).
func wecomInboundMessage(name, chatID, senderID, text string, attachments []Attachment, receivedAt time.Time) InboundMessage {
	return InboundMessage{
		Envelope: Envelope{
			Adapter:    name,
			Platform:   PlatformWeCom,
			ChannelID:  chatID,
			SenderID:   senderID,
			SenderName: senderID,
			ReceivedAt: receivedAt,
		},
		Text:        text,
		Attachments: attachments,
	}
}

// deliverWecomInbound runs the pairing flow first and falls back to the
// regular inbound pipeline. Pairing errors stay log-only (#1255): the
// websocket connection is unaffected and connected state must not flip.
func (a *wecomAdapter) deliverWecomInbound(ctx context.Context, msg InboundMessage, chatID string) {
	if a.manager == nil {
		return
	}
	pairingResult, err := a.manager.HandlePairingInbound(msg)
	debug.Log("wecom", "adapter=%s pairing: consumed=%v bound=%v err=%v", a.name, pairingResult.Consumed, pairingResult.Bound, err)
	if err != nil && err != ErrNoSessionBound {
		// #1255: message-level pairing errors do not flip the adapter state
		// (#1238/#1243/#1248 family, 4th instance): connected is published
		// only once on websocket login and nothing re-publishes it while the
		// connection lives, so one transient store hiccup pinned a healthy
		// adapter at warning until the next reconnect. Log only.
		debug.Log("wecom", "adapter=%s pairing error (websocket unaffected): %v", a.name, err)
	}
	if pairingResult.Consumed {
		_ = a.sendText(ctx, chatID, pairingResult.ReplyText)
		if err := a.manager.NotifyPreviousBindingReplaced(ctx, pairingResult); err != nil {
			debug.Log("wecom", "adapter=%s notify previous: %v", a.name, err)
		}
		return
	}
	a.manager.HandleInbound(ctx, msg)
}

// wecomImageBlockAttachments collects url/base64 image attachments from a
// WeCom image block; top-level image messages and mixed msg_item entries
// share this shape.
func wecomImageBlockAttachments(img map[string]any) []Attachment {
	var out []Attachment
	if img == nil {
		return out
	}
	if url := jsonStringField(img, "url"); url != "" {
		out = append(out, Attachment{
			Kind: AttachmentImage,
			URL:  url,
		})
	}
	// base64 image data
	if b64 := jsonStringField(img, "base64"); b64 != "" {
		out = append(out, Attachment{
			Kind:       AttachmentImage,
			DataBase64: b64,
		})
	}
	return out
}

// wecomMixedImageAttachments walks mixed msg_item entries and collects every
// image item (#1253: per-item msgtypes; text + screenshot compositions must
// not lose the screenshot).
func wecomMixedImageAttachments(mixed map[string]any) []Attachment {
	var out []Attachment
	if mixed == nil {
		return out
	}
	items, _ := mixed["msg_item"].([]any)
	if items == nil {
		return out
	}
	for _, item := range items {
		itemMap, _ := item.(map[string]any)
		if itemMap == nil || !strings.EqualFold(jsonStringField(itemMap, "msgtype"), "image") {
			continue
		}
		img, _ := itemMap["image"].(map[string]any)
		if img == nil {
			continue
		}
		out = append(out, wecomImageBlockAttachments(img)...)
	}
	return out
}

// wecomFileURLAttachment collects the file url attachment when present.
func wecomFileURLAttachment(file map[string]any) []Attachment {
	var out []Attachment
	if file == nil {
		return out
	}
	if url := jsonStringField(file, "url"); url != "" {
		out = append(out, Attachment{
			Kind: AttachmentFile,
			URL:  url,
		})
	}
	return out
}

// wecomAppmsgAttachments extracts AI Bot app message attachments: the file
// block carries the appmsg title as attachment Name; the image block is a
// plain image.
func wecomAppmsgAttachments(appmsg map[string]any) []Attachment {
	var out []Attachment
	if appmsg == nil {
		return out
	}
	if file, _ := appmsg["file"].(map[string]any); file != nil {
		if url := jsonStringField(file, "url"); url != "" {
			out = append(out, Attachment{
				Kind: AttachmentFile,
				URL:  url,
				Name: jsonStringField(appmsg, "title"),
			})
		}
	}
	if img, _ := appmsg["image"].(map[string]any); img != nil {
		if url := jsonStringField(img, "url"); url != "" {
			out = append(out, Attachment{
				Kind: AttachmentImage,
				URL:  url,
			})
		}
	}
	return out
}

// wecomQuoteAttachments collects image/file attachments carried on a quoted
// message; other quote types contribute nothing.
func wecomQuoteAttachments(quote map[string]any) []Attachment {
	var out []Attachment
	if quote == nil {
		return out
	}
	quoteType, _ := quote["msgtype"].(string)
	switch strings.ToLower(quoteType) {
	case "image":
		if img, _ := quote["image"].(map[string]any); img != nil {
			if url := jsonStringField(img, "url"); url != "" {
				out = append(out, Attachment{
					Kind: AttachmentImage,
					URL:  url,
				})
			}
		}
	case "file":
		if file, _ := quote["file"].(map[string]any); file != nil {
			if url := jsonStringField(file, "url"); url != "" {
				out = append(out, Attachment{
					Kind: AttachmentFile,
					URL:  url,
				})
			}
		}
	}
	return out
}
