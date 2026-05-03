package im

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

const (
	dingtalkAPIBase = "https://api.dingtalk.com"

	// DataFrame types (from official SDK payload/data_frame.go)
	dingtalkSubCallback = "CALLBACK"
	dingtalkSubEvent    = "EVENT"

	// DataFrame header keys
	dfHeaderTopic       = "topic"
	dfHeaderContentType = "contentType"
	dfHeaderMessageID   = "messageId"
	dfHeaderTime        = "time"

	dfContentTypeJSON = "application/json"

	dfStatusOK = 200

	// Callback topics (from official SDK payload/utils.go)
	dingtalkBotCallbackTopic      = "/v1.0/im/bot/messages/get"
	dingtalkCardCallbackTopic     = "/v1.0/card/instances/callback"
	dingtalkSystemPingTopic       = "ping"
	dingtalkSystemDisconnectTopic = "disconnect"

	// Heartbeat and reconnect intervals (from official SDK)
	wsPingInterval = 120 * time.Second
	wsPongWait     = 5 * time.Second
	reconnectDelay = 3 * time.Second
)

// ---- DataFrame protocol ----

type dingtalkDataFrame struct {
	SpecVersion string            `json:"specVersion"`
	Type        string            `json:"type"`
	Headers     map[string]string `json:"headers"`
	Data        string            `json:"data"`
}

type dingtalkDataFrameResponse struct {
	Code    int               `json:"code"`
	Headers map[string]string `json:"headers"`
	Data    string            `json:"data"`
	Message string            `json:"message,omitempty"`
}

// ---- Bot callback message (parsed from DataFrame.Data) ----

type dingtalkBotCallbackData struct {
	ConversationID   string `json:"conversationId"`
	ChatbotUserID    string `json:"chatbotUserId"`
	ChatbotCorpID    string `json:"chatbotCorpId"`
	MsgID            string `json:"msgId"`
	SenderNick       string `json:"senderNick"`
	SenderID         string `json:"senderId"`
	SenderStaffID    string `json:"senderStaffId"`
	SessionWebhook   string `json:"sessionWebhook"`
	IsAdmin          bool   `json:"isAdmin"`
	ConversationType string `json:"conversationType"`
	AtUsers          []struct {
		DingtalkID string `json:"dingtalkId"`
	} `json:"atUsers"`
	IsInAtList     bool   `json:"isInAtList"`
	ChatbotUnionID string `json:"chatbotUnionId"`
	Text           struct {
		Content string `json:"content"`
	} `json:"text"`
	RobotCode string `json:"robotCode"`
	MsgType   string `json:"msgtype"`
}

// ---- dingtalkAdapter ----

type dingtalkAdapter struct {
	name      string
	manager   *Manager
	appKey    string
	appSecret string

	mu            sync.RWMutex
	accessToken   string
	tokenExpire   time.Time
	ws            *websocket.Conn
	connected     bool
	cancel        context.CancelFunc
	lastWebhook   string // Latest sessionWebhook from callback
	lastRobotCode string // Latest robotCode from callback
}

func newDingtalkAdapter(name string, mgr *Manager, adapterCfg config.IMAdapterConfig) (*dingtalkAdapter, error) {
	appKeyVal, _ := adapterCfg.Extra["app_key"]
	appSecretVal, _ := adapterCfg.Extra["app_secret"]
	appKey := strings.TrimSpace(fmt.Sprintf("%v", appKeyVal))
	appSecret := strings.TrimSpace(fmt.Sprintf("%v", appSecretVal))
	if appKey == "" || appKey == "<nil>" || appSecret == "" || appSecret == "<nil>" {
		return nil, fmt.Errorf("dingtalk adapter requires app_key and app_secret")
	}
	return &dingtalkAdapter{
		name:      name,
		manager:   mgr,
		appKey:    appKey,
		appSecret: appSecret,
	}, nil
}

// ---- Sink interface ----

func (a *dingtalkAdapter) Name() string {
	return a.name
}

func (a *dingtalkAdapter) Start(ctx context.Context) {
	debug.Log("dingtalk", "adapter=%s start appKey=%s", a.name, a.appKey)
	a.publishState(false, "connecting", "")
	childCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.cancel = cancel
	a.mu.Unlock()
	go a.run(childCtx)
}

func (a *dingtalkAdapter) Stop() {
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
	}
	a.connected = false
	a.ws = nil
	a.mu.Unlock()
}

// Close implements io.Closer for adapter lifecycle.
func (a *dingtalkAdapter) Close() error {
	a.Stop()
	return nil
}

// ---- Main run loop with reconnect ----

func (a *dingtalkAdapter) run(ctx context.Context) {
	// Start token refresh goroutine
	go a.tokenRefresher(ctx)

	backoffs := []time.Duration{3 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second}
	attempt := 0

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := a.connectAndServe(ctx)
		if ctx.Err() != nil {
			return
		}

		a.mu.Lock()
		a.connected = false
		a.ws = nil
		a.mu.Unlock()
		a.publishState(false, "disconnected", "")

		if err != nil {
			debug.Log("dingtalk", "adapter=%s error: %v", a.name, err)
			a.publishState(false, "error", err.Error())
		}

		delay := backoffs[min(attempt, len(backoffs)-1)]
		attempt++
		debug.Log("dingtalk", "adapter=%s reconnecting in %v (attempt %d)", a.name, delay, attempt)

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// ---- Connect + serve messages (single connection lifecycle) ----

func (a *dingtalkAdapter) connectAndServe(ctx context.Context) error {
	// 1. Refresh token
	if err := a.refreshToken(ctx); err != nil {
		return fmt.Errorf("refresh token: %w", err)
	}

	// 2. Open stream → get endpoint + ticket
	wsURL, err := a.streamOpen(ctx)
	if err != nil {
		return fmt.Errorf("stream open: %w", err)
	}

	// 3. Connect WebSocket
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial stream: %w", err)
	}

	a.mu.Lock()
	a.ws = conn
	a.connected = true
	a.mu.Unlock()
	a.publishState(true, "connected", "")
	debug.Log("dingtalk", "adapter=%s connected", a.name)

	defer func() {
		a.mu.Lock()
		a.connected = false
		a.ws = nil
		a.mu.Unlock()
		conn.Close()
	}()

	// 4. Read loop with ping/pong
	conn.SetPongHandler(func(appData string) error {
		debug.Log("dingtalk", "adapter=%s pong received", a.name)
		return nil
	})

	pingTicker := time.NewTicker(wsPingInterval)
	defer pingTicker.Stop()

	readErr := make(chan error, 1)

	// Read goroutine
	go func() {
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				readErr <- err
				return
			}
			a.handleDataFrame(ctx, conn, message)
		}
	}()

	// Main loop: read errors, ping, context cancellation
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readErr:
			return fmt.Errorf("read: %w", err)
		case <-pingTicker.C:
			a.mu.RLock()
			ws := a.ws
			a.mu.RUnlock()
			if ws != nil {
				if err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					return fmt.Errorf("ping: %w", err)
				}
			}
		}
	}
}

// ---- DataFrame handling ----

func (a *dingtalkAdapter) handleDataFrame(ctx context.Context, conn *websocket.Conn, raw []byte) {
	var frame dingtalkDataFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		debug.Log("dingtalk", "adapter=%s unmarshal frame: %v", a.name, err)
		return
	}

	topic := frame.Headers[dfHeaderTopic]
	msgID := frame.Headers[dfHeaderMessageID]

	debug.Log("dingtalk", "adapter=%s frame type=%s topic=%s msgID=%s", a.name, frame.Type, topic, msgID)

	switch {
	case frame.Type == dingtalkSubCallback && topic == dingtalkBotCallbackTopic:
		// Bot message callback — process and ACK
		a.processBotCallback(ctx, frame)
		a.sendFrameResponse(conn, frame, dfStatusOK, "")

	case frame.Type == dingtalkSubCallback && topic == dingtalkCardCallbackTopic:
		// Card callback — ACK only for now
		a.sendFrameResponse(conn, frame, dfStatusOK, "")

	case topic == dingtalkSystemPingTopic:
		// System ping — ACK
		a.sendFrameResponse(conn, frame, dfStatusOK, "pong")

	case topic == dingtalkSystemDisconnectTopic:
		// Server requests disconnect — close and reconnect
		debug.Log("dingtalk", "adapter=%s server disconnect requested", a.name)
		a.sendFrameResponse(conn, frame, dfStatusOK, "")
		conn.Close()

	default:
		debug.Log("dingtalk", "adapter=%s unhandled frame type=%s topic=%s", a.name, frame.Type, topic)
		// ACK with 404 (handler not found) as per SDK
		if frame.Type == dingtalkSubCallback || frame.Type == dingtalkSubEvent {
			a.sendFrameResponse(conn, frame, 404, "handler not found")
		}
	}
}

func (a *dingtalkAdapter) processBotCallback(ctx context.Context, frame dingtalkDataFrame) {
	var callbackData dingtalkBotCallbackData
	if err := json.Unmarshal([]byte(frame.Data), &callbackData); err != nil {
		debug.Log("dingtalk", "adapter=%s unmarshal bot callback: %v", a.name, err)
		return
	}

	text := strings.TrimSpace(callbackData.Text.Content)
	debug.Log("dingtalk", "adapter=%s callback: sender=%s(%s) conv=%s text=%q webhook=%q robotCode=%q convType=%q",
		a.name, callbackData.SenderNick, callbackData.SenderStaffID,
		callbackData.ConversationID, text,
		callbackData.SessionWebhook, callbackData.RobotCode, callbackData.ConversationType)

	if text == "" {
		debug.Log("dingtalk", "adapter=%s empty text, skipping", a.name)
		return
	}

	// Strip @bot mention prefix

	// Cache webhook and robotCode for Send fallback
	a.mu.Lock()
	a.lastWebhook = callbackData.SessionWebhook
	a.lastRobotCode = callbackData.RobotCode
	a.mu.Unlock()
	atPrefix := "@" + callbackData.RobotCode + " "
	text = strings.TrimPrefix(text, atPrefix)
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	channelID := callbackData.SenderStaffID
	if channelID == "" {
		channelID = callbackData.SenderID
	}

	senderID := channelID

	inbound := InboundMessage{
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformDingTalk,
			ChannelID:  channelID,
			SenderID:   senderID,
			MessageID:  callbackData.MsgID,
			ReceivedAt: time.Now(),
		},
		Text: text,
		Metadata: map[string]string{
			"session_webhook":   callbackData.SessionWebhook,
			"sender_nick":       callbackData.SenderNick,
			"conversation_type": callbackData.ConversationType,
			"robot_code":        callbackData.RobotCode,
		},
	}

	// Pairing flow (same as QQ and WeChat)
	pairingResult, err := a.manager.HandlePairingInbound(inbound)
	debug.Log("dingtalk", "adapter=%s pairing: consumed=%v bound=%v err=%v", a.name, pairingResult.Consumed, pairingResult.Bound, err)
	if err != nil && err != ErrNoSessionBound {
		a.publishState(false, "warning", err.Error())
	}
	if pairingResult.Consumed {
		_ = a.sendTextViaWebhook(ctx, callbackData.SessionWebhook, pairingResult.ReplyText, callbackData.RobotCode)
		if pairingResult.Bound && pairingResult.PreviousBinding != nil {
			if err := a.manager.SendDirect(ctx, *pairingResult.PreviousBinding, OutboundEvent{
				Kind: OutboundEventText,
				Text: "当前目录已绑定到其他渠道，如需重新绑定请再次发起配对。",
			}); err != nil {
				debug.Log("dingtalk", "adapter=%s notify previous: %v", a.name, err)
			}
		}
		return
	}

	// Normal inbound
	debug.Log("dingtalk", "adapter=%s calling HandleInbound channel=%s sender=%s", a.name, channelID, senderID)
	if err := a.manager.HandleInbound(ctx, inbound); err != nil {
		if err == ErrInboundChannelDenied {
			_ = a.sendTextViaWebhook(ctx, callbackData.SessionWebhook, "你是未授权用户", callbackData.RobotCode)
			return
		}
		if err != ErrNoChannelBound {
			debug.Log("dingtalk", "adapter=%s handle inbound: %v", a.name, err)
		}
	}
}

// sendFrameResponse sends a DataFrame ACK back to the server.
func (a *dingtalkAdapter) sendFrameResponse(conn *websocket.Conn, reqFrame dingtalkDataFrame, code int, data string) {
	resp := dingtalkDataFrameResponse{
		Code: code,
		Headers: map[string]string{
			dfHeaderContentType: dfContentTypeJSON,
			dfHeaderMessageID:   reqFrame.Headers[dfHeaderMessageID],
		},
		Data: data,
	}
	if resp.Headers[dfHeaderMessageID] == "" {
		resp.Headers[dfHeaderMessageID] = reqFrame.Headers["messageId"]
	}

	respBytes, err := json.Marshal(resp)
	if err != nil {
		return
	}

	a.mu.RLock()
	ws := a.ws
	a.mu.RUnlock()

	if ws != nil {
		if err := ws.WriteMessage(websocket.TextMessage, respBytes); err != nil {
			debug.Log("dingtalk", "adapter=%s write frame response: %v", a.name, err)
		}
	}
}

// ---- Token management ----

func (a *dingtalkAdapter) tokenRefresher(ctx context.Context) {
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
			if time.Until(expire) < 10*time.Minute {
				if err := a.refreshToken(ctx); err != nil {
					debug.Log("dingtalk", "adapter=%s token refresh: %v", a.name, err)
				}
			}
		}
	}
}

func (a *dingtalkAdapter) refreshToken(ctx context.Context) error {
	url := dingtalkAPIBase + "/v1.0/oauth2/accessToken"
	body := map[string]any{
		"appKey":    a.appKey,
		"appSecret": a.appSecret,
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyJSON))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
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
		debug.Log("dingtalk", "adapter=%s token response: %s", a.name, string(data))
		return fmt.Errorf("DingTalk accessToken is empty: %s", string(data))
	}
	expire := 7200
	if exp, ok := result["expireIn"]; ok {
		if n, err := strconv.Atoi(fmt.Sprintf("%v", exp)); err == nil {
			expire = n
		}
	}

	a.mu.Lock()
	a.accessToken = token
	a.tokenExpire = time.Now().Add(time.Duration(expire) * time.Second)
	a.mu.Unlock()
	debug.Log("dingtalk", "adapter=%s token refreshed (appKey=%s), expires in %ds", a.name, a.appKey, expire)
	return nil
}

// ---- Stream open (get WS endpoint + ticket) ----

func (a *dingtalkAdapter) streamOpen(ctx context.Context) (string, error) {
	url := dingtalkAPIBase + "/v1.0/gateway/connections/open"
	a.mu.RLock()
	token := a.accessToken
	a.mu.RUnlock()

	body := map[string]any{
		"clientId":     a.appKey,
		"clientSecret": a.appSecret,
		"subscriptions": []map[string]any{
			{
				"type":  "CALLBACK",
				"topic": dingtalkBotCallbackTopic,
			},
		},
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyJSON))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-access-token", token)

	resp, err := http.DefaultClient.Do(req)
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
	ticket, _ := result["ticket"].(string)
	if endpoint == "" || ticket == "" {
		debug.Log("dingtalk", "adapter=%s streamOpen response: %s", a.name, string(data))
		return "", fmt.Errorf("DingTalk stream endpoint/ticket empty: %s", strings.TrimSpace(string(data)))
	}

	wsURL := fmt.Sprintf("%s?ticket=%s", endpoint, ticket)
	debug.Log("dingtalk", "adapter=%s wsURL=%s", a.name, wsURL)
	return wsURL, nil
}

// ---- Send messages ----

func (a *dingtalkAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	a.mu.RLock()
	connected := a.connected
	a.mu.RUnlock()
	if !connected {
		return fmt.Errorf("dingtalk adapter %s not connected", a.name)
	}

	if event.Kind == OutboundEventText && strings.TrimSpace(event.Text) != "" {
		// Try sessionWebhook first (from the most recent callback).
		// This is the recommended way to reply in DingTalk.
		a.mu.RLock()
		webhook := a.lastWebhook
		robotCode := a.lastRobotCode
		a.mu.RUnlock()

		if webhook != "" {
			debug.Log("dingtalk", "adapter=%s Send via webhook text_len=%d", a.name, len(event.Text))
			err := a.sendTextViaWebhook(ctx, webhook, event.Text, robotCode)
			if err == nil {
				debug.Log("dingtalk", "adapter=%s Send webhook OK", a.name)
				return nil
			}
			debug.Log("dingtalk", "adapter=%s Send webhook failed: %v, falling back to API", a.name, err)
		}

		// Fallback: use REST API with userId (staffId from ChannelID).
		debug.Log("dingtalk", "adapter=%s Send via API userId=%s text_len=%d", a.name, binding.ChannelID, len(event.Text))
		err := a.sendTextViaAPI(ctx, binding, event.Text)
		if err != nil {
			debug.Log("dingtalk", "adapter=%s Send API failed: %v", a.name, err)
		} else {
			debug.Log("dingtalk", "adapter=%s Send API OK", a.name)
		}
		return err
	}
	return nil
}

// sendTextViaWebhook sends a reply using the sessionWebhook URL from the callback.
// This is the official SDK's method for replying to bot messages.
func (a *dingtalkAdapter) sendTextViaWebhook(ctx context.Context, webhookURL, text, robotCode string) error {
	if webhookURL == "" || text == "" {
		return nil
	}

	body := map[string]any{
		"msgtype": "text",
		"text": map[string]string{
			"content": text,
		},
		"robotCode": robotCode,
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("webhook send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("webhook send HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// sendTextViaAPI sends a message via the DingTalk REST API (for outbound messages
// not triggered by a callback, e.g. proactive messages).
func (a *dingtalkAdapter) sendTextViaAPI(ctx context.Context, binding ChannelBinding, text string) error {
	a.mu.RLock()
	token := a.accessToken
	a.mu.RUnlock()
	if token == "" {
		return fmt.Errorf("no access token")
	}

	// DingTalk oToMessages/batchSend requires userIds (staffId).
	// ChannelID stores the sender's staffId.
	userID := strings.TrimSpace(binding.ChannelID)
	if userID == "" {
		return fmt.Errorf("no userId in binding (ChannelID is empty)")
	}

	body := map[string]any{
		"robotCode": a.appKey,
		"userIds":   []string{userID},
		"msgKey":    "sampleText",
		"msgParam":  fmt.Sprintf(`{"content":"%s"}`, escapeJSONString(text)),
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dingtalkAPIBase+"/v1.0/robot/oToMessages/batchSend", bytes.NewReader(bodyJSON))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-access-token", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("api send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("api send HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// ---- Helpers ----

func (a *dingtalkAdapter) publishState(healthy bool, status, errMsg string) {
	if a.manager == nil {
		return
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:      a.name,
		Platform:  PlatformDingTalk,
		Healthy:   healthy,
		Status:    status,
		LastError: errMsg,
	})
}

func escapeJSONString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return s
}
