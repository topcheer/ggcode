package im

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

const (
	wecomDefaultWSURL    = "wss://openws.work.weixin.qq.com"
	wecomConnectTimeout  = 20 * time.Second
	wecomRequestTimeout  = 15 * time.Second
	wecomHeartbeatPeriod = 30 * time.Second
	wecomMaxTextLen      = 2048 // Official: text content max 2048 bytes (https://developer.work.weixin.qq.com/document/path/90236)
	wecomDedupMaxSize    = 1000
	wecomInterMsgDelay   = 600 * time.Millisecond // delay between consecutive proactive sends
)

// WeCom WebSocket command constants (official WeCom AI Bot gateway).
const (
	wecomCmdSubscribe      = "aibot_subscribe"
	wecomCmdCallback       = "aibot_msg_callback"
	wecomCmdLegacyCallback = "aibot_callback"
	wecomCmdEventCallback  = "aibot_event_callback"
	wecomCmdSend           = "aibot_send_msg"
	wecomCmdRespond        = "aibot_respond_msg"
	wecomCmdPing           = "ping"
)

type wecomAdapter struct {
	name    string
	manager *Manager
	botID   string
	secret  string
	wsURL   string

	// Access policies
	dmPolicy       string   // "open", "allowlist", "disabled"
	allowFrom      []string // DM allowlist
	groupPolicy    string   // "open", "allowlist", "disabled"
	groupAllowFrom []string // Group allowlist

	mu          sync.RWMutex
	ws          *websocket.Conn
	writeMu     sync.Mutex // protects websocket writes (gorilla/websocket not concurrent-safe)
	connected   bool
	closed      bool
	seen        map[string]time.Time // message dedup
	replyReqIDs map[string]string    // msgid → req_id for respond_msg replies
}

func newWeComAdapter(name string, _ config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) (*wecomAdapter, error) {
	botID := strings.TrimSpace(stringValue(adapterCfg.Extra, "bot_id", "botid"))
	if botID == "" {
		botID = strings.TrimSpace(os.Getenv("WECOM_BOT_ID"))
	}
	secret := strings.TrimSpace(stringValue(adapterCfg.Extra, "secret"))
	if secret == "" {
		secret = strings.TrimSpace(os.Getenv("WECOM_SECRET"))
	}
	if botID == "" || secret == "" {
		return nil, fmt.Errorf("WeCom bot_id and secret are required for adapter %q", name)
	}
	wsURL := strings.TrimSpace(stringValue(adapterCfg.Extra, "websocket_url", "websocketUrl"))
	if wsURL == "" {
		wsURL = wecomDefaultWSURL
	}

	// Access policies
	dmPolicy := strings.TrimSpace(stringValue(adapterCfg.Extra, "dm_policy", "dmPolicy"))
	if dmPolicy == "" {
		dmPolicy = "open"
	}
	groupPolicy := strings.TrimSpace(stringValue(adapterCfg.Extra, "group_policy", "groupPolicy"))
	if groupPolicy == "" {
		groupPolicy = "open"
	}
	var allowFrom, groupAllowFrom []string
	if v := stringValue(adapterCfg.Extra, "allow_from", "allowFrom"); v != "" {
		allowFrom = strings.Split(v, ",")
	}
	if v := stringValue(adapterCfg.Extra, "group_allow_from", "groupAllowFrom"); v != "" {
		groupAllowFrom = strings.Split(v, ",")
	}

	return &wecomAdapter{
		name:           name,
		manager:        mgr,
		botID:          botID,
		secret:         secret,
		wsURL:          wsURL,
		dmPolicy:       strings.ToLower(dmPolicy),
		allowFrom:      normalizeAllowList(allowFrom),
		groupPolicy:    strings.ToLower(groupPolicy),
		groupAllowFrom: normalizeAllowList(groupAllowFrom),
		seen:           make(map[string]time.Time),
		replyReqIDs:    make(map[string]string),
	}, nil
}

func (a *wecomAdapter) Name() string { return a.name }

func (a *wecomAdapter) Start(ctx context.Context) {
	debug.Log("wecom", "adapter=%s start", a.name)
	a.publishState(false, "connecting", "")
	safego.Go("im.wecom.run", func() { a.run(ctx) })
}

func (a *wecomAdapter) Close() error {
	a.mu.Lock()
	a.closed = true
	a.connected = false
	ws := a.ws
	a.ws = nil
	a.mu.Unlock()
	// Close outside the lock to avoid potential deadlock if ws.Close()
	// triggers internal callbacks that try to acquire a.mu.RLock().
	if ws != nil {
		ws.Close()
	}
	return nil
}

func (a *wecomAdapter) run(ctx context.Context) {
	backoffs := []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second}
	attempt := 0
	for {
		if ctx.Err() != nil {
			a.publishState(false, "stopped", "")
			return
		}
		if err := a.connectAndServe(ctx); err != nil {
			a.publishState(false, "error", err.Error())
			debug.Log("wecom", "adapter=%s error: %v", a.name, err)
		}
		a.mu.RLock()
		isClosed := a.closed
		a.mu.RUnlock()
		if isClosed {
			return
		}
		delay := backoffs[min(attempt, len(backoffs)-1)]
		attempt++
		select {
		case <-ctx.Done():
			a.publishState(false, "stopped", "")
			return
		case <-time.After(jitterDuration(delay)):
		}
	}
}

func (a *wecomAdapter) connectAndServe(ctx context.Context) error {
	if err := a.connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	a.mu.Lock()
	a.connected = true
	a.mu.Unlock()
	a.publishState(true, "connected", "")
	debug.Log("wecom", "adapter=%s connected to %s", a.name, a.wsURL)

	// Start heartbeat
	heartbeatCtx, heartbeatCancel := context.WithCancel(ctx)
	defer heartbeatCancel()
	safego.Go("im.wecom.heartbeat", func() { a.heartbeatLoop(heartbeatCtx) })

	// Read loop
	defer func() {
		a.mu.Lock()
		a.connected = false
		if a.ws != nil {
			a.ws.Close()
			a.ws = nil
		}
		a.mu.Unlock()
	}()

	for {
		if ctx.Err() != nil {
			return nil
		}
		a.mu.RLock()
		ws := a.ws
		a.mu.RUnlock()
		if ws == nil {
			return fmt.Errorf("websocket closed")
		}
		_, msgBytes, err := ws.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(msgBytes, &payload); err != nil {
			continue
		}
		a.dispatchPayload(ctx, payload)
	}
}

func (a *wecomAdapter) connect() error {
	a.mu.Lock()
	if a.ws != nil {
		a.ws.Close()
		a.ws = nil
	}
	a.mu.Unlock()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer dialCancel()
	ws, _, err := websocket.DefaultDialer.DialContext(dialCtx, a.wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial %s: %w", a.wsURL, err)
	}

	// Send subscribe
	reqID := newWeComReqID("subscribe")
	subscribeMsg := map[string]any{
		"cmd":     wecomCmdSubscribe,
		"headers": map[string]any{"req_id": reqID},
		"body":    map[string]any{"bot_id": a.botID, "secret": a.secret},
	}
	if err := ws.WriteJSON(subscribeMsg); err != nil {
		ws.Close()
		return fmt.Errorf("subscribe write: %w", err)
	}

	// Wait for subscribe ack
	ws.SetReadDeadline(time.Now().Add(wecomConnectTimeout))
	_, msgBytes, err := ws.ReadMessage()
	if err != nil {
		ws.Close()
		return fmt.Errorf("subscribe read: %w", err)
	}
	ws.SetReadDeadline(time.Time{})

	var ack map[string]any
	if err := json.Unmarshal(msgBytes, &ack); err != nil {
		ws.Close()
		return fmt.Errorf("subscribe ack parse: %w", err)
	}

	// Skip pings until we get our response
	for {
		cmd, _ := ack["cmd"].(string)
		ackReqID := payloadReqID(ack)
		if cmd == wecomCmdPing {
			ws.SetReadDeadline(time.Now().Add(wecomConnectTimeout))
			_, msgBytes, err = ws.ReadMessage()
			if err != nil {
				ws.Close()
				return fmt.Errorf("subscribe read after ping: %w", err)
			}
			ws.SetReadDeadline(time.Time{})
			if err := json.Unmarshal(msgBytes, &ack); err != nil {
				continue
			}
			continue
		}
		if ackReqID == reqID {
			body, _ := ack["body"].(map[string]any)
			if body != nil {
				errcode, _ := body["errcode"].(float64)
				if errcode != 0 {
					errmsg, _ := body["errmsg"].(string)
					ws.Close()
					return fmt.Errorf("subscribe failed: %s (errcode=%v)", errmsg, errcode)
				}
			}
			break
		}
		// Unknown pre-auth message, read next
		ws.SetReadDeadline(time.Now().Add(wecomConnectTimeout))
		_, msgBytes, err = ws.ReadMessage()
		if err != nil {
			ws.Close()
			return fmt.Errorf("subscribe read: %w", err)
		}
		ws.SetReadDeadline(time.Time{})
		if err := json.Unmarshal(msgBytes, &ack); err != nil {
			continue
		}
	}

	a.mu.Lock()
	a.ws = ws
	a.mu.Unlock()
	return nil
}

func (a *wecomAdapter) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(wecomHeartbeatPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.mu.RLock()
			ws := a.ws
			a.mu.RUnlock()
			if ws == nil {
				continue
			}
			pingMsg := map[string]any{
				"cmd":     wecomCmdPing,
				"headers": map[string]any{"req_id": newWeComReqID("ping")},
				"body":    map[string]any{},
			}
			a.writeMu.Lock()
			err := ws.WriteJSON(pingMsg)
			a.writeMu.Unlock()
			if err != nil {
				debug.Log("wecom", "adapter=%s heartbeat send failed: %v", a.name, err)
				// Close the WebSocket to unblock ReadMessage in the main loop,
				// which triggers the reconnect cycle.
				ws.Close()
				return
			}
		}
	}
}

func (a *wecomAdapter) dispatchPayload(ctx context.Context, payload map[string]any) {
	cmd, _ := payload["cmd"].(string)

	switch cmd {
	case wecomCmdCallback, wecomCmdLegacyCallback:
		a.handleMessage(ctx, payload)
	case wecomCmdEventCallback, wecomCmdPing:
		// Ignore
	default:
		debug.Log("wecom", "adapter=%s ignoring payload cmd=%s", a.name, cmd)
	}
}

func (a *wecomAdapter) handleMessage(ctx context.Context, payload map[string]any) {
	body, _ := payload["body"].(map[string]any)
	if body == nil {
		return
	}

	msgID := jsonStringField(body, "msgid")
	if msgID == "" {
		msgID = payloadReqID(payload)
	}
	if msgID == "" {
		return
	}

	// Dedup
	a.mu.Lock()
	if _, seen := a.seen[msgID]; seen {
		a.mu.Unlock()
		return
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
	}
	// Remember req_id for respond_msg replies
	reqID := payloadReqID(payload)
	if reqID != "" {
		a.replyReqIDs[msgID] = reqID
		if len(a.replyReqIDs) > wecomDedupMaxSize {
			for k := range a.replyReqIDs {
				delete(a.replyReqIDs, k)
				break
			}
		}
	}
	a.mu.Unlock()

	// Extract sender
	from, _ := body["from"].(map[string]any)
	senderID := jsonStringField(from, "userid")
	chatID := jsonStringField(body, "chatid")
	if chatID == "" {
		chatID = senderID
	}
	if chatID == "" {
		return
	}

	chatType, _ := body["chattype"].(string)
	isGroup := strings.EqualFold(chatType, "group")

	// Access policy check
	if isGroup {
		if !a.isGroupAllowed(chatID, senderID) {
			debug.Log("wecom", "adapter=%s group %s sender %s blocked by policy", a.name, chatID, senderID)
			return
		}
	} else {
		if !a.isDMAllowed(senderID) {
			debug.Log("wecom", "adapter=%s DM sender %s blocked by policy", a.name, senderID)
			return
		}
	}

	// Extract text and quote
	text := a.extractText(body)
	quoteText := a.extractQuote(body)

	// If text is empty but we have a quote, use the quote as text
	if text == "" && quoteText != "" {
		text = quoteText
	}
	if text == "" {
		return
	}

	// Extract attachments
	attachments := a.extractAttachments(body)

	msg := InboundMessage{
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformWeCom,
			ChannelID:  chatID,
			SenderID:   senderID,
			SenderName: senderID,
			ReceivedAt: time.Now(),
		},
		Text:        text,
		Attachments: attachments,
	}

	// Pairing flow: first inbound from an unbound channel triggers pairing.
	if a.manager != nil {
		pairingResult, err := a.manager.HandlePairingInbound(msg)
		debug.Log("wecom", "adapter=%s pairing: consumed=%v bound=%v err=%v", a.name, pairingResult.Consumed, pairingResult.Bound, err)
		if err != nil && err != ErrNoSessionBound {
			a.publishState(false, "warning", err.Error())
		}
		if pairingResult.Consumed {
			_ = a.sendText(ctx, chatID, pairingResult.ReplyText)
			if err := a.manager.NotifyPreviousBindingReplaced(ctx, pairingResult); err != nil {
				debug.Log("wecom", "adapter=%s notify previous: %v", a.name, err)
			}
			return
		}
	}

	if a.manager != nil {
		a.manager.HandleInbound(ctx, msg)
	}
}

// extractText extracts plain text content from the callback body.
// Supports text, mixed, voice, and appmsg message types.
func (a *wecomAdapter) extractText(body map[string]any) string {
	msgType, _ := body["msgtype"].(string)
	var textParts []string

	switch strings.ToLower(msgType) {
	case "mixed":
		mixed, _ := body["mixed"].(map[string]any)
		items, _ := mixed["msg_item"].([]any)
		for _, item := range items {
			itemMap, _ := item.(map[string]any)
			if itemMap == nil {
				continue
			}
			if strings.EqualFold(jsonStringField(itemMap, "msgtype"), "text") {
				textBlock, _ := itemMap["text"].(map[string]any)
				if content := strings.TrimSpace(jsonStringField(textBlock, "content")); content != "" {
					textParts = append(textParts, content)
				}
			}
		}
	default:
		textBlock, _ := body["text"].(map[string]any)
		if content := strings.TrimSpace(jsonStringField(textBlock, "content")); content != "" {
			textParts = append(textParts, content)
		}
		// Voice transcription
		if strings.EqualFold(msgType, "voice") {
			voiceBlock, _ := body["voice"].(map[string]any)
			if content := strings.TrimSpace(jsonStringField(voiceBlock, "content")); content != "" {
				textParts = append(textParts, content)
			}
		}
		// App message title (file attachments sent via AI Bot)
		if strings.EqualFold(msgType, "appmsg") {
			appmsg, _ := body["appmsg"].(map[string]any)
			if title := strings.TrimSpace(jsonStringField(appmsg, "title")); title != "" {
				textParts = append(textParts, title)
			}
		}
	}

	return strings.Join(textParts, "\n")
}

// extractQuote extracts the quoted/replied-to text from the callback body.
func (a *wecomAdapter) extractQuote(body map[string]any) string {
	quote, _ := body["quote"].(map[string]any)
	if quote == nil {
		return ""
	}
	quoteType, _ := quote["msgtype"].(string)
	switch strings.ToLower(quoteType) {
	case "text":
		quoteText, _ := quote["text"].(map[string]any)
		return strings.TrimSpace(jsonStringField(quoteText, "content"))
	case "voice":
		quoteVoice, _ := quote["voice"].(map[string]any)
		return strings.TrimSpace(jsonStringField(quoteVoice, "content"))
	}
	return ""
}

func (a *wecomAdapter) extractAttachments(body map[string]any) []Attachment {
	msgType, _ := body["msgtype"].(string)
	var attachments []Attachment

	// Image
	if strings.EqualFold(msgType, "image") {
		if img, _ := body["image"].(map[string]any); img != nil {
			if url := jsonStringField(img, "url"); url != "" {
				attachments = append(attachments, Attachment{
					Kind: AttachmentImage,
					URL:  url,
				})
			}
			// base64 image data
			if b64 := jsonStringField(img, "base64"); b64 != "" {
				attachments = append(attachments, Attachment{
					Kind:       AttachmentImage,
					DataBase64: b64,
				})
			}
		}
	}

	// File
	if strings.EqualFold(msgType, "file") {
		if file, _ := body["file"].(map[string]any); file != nil {
			if url := jsonStringField(file, "url"); url != "" {
				attachments = append(attachments, Attachment{
					Kind: AttachmentFile,
					URL:  url,
				})
			}
		}
	}

	// App message file/image (AI Bot attachments like PDF/Word/Excel)
	if strings.EqualFold(msgType, "appmsg") {
		appmsg, _ := body["appmsg"].(map[string]any)
		if appmsg != nil {
			if file, _ := appmsg["file"].(map[string]any); file != nil {
				if url := jsonStringField(file, "url"); url != "" {
					attachments = append(attachments, Attachment{
						Kind: AttachmentFile,
						URL:  url,
						Name: jsonStringField(appmsg, "title"),
					})
				}
			}
			if img, _ := appmsg["image"].(map[string]any); img != nil {
				if url := jsonStringField(img, "url"); url != "" {
					attachments = append(attachments, Attachment{
						Kind: AttachmentImage,
						URL:  url,
					})
				}
			}
		}
	}

	// Quote image/file
	if quote, _ := body["quote"].(map[string]any); quote != nil {
		quoteType, _ := quote["msgtype"].(string)
		switch strings.ToLower(quoteType) {
		case "image":
			if img, _ := quote["image"].(map[string]any); img != nil {
				if url := jsonStringField(img, "url"); url != "" {
					attachments = append(attachments, Attachment{
						Kind: AttachmentImage,
						URL:  url,
					})
				}
			}
		case "file":
			if file, _ := quote["file"].(map[string]any); file != nil {
				if url := jsonStringField(file, "url"); url != "" {
					attachments = append(attachments, Attachment{
						Kind: AttachmentFile,
						URL:  url,
					})
				}
			}
		}
	}

	return attachments
}

// --- Access policy ---

func (a *wecomAdapter) isDMAllowed(senderID string) bool {
	if a.dmPolicy == "disabled" {
		return false
	}
	if a.dmPolicy == "allowlist" {
		return entryMatches(a.allowFrom, senderID)
	}
	return true // "open"
}

func (a *wecomAdapter) isGroupAllowed(chatID, senderID string) bool {
	if a.groupPolicy == "disabled" {
		return false
	}
	if a.groupPolicy == "allowlist" && !entryMatches(a.groupAllowFrom, chatID) {
		return false
	}
	return true
}

// --- Outbound ---

// Send delivers an outbound message to a WeCom chat.
// Prefers aibot_respond_msg (stream reply) when the inbound message's req_id
// is still tracked, which shows a "reply" bubble in the WeCom UI.
// Falls back to aibot_send_msg (proactive message) otherwise.
func (a *wecomAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	chatID := binding.ChannelID
	if chatID == "" {
		chatID = binding.TargetID
	}
	if chatID == "" {
		return nil
	}
	text := a.outboundText(event)
	if text == "" {
		return nil
	}

	// Split long messages instead of truncating — losing content is worse UX
	// than sending multiple messages. WeCom limit is 2048 bytes per message.
	// Note: do NOT strip markdown here — sendProactive uses msgtype=markdown
	// which renders formatting. sendRespond strips markdown per-call since
	// the stream msgtype renders plain text only.
	chunks := SplitMessageForPlatform(text, PlatformWeCom)

	// First chunk: try respond_msg if we have a tracked req_id.
	// Subsequent chunks: must use proactive API (respond_msg has reply limits).
	hasReply := false
	if msgID := strings.TrimSpace(binding.LastInboundMessageID); msgID != "" {
		a.mu.RLock()
		reqID, ok := a.replyReqIDs[msgID]
		a.mu.RUnlock()
		if ok && reqID != "" {
			if err := a.sendRespond(chatID, reqID, chunks[0]); err != nil {
				return err
			}
			hasReply = true
		}
	}

	firstProactive := true
	for i := 0; i < len(chunks); i++ {
		if hasReply && i == 0 {
			continue // already sent via respond_msg
		}
		if !firstProactive {
			select {
			case <-time.After(wecomInterMsgDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		firstProactive = false
		if err := a.sendProactive(chatID, chunks[i]); err != nil {
			return err
		}
	}
	return nil
}

// TriggerTyping implements the TypingIndicator interface.
// WeCom AI Bot does not have a native typing API, but we can send an
// intermediate respond_msg with finish=false to show a "thinking" state.
func (a *wecomAdapter) TriggerTyping(ctx context.Context, binding ChannelBinding) error {
	msgID := strings.TrimSpace(binding.LastInboundMessageID)
	if msgID == "" {
		return nil
	}
	a.mu.RLock()
	reqID, ok := a.replyReqIDs[msgID]
	a.mu.RUnlock()
	if !ok || reqID == "" {
		return nil
	}

	a.mu.RLock()
	ws := a.ws
	a.mu.RUnlock()
	if ws == nil {
		return nil
	}

	typingMsg := map[string]any{
		"cmd":     wecomCmdRespond,
		"headers": map[string]any{"req_id": reqID},
		"body": map[string]any{
			"msgtype": "stream",
			"stream": map[string]any{
				"id":      newWeComReqID("stream"),
				"finish":  false,
				"content": "",
			},
		},
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	return ws.WriteJSON(typingMsg)
}

// sendRespond sends via aibot_respond_msg — shows as a reply bubble in WeCom.
func (a *wecomAdapter) sendRespond(chatID, replyReqID, text string) error {
	a.mu.RLock()
	ws := a.ws
	a.mu.RUnlock()
	if ws == nil {
		return fmt.Errorf("WeCom: not connected")
	}

	// stream msgtype renders plain text only — strip markdown formatting
	text = stripMarkdown(text)

	respondMsg := map[string]any{
		"cmd":     wecomCmdRespond,
		"headers": map[string]any{"req_id": replyReqID},
		"body": map[string]any{
			"msgtype": "stream",
			"stream": map[string]any{
				"id":      newWeComReqID("stream"),
				"finish":  true,
				"content": text,
			},
		},
	}
	a.writeMu.Lock()
	err := ws.WriteJSON(respondMsg)
	a.writeMu.Unlock()
	if err != nil {
		// Fall back to proactive send on respond failure
		debug.Log("wecom", "adapter=%s respond_msg failed: %v, falling back to send_msg", a.name, err)
		return a.sendProactive(chatID, text)
	}
	return nil
}

// sendProactive sends via aibot_send_msg — a standalone message.
func (a *wecomAdapter) sendProactive(chatID, text string) error {
	a.mu.RLock()
	ws := a.ws
	a.mu.RUnlock()
	if ws == nil {
		return fmt.Errorf("WeCom: not connected")
	}

	sendMsg := map[string]any{
		"cmd":     wecomCmdSend,
		"headers": map[string]any{"req_id": newWeComReqID("send")},
		"body": map[string]any{
			"chatid":  chatID,
			"msgtype": "markdown",
			"markdown": map[string]any{
				"content": text,
			},
		},
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	return ws.WriteJSON(sendMsg)
}

// sendText is a convenience wrapper used for pairing replies (no inbound to correlate).
func (a *wecomAdapter) sendText(ctx context.Context, chatID, text string) error {
	if text == "" || chatID == "" {
		return nil
	}
	return a.sendProactive(chatID, text)
}

func (a *wecomAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	contactURI := ""
	if a.botID != "" {
		contactURI = "https://work.weixin.qq.com/wework_admin/chat?botId=" + a.botID
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:       a.name,
		Platform:   PlatformWeCom,
		Healthy:    healthy,
		Status:     status,
		LastError:  lastErr,
		ContactURI: contactURI,
		UpdatedAt:  time.Now(),
	})
}

// --- Helpers ---

func newWeComReqID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func payloadReqID(payload map[string]any) string {
	headers, _ := payload["headers"].(map[string]any)
	if headers == nil {
		return ""
	}
	reqID, _ := headers["req_id"].(string)
	return reqID
}

// jsonStringField safely extracts a string field from a nested map.
func jsonStringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case json.Number:
		return val.String()
	default:
		return fmt.Sprintf("%v", val)
	}
}

// normalizeAllowList trims and filters a list of allowlist entries.
func normalizeAllowList(entries []string) []string {
	var result []string
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e != "" {
			result = append(result, e)
		}
	}
	return result
}

// entryMatches checks if a target matches any entry in an allowlist (case-insensitive, supports "*").
func entryMatches(entries []string, target string) bool {
	t := strings.TrimSpace(strings.ToLower(target))
	for _, e := range entries {
		e = strings.TrimSpace(strings.ToLower(e))
		if e == "*" || e == t {
			return true
		}
	}
	return false
}

// outboundText converts an OutboundEvent to display text for WeCom.
func (a *wecomAdapter) outboundText(event OutboundEvent) string {
	switch event.Kind {
	case OutboundEventText:
		return event.Text
	case OutboundEventStatus:
		return event.Status
	case OutboundEventToolCall:
		if event.ToolCall == nil {
			return ""
		}
		return formatToolCallText(event.ToolCall)
	case OutboundEventToolResult:
		if event.ToolRes == nil {
			return ""
		}
		return formatToolResultText(event.ToolRes)
	default:
		return event.Text
	}
}
