package im

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
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

var (
	dingtalkMarkdownLinkRe      = regexp.MustCompile(`\[(.*?)\]\((.*?)\)`)
	dingtalkOrderedListPrefixRe = regexp.MustCompile(`^\d+[.)]\s+`)
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
	name       string
	manager    *Manager
	appKey     string
	appSecret  string
	httpClient *http.Client

	mu            sync.RWMutex
	writeMu       sync.Mutex // protects websocket writes
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
		name:       name,
		manager:    mgr,
		appKey:     appKey,
		appSecret:  appSecret,
		httpClient: util.NewInsecureAwareClient(30 * time.Second),
	}, nil
}

// client returns the adapter's HTTP client, initializing one on first use
// if the constructor didn't set it (e.g. in tests).
func (a *dingtalkAdapter) client() *http.Client {
	if a.httpClient == nil {
		a.httpClient = util.NewInsecureAwareClient(30 * time.Second)
	}
	return a.httpClient
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
	safego.Go("dingtalk.run", func() { a.run(childCtx) })
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
	safego.Go("dingtalk.tokenRefresher", func() { a.tokenRefresher(ctx) })

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

	// Read goroutine (safego protects against panic)
	safego.Go("im.dingtalk.read", func() {
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				readErr <- err
				return
			}
			a.handleDataFrame(ctx, conn, message)
		}
	})

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
				a.writeMu.Lock()
				err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
				a.writeMu.Unlock()
				if err != nil {
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
		redactDingTalkURL(callbackData.SessionWebhook), callbackData.RobotCode, callbackData.ConversationType)

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
		_ = a.sendMarkdownViaWebhook(ctx, callbackData.SessionWebhook, pairingResult.ReplyText, callbackData.RobotCode)
		if err := a.manager.NotifyPreviousBindingReplaced(ctx, pairingResult); err != nil {
			debug.Log("dingtalk", "adapter=%s notify previous: %v", a.name, err)
		}
		return
	}

	// Normal inbound
	debug.Log("dingtalk", "adapter=%s calling HandleInbound channel=%s sender=%s", a.name, channelID, senderID)
	if err := a.manager.HandleInbound(ctx, inbound); err != nil {
		if err == ErrInboundChannelDenied {
			_ = a.sendMarkdownViaWebhook(ctx, callbackData.SessionWebhook, UnauthorizedMessage(a.manager.Language()), callbackData.RobotCode)
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
		debug.Log("dingtalk", "adapter=%s marshal frame response: %v", a.name, err)
		return
	}

	a.mu.RLock()
	ws := a.ws
	a.mu.RUnlock()

	if ws != nil {
		a.writeMu.Lock()
		err := ws.WriteMessage(websocket.TextMessage, respBytes)
		a.writeMu.Unlock()
		if err != nil {
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
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyJSON))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := util.ReadAll(resp.Body, util.ReadLimitGeneral)
	if err != nil {
		return err
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return err
	}
	token, _ := result["accessToken"].(string)
	if token == "" {
		redacted := redactDingTalkResponse(string(data))
		debug.Log("dingtalk", "adapter=%s token response: %s", a.name, redacted)
		return fmt.Errorf("DingTalk accessToken is empty: %s", redacted)
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
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal stream open request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyJSON))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-access-token", token)

	resp, err := a.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := util.ReadAll(resp.Body, util.ReadLimitGeneral)
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
		redacted := redactDingTalkResponse(string(data))
		debug.Log("dingtalk", "adapter=%s streamOpen response: %s", a.name, redacted)
		return "", fmt.Errorf("DingTalk stream endpoint/ticket empty: %s", strings.TrimSpace(redacted))
	}

	wsURL := fmt.Sprintf("%s?ticket=%s", endpoint, ticket)
	debug.Log("dingtalk", "adapter=%s wsURL=%s", a.name, redactDingTalkURL(wsURL))
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

	content := strings.TrimSpace(a.outboundText(event))
	if content == "" {
		return nil
	}

	// Split long messages to stay within DingTalk's ~4000 char markdown limit.
	// Without splitting, long agent responses are silently truncated or rejected.
	chunks := SplitMessageForPlatform(content, PlatformDingTalk)

	// Try sessionWebhook first (from the most recent callback).
	// This is the recommended way to reply in DingTalk.
	a.mu.RLock()
	webhook := a.lastWebhook
	robotCode := a.lastRobotCode
	a.mu.RUnlock()

	for i, chunk := range chunks {
		if webhook != "" {
			debug.Log("dingtalk", "adapter=%s Send via webhook chunk=%d/%d text_len=%d", a.name, i+1, len(chunks), len(chunk))
			err := a.sendMarkdownViaWebhook(ctx, webhook, chunk, robotCode)
			if err == nil {
				continue
			}
			debug.Log("dingtalk", "adapter=%s Send webhook failed: %v, falling back to API", a.name, err)
		}

		// Fallback: use REST API with userId (staffId from ChannelID).
		debug.Log("dingtalk", "adapter=%s Send via API userId=%s chunk=%d/%d text_len=%d", a.name, binding.ChannelID, i+1, len(chunks), len(chunk))
		err := a.sendMarkdownViaAPI(ctx, binding, chunk)
		if err != nil {
			debug.Log("dingtalk", "adapter=%s Send API failed: %v", a.name, err)
			return err
		}
	}
	debug.Log("dingtalk", "adapter=%s Send OK chunks=%d", a.name, len(chunks))
	return nil
}

// sendMarkdownViaWebhook sends a reply using the sessionWebhook URL from the callback.
// This is the official SDK's method for replying to bot messages.
func (a *dingtalkAdapter) sendMarkdownViaWebhook(ctx context.Context, webhookURL, text, robotCode string) error {
	if webhookURL == "" || text == "" {
		return nil
	}
	title := dingtalkMarkdownTitle(text)

	body := map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"title": title,
			"text":  text,
		},
		"robotCode": robotCode,
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal webhook request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client().Do(req)
	if err != nil {
		return fmt.Errorf("webhook send: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("webhook send HTTP %d: %s", resp.StatusCode, redactDingTalkResponse(string(respBody)))
	}
	// DingTalk returns HTTP 200 with errcode in JSON body on failure
	// (rate limits, content too long, etc.). Must check errcode.
	var wresp struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(respBody, &wresp); err == nil && wresp.ErrCode != 0 {
		return fmt.Errorf("webhook send errcode %d: %s", wresp.ErrCode, redactDingTalkResponse(wresp.ErrMsg))
	}
	return nil
}

// sendMarkdownViaAPI sends a message via the DingTalk REST API (for outbound messages
// not triggered by a callback, e.g. proactive messages).
func (a *dingtalkAdapter) sendMarkdownViaAPI(ctx context.Context, binding ChannelBinding, text string) error {
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
	title := dingtalkMarkdownTitle(text)

	body := map[string]any{
		"robotCode": a.appKey,
		"userIds":   []string{userID},
		"msgKey":    "sampleMarkdown",
		"msgParam":  fmt.Sprintf(`{"title":"%s","text":"%s"}`, escapeJSONString(title), escapeJSONString(text)),
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal api send request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dingtalkAPIBase+"/v1.0/robot/oToMessages/batchSend", bytes.NewReader(bodyJSON))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-access-token", token)

	resp, err := a.client().Do(req)
	if err != nil {
		return fmt.Errorf("api send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("api send HTTP %d: %s", resp.StatusCode, redactDingTalkResponse(string(respBody)))
	}
	// DingTalk API can also return HTTP 200 with errcode in JSON body on failure.
	// Same pattern as webhook — must check errcode to avoid silent failures.
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	var aresp struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(respBody, &aresp); err == nil && aresp.ErrCode != 0 {
		return fmt.Errorf("api send errcode %d: %s", aresp.ErrCode, redactDingTalkResponse(aresp.ErrMsg))
	}
	return nil
}

// ---- Helpers ----

func (a *dingtalkAdapter) publishState(healthy bool, status, errMsg string) {
	if a.manager == nil {
		return
	}
	contactURI := ""
	if a.appKey != "" {
		// DingTalk enterprise bots don't have a public scan-to-add URL.
		// Link to the developer console app detail page instead.
		contactURI = "https://dev.dingtalk.com/app/detail?appKey=" + a.appKey
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:       a.name,
		Platform:   PlatformDingTalk,
		Healthy:    healthy,
		Status:     status,
		LastError:  errMsg,
		ContactURI: contactURI,
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

func dingtalkMarkdownTitle(text string) string {
	const fallback = "ggcode"
	const maxRunes = 64

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			continue
		}
		if strings.Trim(line, "| :-") == "" {
			continue
		}

		title := markdownImageRe.ReplaceAllString(line, "$1")
		title = dingtalkMarkdownLinkRe.ReplaceAllString(title, "$1")
		title = strings.TrimLeft(title, "#>*-+ \t")
		title = dingtalkOrderedListPrefixRe.ReplaceAllString(title, "")
		title = strings.NewReplacer("`", "", "*", "", "_", "", "~", "", "|", " ").Replace(title)
		title = strings.Join(strings.Fields(title), " ")
		if title == "" {
			continue
		}

		runes := []rune(title)
		if len(runes) > maxRunes {
			return string(runes[:maxRunes-3]) + "..."
		}
		return title
	}

	return fallback
}

func redactDingTalkResponse(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
		redacted, err := json.Marshal(redactDingTalkValue(decoded, ""))
		if err == nil {
			return string(redacted)
		}
	}

	return redactDingTalkURL(trimmed)
}

func redactDingTalkValue(value any, key string) any {
	switch typed := value.(type) {
	case map[string]any:
		for k, v := range typed {
			typed[k] = redactDingTalkValue(v, k)
		}
		return typed
	case []any:
		for i, v := range typed {
			typed[i] = redactDingTalkValue(v, key)
		}
		return typed
	case string:
		if isDingTalkSensitiveKey(key) {
			return redactSecret(typed)
		}
		return redactDingTalkURL(typed)
	default:
		return value
	}
}

func redactDingTalkURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}
	query := u.Query()
	changed := false
	for key := range query {
		if isDingTalkSensitiveKey(key) {
			query.Set(key, redactSecret(query.Get(key)))
			changed = true
		}
	}
	if changed {
		u.RawQuery = query.Encode()
	}
	return u.String()
}

func isDingTalkSensitiveKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(key, "token") ||
		strings.Contains(key, "secret") ||
		strings.Contains(key, "ticket") ||
		strings.Contains(key, "webhook")
}

func redactSecret(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return "[redacted]"
	}
	return value[:4] + "..." + value[len(value)-4:]
}
