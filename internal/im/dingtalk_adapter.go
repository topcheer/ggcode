package im

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

const (
	dingtalkAPIBase    = "https://api.dingtalk.com"
	dingtalkMaxTextLen = 4000
)

type dingtalkAdapter struct {
	name       string
	manager    *Manager
	httpClient *http.Client
	appKey     string
	appSecret  string

	mu          sync.RWMutex
	connected   bool
	accessToken string
	tokenExpire time.Time
	ws          *websocket.Conn
}

func newDingTalkAdapter(name string, _ config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) (*dingtalkAdapter, error) {
	appKey := strings.TrimSpace(stringValue(adapterCfg.Extra, "app_key", "appKey"))
	if appKey == "" {
		return nil, fmt.Errorf("DingTalk app_key is required for adapter %q", name)
	}
	appSecret := strings.TrimSpace(stringValue(adapterCfg.Extra, "app_secret", "appSecret"))
	if appSecret == "" {
		return nil, fmt.Errorf("DingTalk app_secret is required for adapter %q", name)
	}
	return &dingtalkAdapter{
		name:       name,
		manager:    mgr,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		appKey:     appKey,
		appSecret:  appSecret,
	}, nil
}

func (a *dingtalkAdapter) Name() string { return a.name }

func (a *dingtalkAdapter) Start(ctx context.Context) {
	debug.Log("dingtalk", "adapter=%s start appKey=%s", a.name, a.appKey)
	a.publishState(false, "connecting", "")
	go a.run(ctx)
}

func (a *dingtalkAdapter) run(ctx context.Context) {
	backoffs := []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second}
	attempt := 0
	for {
		if ctx.Err() != nil {
			a.publishState(false, "stopped", "")
			return
		}
		if err := a.connectAndServe(ctx); err != nil {
			a.publishState(false, "error", err.Error())
			debug.Log("dingtalk", "adapter=%s error: %v", a.name, err)
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

func (a *dingtalkAdapter) connectAndServe(ctx context.Context) error {
	// Get access token
	if err := a.refreshToken(ctx); err != nil {
		return fmt.Errorf("get access token: %w", err)
	}

	// Open Stream connection
	wsURL, err := a.streamOpen(ctx)
	if err != nil {
		return fmt.Errorf("stream open: %w", err)
	}
	debug.Log("dingtalk", "adapter=%s stream endpoint=%s", a.name, wsURL)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial stream: %w", err)
	}
	defer func() {
		a.mu.Lock()
		a.connected = false
		a.ws = nil
		a.mu.Unlock()
		conn.Close()
	}()

	a.mu.Lock()
	a.ws = conn
	a.connected = true
	a.mu.Unlock()
	a.publishState(true, "connected", "")
	debug.Log("dingtalk", "adapter=%s connected", a.name)

	// Token refresh loop
	go a.tokenRefreshLoop(ctx)

	// Read loop
	for {
		if ctx.Err() != nil {
			return nil
		}
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read websocket: %w", err)
		}
		var msg map[string]any
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			continue
		}

		headers, _ := msg["headers"].(map[string]any)
		if headers == nil {
			continue
		}
		contentType, _ := headers["contentType"].(string)
		topic, _ := headers["topic"].(string)
		messageID, _ := headers["messageId"].(string)

		if topic == "/v1.0/im/bot/messages/get" {
			body, _ := msg["body"].(string)
			if body != "" {
				a.handleBotMessage(ctx, body, messageID, conn)
			}
		}

		_ = contentType
	}
}

func (a *dingtalkAdapter) refreshToken(ctx context.Context) error {
	url := dingtalkAPIBase + "/v1.0/oauth2/accessToken"
	body := map[string]any{
		"appKey":    a.appKey,
		"appSecret": a.appSecret,
	}
	bodyBytes, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return err
	}
	token, _ := result["accessToken"].(string)
	if token == "" {
		return fmt.Errorf("DingTalk accessToken is empty")
	}
	expire, _ := intValue(result["expireIn"])
	if expire <= 0 {
		expire = 7200
	}
	a.mu.Lock()
	a.accessToken = token
	a.tokenExpire = time.Now().Add(time.Duration(expire) * time.Second)
	a.mu.Unlock()
	debug.Log("dingtalk", "adapter=%s token refreshed, expires in %ds", a.name, expire)
	return nil
}

func (a *dingtalkAdapter) tokenRefreshLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.mu.RLock()
			expire := a.tokenExpire
			a.mu.RUnlock()
			if time.Until(expire) < 5*time.Minute {
				if err := a.refreshToken(ctx); err != nil {
					debug.Log("dingtalk", "adapter=%s token refresh error: %v", a.name, err)
				}
			}
		}
	}
}

func (a *dingtalkAdapter) streamOpen(ctx context.Context) (string, error) {
	url := dingtalkAPIBase + "/v1.0/gateway/connections/open"
	a.mu.RLock()
	token := a.accessToken
	a.mu.RUnlock()
	body := map[string]any{
		"clientId": a.appKey,
		"subscriptions": []map[string]any{
			{
				"type":  "EVENT",
				"topic": "/v1.0/im/bot/messages/get",
			},
		},
	}
	bodyBytes, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Acs-Dingtalk-Access-Token", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return "", err
	}
	endpoint, _ := result["endpoint"].(string)
	if endpoint == "" {
		return "", fmt.Errorf("DingTalk stream endpoint is empty: %s", strings.TrimSpace(string(data)))
	}
	return endpoint, nil
}

func (a *dingtalkAdapter) handleBotMessage(ctx context.Context, body string, messageID string, conn *websocket.Conn) {
	var msgData map[string]any
	if err := json.Unmarshal([]byte(body), &msgData); err != nil {
		debug.Log("dingtalk", "adapter=%s parse bot message error: %v", a.name, err)
		return
	}

	sender, _ := msgData["sender"].(map[string]any)
	conversationID, _ := msgData["conversationId"].(string)
	var senderID, senderNick string
	if sender != nil {
		senderID, _ = sender["senderId"].(string)
		senderNick, _ = sender["nick"].(string)
		if senderNick == "" {
			st, _ := sender["senderType"].(string)
			if st == "" {
				senderNick = senderID
			}
		}
	}

	textContent, _ := msgData["text"].(map[string]any)
	var text string
	if textContent != nil {
		content, _ := textContent["content"].(string)
		text = content
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	debug.Log("dingtalk", "adapter=%s inbound conversation=%s sender=%s len=%d", a.name, conversationID, senderID, len(text))

	// Send ack response
	if conn != nil && messageID != "" {
		ack := map[string]any{
			"code":    200,
			"headers": map[string]string{"contentType": "application/json"},
			"message": "",
			"data":    map[string]string{"response": "success"},
		}
		ackData, _ := json.Marshal(ack)
		_ = conn.WriteMessage(1, ackData)
	}

	inbound := InboundMessage{
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformDingTalk,
			ChannelID:  conversationID,
			SenderID:   senderID,
			SenderName: senderNick,
			MessageID:  messageID,
			ReceivedAt: time.Now(),
		},
		Text: text,
	}

	pairingResult, err := a.manager.HandlePairingInbound(inbound)
	if err != nil && err != ErrNoSessionBound {
		a.publishState(false, "warning", err.Error())
	}
	if pairingResult.Consumed {
		if sendErr := a.sendGroupMessage(ctx, conversationID, pairingResult.ReplyText); sendErr != nil {
			a.publishState(false, "warning", sendErr.Error())
		}
		return
	}

	if err := a.manager.HandleInbound(ctx, inbound); err != nil {
		if err == ErrInboundChannelDenied {
			debug.Log("dingtalk", "adapter=%s unauthorized inbound conversation=%s", a.name, conversationID)
			return
		}
		if err != ErrNoChannelBound {
			a.publishState(false, "warning", err.Error())
		}
	}
}

func (a *dingtalkAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	a.mu.RLock()
	connected := a.connected
	a.mu.RUnlock()
	if !connected {
		return fmt.Errorf("DingTalk bot %q is not online", a.name)
	}
	conversationID := strings.TrimSpace(binding.ChannelID)
	if conversationID == "" {
		return fmt.Errorf("DingTalk channel is not configured for current directory")
	}
	content := strings.TrimSpace(a.outboundText(event))
	if content == "" {
		return nil
	}

	// Extract images and send them
	images, remainingText := ExtractImagesFromText(content)
	for _, img := range images {
		if err := a.sendExtractedImage(ctx, conversationID, img); err != nil {
			debug.Log("dingtalk", "adapter=%s image send failed: %v", a.name, err)
		}
	}

	// Send remaining text as markdown
	remainingText = strings.TrimSpace(remainingText)
	if remainingText == "" {
		return nil
	}
	chunks := splitDingTalkMessage(remainingText, dingtalkMaxTextLen)
	for _, chunk := range chunks {
		if err := a.sendGroupMessage(ctx, conversationID, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (a *dingtalkAdapter) outboundText(event OutboundEvent) string {
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
	case OutboundEventApprovalRequest:
		if event.Approval == nil {
			return ""
		}
		return fmt.Sprintf("[approval] %s\n%s", event.Approval.ToolName, event.Approval.Input)
	case OutboundEventApprovalResult:
		if event.Result == nil {
			return ""
		}
		return fmt.Sprintf("[approval result] %s", event.Result.Decision)
	default:
		return ""
	}
}

func (a *dingtalkAdapter) sendGroupMessage(ctx context.Context, conversationID, content string) error {
	a.mu.RLock()
	token := a.accessToken
	a.mu.RUnlock()

	// Use markdown format for rich content
	msgParam, _ := json.Marshal(map[string]string{"title": "", "text": content})

	if strings.HasPrefix(conversationID, "cid") {
		// Group message
		url := dingtalkAPIBase + "/v1.0/robot/groupMessages/send"
		body := map[string]any{
			"robotCode":      a.appKey,
			"conversationId": conversationID,
			"msgKey":         "sampleMarkdown",
			"msgParam":       string(msgParam),
		}
		return a.doDingTalkPost(ctx, url, token, body)
	}

	// Single chat message — conversationID is a userId
	url := dingtalkAPIBase + "/v1.0/robot/oToMessages/batchSend"
	body := map[string]any{
		"robotCode": a.appKey,
		"userIds":   []string{conversationID},
		"msgKey":    "sampleMarkdown",
		"msgParam":  string(msgParam),
	}
	return a.doDingTalkPost(ctx, url, token, body)
}

// sendExtractedImage resolves and sends an extracted image via DingTalk.
func (a *dingtalkAdapter) sendExtractedImage(ctx context.Context, conversationID string, img ExtractedImage) error {
	switch img.Kind {
	case "url":
		if IsLocalFilePath(img.Data) {
			// DingTalk image messages require a URL, can't upload local files directly
			debug.Log("dingtalk", "adapter=%s skipping local file image (not supported)", a.name)
			return nil
		}
		return a.sendImageMessage(ctx, conversationID, img.Data)
	case "data_url":
		// DingTalk doesn't support base64 uploads directly
		debug.Log("dingtalk", "adapter=%s skipping data URL image (not supported)", a.name)
		return nil
	default:
		return fmt.Errorf("unknown image kind: %s", img.Kind)
	}
}

// sendImageMessage sends an image message using DingTalk's sampleImageMsg.
func (a *dingtalkAdapter) sendImageMessage(ctx context.Context, conversationID, photoURL string) error {
	a.mu.RLock()
	token := a.accessToken
	a.mu.RUnlock()

	msgParam, _ := json.Marshal(map[string]string{"photoURL": photoURL})

	if strings.HasPrefix(conversationID, "cid") {
		url := dingtalkAPIBase + "/v1.0/robot/groupMessages/send"
		body := map[string]any{
			"robotCode":      a.appKey,
			"conversationId": conversationID,
			"msgKey":         "sampleImageMsg",
			"msgParam":       string(msgParam),
		}
		return a.doDingTalkPost(ctx, url, token, body)
	}

	// Single chat
	url := dingtalkAPIBase + "/v1.0/robot/oToMessages/batchSend"
	body := map[string]any{
		"robotCode": a.appKey,
		"userIds":   []string{conversationID},
		"msgKey":    "sampleImageMsg",
		"msgParam":  string(msgParam),
	}
	return a.doDingTalkPost(ctx, url, token, body)
}

func (a *dingtalkAdapter) doDingTalkPost(ctx context.Context, url, token string, body map[string]any) error {
	bodyBytes, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("x-acs-dingtalk-access-token", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("DingTalk API [%d] %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func (a *dingtalkAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:      a.name,
		Platform:  PlatformDingTalk,
		Healthy:   healthy,
		Status:    status,
		LastError: lastErr,
		UpdatedAt: time.Now(),
	})
}

func splitDingTalkMessage(text string, maxLen int) []string {
	text = strings.TrimSpace(text)
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
		if idx := strings.LastIndex(text[:maxLen], "\n"); idx > 0 {
			splitAt = idx + 1
		}
		chunks = append(chunks, text[:splitAt])
		text = text[splitAt:]
	}
	return chunks
}
