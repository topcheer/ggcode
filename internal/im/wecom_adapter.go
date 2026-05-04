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
	wecomMaxTextLen      = 4000
	wecomDedupMaxSize    = 1000
)

// WeCom WebSocket command constants.
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

	mu        sync.RWMutex
	ws        *websocket.Conn
	connected bool
	closed    bool
	seen      map[string]time.Time // message dedup
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
	return &wecomAdapter{
		name:    name,
		manager: mgr,
		botID:   botID,
		secret:  secret,
		wsURL:   wsURL,
		seen:    make(map[string]time.Time),
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
	defer a.mu.Unlock()
	a.closed = true
	a.connected = false
	if a.ws != nil {
		a.ws.Close()
		a.ws = nil
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
		case <-time.After(delay):
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

	ws, _, err := websocket.DefaultDialer.Dial(a.wsURL, nil)
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
			if err := ws.WriteJSON(pingMsg); err != nil {
				debug.Log("wecom", "adapter=%s heartbeat send failed: %v", a.name, err)
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
	_, _ = chatType, isGroup

	// Extract text
	text := a.extractText(body)
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

	a.manager.HandleInbound(ctx, msg)
}

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
		// App message title
		if strings.EqualFold(msgType, "appmsg") {
			appmsg, _ := body["appmsg"].(map[string]any)
			if title := strings.TrimSpace(jsonStringField(appmsg, "title")); title != "" {
				textParts = append(textParts, title)
			}
		}
	}

	return strings.Join(textParts, "\n")
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

	return attachments
}

// Send delivers an outbound message to a WeCom chat.
func (a *wecomAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	text := event.Text
	if text == "" {
		return nil
	}
	chatID := binding.ChannelID
	if chatID == "" {
		chatID = binding.TargetID
	}
	if chatID == "" {
		return fmt.Errorf("WeCom: no chat_id for binding %s", binding.Adapter)
	}

	a.mu.RLock()
	ws := a.ws
	a.mu.RUnlock()
	if ws == nil {
		return fmt.Errorf("WeCom: not connected")
	}

	// Truncate long messages
	if len(text) > wecomMaxTextLen {
		text = text[:wecomMaxTextLen]
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

	if err := ws.WriteJSON(sendMsg); err != nil {
		return fmt.Errorf("WeCom send: %w", err)
	}
	return nil
}

func (a *wecomAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:      a.name,
		Platform:  PlatformWeCom,
		Healthy:   healthy,
		Status:    status,
		LastError: lastErr,
		UpdatedAt: time.Now(),
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

func chatTypeString(isGroup bool) string {
	if isGroup {
		return "group"
	}
	return "dm"
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
