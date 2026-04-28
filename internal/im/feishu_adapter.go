package im

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	imstt "github.com/topcheer/ggcode/internal/im/stt"
	imagepkg "github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/safego"
)

const (
	feishuDefaultDomain    = "feishu"
	feishuMaxTextLen       = 4000
	feishuTokenExpireDelta = 300 // refresh 5 minutes early
)

type feishuAdapter struct {
	name        string
	manager     *Manager
	httpClient  *http.Client
	appID       string
	appSecret   string
	encryptKey  string
	verifyToken string
	domain      string
	webhookPort int // legacy: only used when transport=webhook

	mu          sync.RWMutex
	connected   bool
	token       string
	tokenExpire time.Time
	httpServer  *http.Server
	stt         imstt.Transcriber

	// seenEvents deduplicates Feishu webhook redeliveries (Feishu retries up
	// to 3x if the webhook does not respond within ~3s; we may legitimately
	// take longer than that to respond, so dedup by event_id+message_id is
	// required to avoid running the same agent turn 2-3x). See locks.md S1.
	seenMu     sync.Mutex
	seenEvents map[string]time.Time
	// seenNonces deduplicates (timestamp,nonce) pairs presented to
	// verifySignature, defeating brute replay even within the freshness
	// window. See locks.md S2.
	seenNonces map[string]time.Time
}

func newFeishuAdapter(name string, imCfg config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) (*feishuAdapter, error) {
	appID := strings.TrimSpace(stringValue(adapterCfg.Extra, "app_id"))
	if appID == "" {
		return nil, fmt.Errorf("Feishu app_id is required for adapter %q", name)
	}
	appSecret := strings.TrimSpace(stringValue(adapterCfg.Extra, "app_secret"))
	if appSecret == "" {
		return nil, fmt.Errorf("Feishu app_secret is required for adapter %q", name)
	}
	domain := strings.TrimSpace(stringValue(adapterCfg.Extra, "domain"))
	if domain == "" {
		domain = feishuDefaultDomain
	}
	encryptKey := strings.TrimSpace(stringValue(adapterCfg.Extra, "encrypt_key", "encryptKey"))
	verifyToken := strings.TrimSpace(stringValue(adapterCfg.Extra, "verification_token", "verify_token"))
	webhookPort := 0
	if v := strings.TrimSpace(stringValue(adapterCfg.Extra, "webhook_port")); v != "" {
		var err error
		if n, ok := intValueStr(v); ok && n > 0 {
			webhookPort = n
		} else {
			_ = err
		}
	}
	adapter := &feishuAdapter{
		name:        name,
		manager:     mgr,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		appID:       appID,
		appSecret:   appSecret,
		encryptKey:  encryptKey,
		verifyToken: verifyToken,
		domain:      domain,
		webhookPort: webhookPort,
		seenEvents:  make(map[string]time.Time),
		seenNonces:  make(map[string]time.Time),
	}
	adapter.stt = buildSTTWithFallback(imCfg.STT, adapterCfg.Extra, resolveFeishuSTTConfig)
	return adapter, nil
}

func resolveFeishuSTTConfig(global config.IMSTTConfig, extra map[string]interface{}) *config.IMSTTConfig {
	var cfg config.IMSTTConfig
	hasOverride := false
	if sttExtra, ok := extra["stt"].(map[string]interface{}); ok {
		cfg.Provider = firstNonEmpty(strings.TrimSpace(stringFromAny(sttExtra["provider"])), cfg.Provider)
		cfg.BaseURL = firstNonEmpty(strings.TrimSpace(stringFromAny(sttExtra["baseUrl"])), strings.TrimSpace(stringFromAny(sttExtra["base_url"])), cfg.BaseURL)
		cfg.APIKey = firstNonEmpty(strings.TrimSpace(stringFromAny(sttExtra["apiKey"])), strings.TrimSpace(stringFromAny(sttExtra["api_key"])), cfg.APIKey)
		cfg.Model = firstNonEmpty(strings.TrimSpace(stringFromAny(sttExtra["model"])), cfg.Model)
		if cfg.Provider != "" || cfg.BaseURL != "" || cfg.APIKey != "" || cfg.Model != "" {
			hasOverride = true
		}
	}
	if !hasOverride {
		if global.Provider == "" && global.BaseURL == "" && global.APIKey == "" {
			return nil
		}
		return &global
	}
	return &cfg
}

func (a *feishuAdapter) Name() string { return a.name }

func (a *feishuAdapter) Start(ctx context.Context) {
	debug.Log("feishu", "adapter=%s start domain=%s webhookPort=%d", a.name, a.domain, a.webhookPort)
	a.publishState(false, "connecting", "")
	go a.run(ctx)
}

func (a *feishuAdapter) Close() error {
	a.mu.Lock()
	srv := a.httpServer
	a.httpServer = nil
	a.connected = false
	a.mu.Unlock()
	if srv != nil {
		return srv.Close()
	}
	return nil
}

func (a *feishuAdapter) run(ctx context.Context) {
	// Initial token fetch (needed for sending messages regardless of transport)
	if err := a.refreshToken(ctx); err != nil {
		a.publishState(false, "error", fmt.Sprintf("token refresh: %v", err))
		return
	}
	a.mu.Lock()
	a.connected = true
	a.mu.Unlock()
	a.publishState(true, "connected", "")
	debug.Log("feishu", "adapter=%s connected (token obtained)", a.name)

	// Start token refresh goroutine
	go a.tokenRefreshLoop(ctx)

	// Start webhook server if port configured (legacy mode)
	if a.webhookPort > 0 {
		a.startWebhookServer(ctx)
		<-ctx.Done()
	} else {
		// Default: use WebSocket long connection via Feishu SDK
		a.runWebSocket(ctx)
	}

	a.mu.Lock()
	a.connected = false
	a.mu.Unlock()
	a.publishState(false, "stopped", "")
}

func (a *feishuAdapter) runWebSocket(ctx context.Context) {
	// We bypass the SDK's WS client entirely because it silently drops
	// MessageTypeCard frames (case MessageTypeCard: return in handleDataFrame).
	// Instead, we establish our own single WS connection that handles both
	// event (messages) and card (button callbacks) message types.
	// Using a single connection avoids the conflict where two WS connections
	// for the same appID cause the earlier one to be disconnected (error 200340).

	eventHandler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			a.handleLarkMessageEvent(ctx, event)
			return nil
		}).
		OnP2CardActionTrigger(func(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
			a.handleCardActionTrigger(ctx, event)
			return nil, nil
		})

	domain := lark.FeishuBaseUrl
	if a.domain == "lark" {
		domain = "https://open.larksuite.com"
	}

	// Retry loop: reconnect on disconnection
	for {
		if ctx.Err() != nil {
			return
		}
		_ = a.runSingleWSConnection(ctx, domain, eventHandler)
		if ctx.Err() != nil {
			return
		}
		debug.Log("feishu", "adapter=%s WS disconnected, reconnecting in 3s...", a.name)
		a.publishState(false, "reconnecting", "websocket disconnected, reconnecting")
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

// runSingleWSConnection establishes one WS connection, processes all message
// types (events + card callbacks), and returns when the connection is lost.
func (a *feishuAdapter) runSingleWSConnection(ctx context.Context, domain string, eventHandler *dispatcher.EventDispatcher) error {
	// 1. Get WS endpoint URL
	body := map[string]string{
		"AppID":     a.appID,
		"AppSecret": a.appSecret,
	}
	bs, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, domain+"/callback/ws/endpoint", bytes.NewReader(bs))
	if err != nil {
		return fmt.Errorf("endpoint request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("endpoint fetch: %w", err)
	}
	defer resp.Body.Close()
	var endpointResp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data *struct {
			URL       string `json:"url"`
			Lifespan  int64  `json:"lifespan"`
			ExpiresAt int64  `json:"expire_in"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&endpointResp); err != nil || endpointResp.Data == nil || endpointResp.Data.URL == "" {
		return fmt.Errorf("no endpoint URL: code=%d, err=%v", endpointResp.Code, err)
	}
	wsURL := endpointResp.Data.URL
	debug.Log("feishu", "adapter=%s got WS endpoint, connecting...", a.name)

	// 2. Dial WebSocket
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}
	defer conn.Close()

	a.mu.Lock()
	a.connected = true
	a.mu.Unlock()
	a.publishState(true, "online", "websocket connected")
	debug.Log("feishu", "adapter=%s WS connected", a.name)

	// 3. Ping loop (Feishu requires periodic ping to keep connection alive)
	safego.Go("im.feishu.wsPing", func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
					debug.Log("feishu", "adapter=%s ping failed: %v", a.name, err)
					return
				}
			case <-ctx.Done():
				return
			}
		}
	})

	// 4. Read loop — dispatch based on message type
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		mt, msg, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			debug.Log("feishu", "adapter=%s WS read error: %v", a.name, err)
			return fmt.Errorf("read: %w", err)
		}

		// Text messages: "pong" responses to our ping
		if mt == websocket.TextMessage {
			continue
		}

		// Binary messages: protobuf frames with event/card data
		if mt != websocket.BinaryMessage {
			continue
		}

		var frame larkws.Frame
		if err := frame.Unmarshal(msg); err != nil {
			continue
		}

		headers := larkws.Headers(frame.Headers)
		msgType := headers.GetString("type")
		debug.Log("feishu", "adapter=%s WS frame: type=%s payload_len=%d", a.name, msgType, len(frame.Payload))

		switch larkws.MessageType(msgType) {
		case larkws.MessageTypeEvent:
			// Delegate to SDK's event dispatcher for message handling
			safego.Go("im.feishu.wsEvent", func() {
				eventHandler.Do(ctx, frame.Payload)
			})

		case larkws.MessageTypeCard:
			// Card callbacks: delegate to eventHandler which has
			// OnP2CardActionTrigger registered. The SDK's WS client
			// drops MessageTypeCard, but our custom WS passes it through.
			safego.Go("im.feishu.wsCard", func() {
				eventHandler.Do(ctx, frame.Payload)
			})

		default:
			// ping/pong/ack — no action needed
		}
	}
}

// handleCardActionTrigger processes a card action callback received via
// OnP2CardActionTrigger on the EventDispatcher. This handles button clicks
// from Feishu Card 2.0 interactive messages sent via SendInteractive.
func (a *feishuAdapter) handleCardActionTrigger(ctx context.Context, event *callback.CardActionTriggerEvent) {
	if a.manager == nil || event.Event == nil {
		return
	}
	action := event.Event.Action
	if action == nil || action.Value == nil {
		return
	}
	choice, _ := action.Value["choice"].(string)
	if choice == "" {
		return
	}

	var openID, messageID, chatID string
	if event.Event.Operator != nil {
		openID = event.Event.Operator.OpenID
	}
	if event.Event.Context != nil {
		messageID = event.Event.Context.OpenMessageID
		chatID = event.Event.Context.OpenChatID
	}

	debug.Log("feishu", "adapter=%s card action: choice=%s openID=%s msgID=%s", a.name, choice, openID, messageID)

	a.manager.HandleInteractiveCallback(InteractiveCallback{
		MessageID: messageID,
		Values:    []string{choice},
		Adapter:   a.name,
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformFeishu,
			ChannelID:  chatID,
			SenderID:   openID,
			MessageID:  messageID,
			ReceivedAt: time.Now(),
		},
	})
}

func (a *feishuAdapter) handleLarkMessageEvent(ctx context.Context, event *larkim.P2MessageReceiveV1) {
	debug.Log("feishu", "adapter=%s handleLarkMessageEvent called, event=%p event.Event=%p", a.name, event, event.Event)
	if event.Event == nil || event.Event.Message == nil {
		debug.Log("feishu", "adapter=%s handleLarkMessageEvent: event.Event or event.Event.Message is nil", a.name)
		return
	}
	msg := event.Event.Message

	var openID string
	if event.Event.Sender != nil && event.Event.Sender.SenderId != nil {
		openID = derefStr(event.Event.Sender.SenderId.OpenId)
	}

	chatID := derefStr(msg.ChatId)
	messageID := derefStr(msg.MessageId)
	content := derefStr(msg.Content)
	chatType := derefStr(msg.ChatType)
	msgType := derefStr(msg.MessageType)

	debug.Log("feishu", "adapter=%s raw inbound: chat=%s msgType=%s chatType=%s sender=%s contentLen=%d", a.name, chatID, msgType, chatType, openID, len(content))

	// Process non-text message types into attachments
	attachments, voiceText := a.processAttachments(ctx, msgType, content, messageID)

	// Parse text content
	text := a.parseMessageContent(content)
	text = strings.TrimSpace(text)
	if voiceText != "" {
		if text != "" {
			text += "\n\n" + voiceText
		} else {
			text = voiceText
		}
	}
	if text == "" && len(attachments) == 0 {
		debug.Log("feishu", "adapter=%s parsed text is empty, content=%q, msgType=%s", a.name, content, msgType)
		return
	}

	debug.Log("feishu", "adapter=%s inbound chat=%s type=%s sender=%s len=%d attachments=%d", a.name, chatID, chatType, openID, len(text), len(attachments))

	inbound := InboundMessage{
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformFeishu,
			ChannelID:  chatID,
			SenderID:   openID,
			MessageID:  messageID,
			ReceivedAt: time.Now(),
		},
		Text:        text,
		Attachments: attachments,
	}

	pairingResult, err := a.manager.HandlePairingInbound(inbound)
	if err != nil && err != ErrNoSessionBound {
		a.publishState(false, "warning", err.Error())
	}
	if pairingResult.Consumed {
		if sendErr := a.sendTextMessage(ctx, chatID, pairingResult.ReplyText); sendErr != nil {
			a.publishState(false, "warning", sendErr.Error())
		}
		return
	}

	if err := a.manager.HandleInbound(ctx, inbound); err != nil {
		if err == ErrInboundChannelDenied {
			debug.Log("feishu", "adapter=%s unauthorized inbound chat=%s", a.name, chatID)
			return
		}
		if err != ErrNoChannelBound {
			a.publishState(false, "warning", err.Error())
		}
	}
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (a *feishuAdapter) tokenRefreshLoop(ctx context.Context) {
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
			if time.Until(expire) < time.Duration(feishuTokenExpireDelta)*time.Second {
				if err := a.refreshToken(ctx); err != nil {
					debug.Log("feishu", "adapter=%s token refresh error: %v", a.name, err)
				}
			}
		}
	}
}

func (a *feishuAdapter) refreshToken(ctx context.Context) error {
	apiBase := a.resolveAPIBase()
	url := apiBase + "/auth/v3/tenant_access_token/internal"
	body := map[string]any{
		"app_id":     a.appID,
		"app_secret": a.appSecret,
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
	code, _ := intValue(result["code"])
	if code != 0 {
		msg, _ := result["msg"].(string)
		return fmt.Errorf("Feishu auth error [%d]: %s", code, msg)
	}
	token, _ := result["tenant_access_token"].(string)
	if token == "" {
		return fmt.Errorf("Feishu tenant_access_token is empty")
	}
	expire, _ := intValue(result["expire"])
	if expire <= 0 {
		expire = 7200
	}
	a.mu.Lock()
	a.token = token
	a.tokenExpire = time.Now().Add(time.Duration(expire) * time.Second)
	a.mu.Unlock()
	debug.Log("feishu", "adapter=%s token refreshed, expires in %ds", a.name, expire)
	return nil
}

func (a *feishuAdapter) startWebhookServer(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleWebhook)
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", a.webhookPort),
		Handler: mux,
	}
	a.mu.Lock()
	a.httpServer = server
	a.mu.Unlock()

	safego.Go("im.feishu.webhookServe", func() {
		debug.Log("feishu", "adapter=%s webhook listening on :%d", a.name, a.webhookPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			debug.Log("feishu", "adapter=%s webhook server error: %v", a.name, err)
		}
	})
	safego.Go("im.feishu.webhookShutdown", func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	})
}

func (a *feishuAdapter) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Cap body size to prevent OOM DoS via Content-Length: 5G style attacks.
	// 1 MiB is generous for Feishu event envelopes (typical size is <10 KiB).
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body error", http.StatusRequestEntityTooLarge)
		return
	}
	defer r.Body.Close()

	// Verify signature if encryptKey is set
	if a.encryptKey != "" {
		timestamp := r.Header.Get("X-Lark-Request-Timestamp")
		nonce := r.Header.Get("X-Lark-Request-Nonce")
		signature := r.Header.Get("X-Lark-Signature")
		if !a.verifySignature(timestamp, nonce, string(bodyBytes), signature) {
			debug.Log("feishu", "adapter=%s webhook signature verification failed", a.name)
			http.Error(w, "signature mismatch", http.StatusUnauthorized)
			return
		}
		// Reject stale or replayed requests. Feishu sends timestamp as a
		// unix-second string. ±5min window matches Feishu's documented
		// recommendation; combined with nonce LRU this defeats replay even
		// when the original signature is captured.
		if !a.acceptTimestamp(timestamp) {
			debug.Log("feishu", "adapter=%s webhook stale timestamp=%s", a.name, timestamp)
			http.Error(w, "stale request", http.StatusUnauthorized)
			return
		}
		if !a.acceptNonce(timestamp, nonce) {
			debug.Log("feishu", "adapter=%s webhook replayed nonce=%s", a.name, nonce)
			http.Error(w, "replayed request", http.StatusUnauthorized)
			return
		}
	}

	var payload map[string]any
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Handle URL verification challenge
	if challenge, ok := payload["challenge"].(string); ok {
		resp := map[string]any{
			"challenge": challenge,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	// Process event
	header, _ := payload["header"].(map[string]any)
	if header == nil {
		return
	}
	// Dedup by event_id (preferred) or message_id. Feishu retries up to 3x
	// when the webhook does not reply 200 within ~3s; agent runs commonly
	// exceed that, so without dedup the same message triggers 2-3 parallel
	// agent turns and 2-3x the side effects.
	eventID, _ := header["event_id"].(string)
	dedupKey := eventID
	if dedupKey == "" {
		if event, ok := payload["event"].(map[string]any); ok {
			if message, ok := event["message"].(map[string]any); ok {
				if mid, ok := message["message_id"].(string); ok {
					dedupKey = mid
				}
			}
		}
	}
	if dedupKey != "" && a.seenEvent(dedupKey) {
		debug.Log("feishu", "adapter=%s duplicate webhook event=%s suppressed", a.name, dedupKey)
		w.WriteHeader(http.StatusOK)
		return
	}

	eventType, _ := header["event_type"].(string)
	if eventType == "im.message.receive_v1" {
		event, _ := payload["event"].(map[string]any)
		if event != nil {
			// Reply 200 immediately so Feishu does not retry; process the
			// event in a detached goroutine. Use background ctx because the
			// HTTP request ctx is cancelled the moment we return.
			w.WriteHeader(http.StatusOK)
			go a.handleMessageEvent(context.Background(), event)
			return
		}
	}
	// Handle card action callbacks (button clicks from interactive messages)
	if eventType == "card.action.trigger" {
		w.WriteHeader(http.StatusOK)
		go a.handleCardAction(context.Background(), payload)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *feishuAdapter) verifySignature(timestamp, nonce, body, signature string) bool {
	mac := hmac.New(sha256.New, []byte(a.encryptKey))
	mac.Write([]byte(timestamp + nonce + a.encryptKey + body))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expected))
}

// acceptTimestamp returns true if the request timestamp (unix seconds) is
// within ±5 minutes of now.
func (a *feishuAdapter) acceptTimestamp(timestamp string) bool {
	if timestamp == "" {
		return false
	}
	ts, err := parseUnixSeconds(timestamp)
	if err != nil {
		return false
	}
	delta := time.Since(ts)
	if delta < 0 {
		delta = -delta
	}
	return delta <= 5*time.Minute
}

// acceptNonce records (timestamp,nonce) and returns false if the pair was
// already seen within the freshness window.
func (a *feishuAdapter) acceptNonce(timestamp, nonce string) bool {
	if nonce == "" {
		// Feishu does include a nonce on real requests; an empty nonce is
		// suspicious but we don't want to break legitimate clients that omit
		// it. Skip the check rather than reject outright.
		return true
	}
	key := timestamp + "|" + nonce
	a.seenMu.Lock()
	defer a.seenMu.Unlock()
	now := time.Now()
	for k, t := range a.seenNonces {
		if now.Sub(t) > 10*time.Minute {
			delete(a.seenNonces, k)
		}
	}
	if _, ok := a.seenNonces[key]; ok {
		return false
	}
	a.seenNonces[key] = now
	return true
}

// seenEvent records the event id and returns true if it was already processed.
func (a *feishuAdapter) seenEvent(eventID string) bool {
	a.seenMu.Lock()
	defer a.seenMu.Unlock()
	now := time.Now()
	for id, t := range a.seenEvents {
		if now.Sub(t) > 10*time.Minute {
			delete(a.seenEvents, id)
		}
	}
	if _, ok := a.seenEvents[eventID]; ok {
		return true
	}
	a.seenEvents[eventID] = now
	return false
}

func parseUnixSeconds(ts string) (time.Time, error) {
	var sec int64
	_, err := fmt.Sscanf(ts, "%d", &sec)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(sec, 0), nil
}

// handleCardAction processes Feishu card action callbacks (button clicks).
// Card action triggers arrive via the webhook with event_type "card.action.trigger".
func (a *feishuAdapter) handleCardAction(ctx context.Context, payload map[string]any) {
	if a.manager == nil {
		return
	}

	// Extract action value from the callback
	action, _ := payload["action"].(map[string]any)
	if action == nil {
		return
	}

	// The button value contains {"choice": "1"} etc.
	value, _ := action["value"].(map[string]any)
	if value == nil {
		return
	}
	choice, _ := value["choice"].(string)
	if choice == "" {
		return
	}

	// Extract sender info
	userID, _ := action["open_id"].(string)
	senderName := ""

	// Try to get message context from the action
	messageID, _ := action["message_id"].(string)

	a.manager.HandleInteractiveCallback(InteractiveCallback{
		MessageID: messageID,
		Values:    []string{choice},
		Adapter:   a.name,
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformFeishu,
			ChannelID:  "", // filled from action context if available
			SenderID:   userID,
			SenderName: senderName,
			MessageID:  messageID,
			ReceivedAt: time.Now(),
		},
	})
}

func (a *feishuAdapter) handleMessageEvent(ctx context.Context, event map[string]any) {
	sender, _ := event["sender"].(map[string]any)
	message, _ := event["message"].(map[string]any)
	if sender == nil || message == nil {
		return
	}

	senderID, _ := sender["sender_id"].(map[string]any)
	var openID string
	if senderID != nil {
		openID, _ = senderID["open_id"].(string)
	}
	chatID, _ := message["chat_id"].(string)
	messageID, _ := message["message_id"].(string)
	content, _ := message["content"].(string)
	chatType, _ := message["chat_type"].(string)

	// Parse text content
	text := a.parseMessageContent(content)
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	debug.Log("feishu", "adapter=%s inbound chat=%s type=%s sender=%s len=%d", a.name, chatID, chatType, openID, len(text))

	inbound := InboundMessage{
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformFeishu,
			ChannelID:  chatID,
			SenderID:   openID,
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
		if sendErr := a.sendTextMessage(ctx, chatID, pairingResult.ReplyText); sendErr != nil {
			a.publishState(false, "warning", sendErr.Error())
		}
		return
	}

	if err := a.manager.HandleInbound(ctx, inbound); err != nil {
		if err == ErrInboundChannelDenied {
			debug.Log("feishu", "adapter=%s unauthorized inbound chat=%s", a.name, chatID)
			return
		}
		if err != ErrNoChannelBound {
			a.publishState(false, "warning", err.Error())
		}
	}
}

// processAttachments handles non-text message types (image, audio, file)
// and returns attachments plus an optional voice transcript text.
func (a *feishuAdapter) processAttachments(ctx context.Context, msgType, content, messageID string) ([]Attachment, string) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, ""
	}

	switch msgType {
	case "image":
		return a.processImageAttachment(ctx, parsed)
	case "audio":
		return a.processAudioAttachment(ctx, parsed, messageID)
	case "file":
		return a.processFileAttachment(ctx, parsed, messageID)
	case "sticker":
		// Sticker is essentially an image
		return a.processImageAttachment(ctx, parsed)
	}
	return nil, ""
}

func (a *feishuAdapter) processImageAttachment(ctx context.Context, parsed map[string]any) ([]Attachment, string) {
	imageKey, _ := parsed["image_key"].(string)
	if imageKey == "" {
		return nil, ""
	}
	data, mimeType, err := a.downloadFeishuImage(ctx, imageKey)
	if err != nil {
		debug.Log("feishu", "adapter=%s download image %s failed: %v", a.name, imageKey, err)
		return nil, ""
	}
	if len(data) == 0 {
		return nil, ""
	}
	// Detect real MIME type from content
	if decoded, decodeErr := imagepkg.Decode(data); decodeErr == nil && strings.TrimSpace(decoded.MIME) != "" {
		mimeType = decoded.MIME
	}
	return []Attachment{{
		Kind:       AttachmentImage,
		Name:       "image.jpg",
		MIME:       mimeType,
		DataBase64: base64.StdEncoding.EncodeToString(data),
	}}, ""
}

func (a *feishuAdapter) processAudioAttachment(ctx context.Context, parsed map[string]any, messageID string) ([]Attachment, string) {
	fileKey, _ := parsed["file_key"].(string)
	if fileKey == "" {
		return nil, ""
	}
	data, mimeType, err := a.downloadFeishuResource(ctx, messageID, fileKey, "file")
	if err != nil {
		debug.Log("feishu", "adapter=%s download audio %s failed: %v", a.name, fileKey, err)
		return nil, ""
	}
	if len(data) == 0 {
		return nil, ""
	}

	// Try STT transcription
	transcript := ""
	if a.stt != nil {
		transcript = a.transcribeFeishuAudio(ctx, data, mimeType)
	}

	return []Attachment{{
		Kind:       AttachmentVoice,
		Name:       "audio.opus",
		MIME:       mimeType,
		Transcript: transcript,
	}}, transcript
}

func (a *feishuAdapter) processFileAttachment(ctx context.Context, parsed map[string]any, messageID string) ([]Attachment, string) {
	fileKey, _ := parsed["file_key"].(string)
	fileName, _ := parsed["file_name"].(string)
	if fileKey == "" {
		return nil, ""
	}
	data, mimeType, err := a.downloadFeishuResource(ctx, messageID, fileKey, "file")
	if err != nil {
		debug.Log("feishu", "adapter=%s download file %s failed: %v", a.name, fileKey, err)
		return nil, ""
	}
	if len(data) == 0 {
		return nil, ""
	}
	// Cache file locally
	localPath, cacheErr := cacheFeishuAttachment(data, fileName, mimeType)
	if cacheErr != nil {
		debug.Log("feishu", "adapter=%s cache file failed: %v", a.name, cacheErr)
	}
	return []Attachment{{
		Kind: AttachmentFile,
		Name: fileName,
		MIME: mimeType,
		Path: localPath,
	}}, ""
}

// downloadFeishuImage downloads an image by image_key via GET /im/v1/images/{image_key}.
func (a *feishuAdapter) downloadFeishuImage(ctx context.Context, imageKey string) ([]byte, string, error) {
	apiBase := a.resolveAPIBase()
	url := apiBase + "/im/v1/images/" + imageKey
	return a.downloadFeishuFile(ctx, url)
}

// downloadFeishuResource downloads a message resource (audio/file) via
// GET /im/v1/messages/{message_id}/resources/{file_key}?type=file.
func (a *feishuAdapter) downloadFeishuResource(ctx context.Context, messageID, fileKey, resourceType string) ([]byte, string, error) {
	apiBase := a.resolveAPIBase()
	url := apiBase + "/im/v1/messages/" + messageID + "/resources/" + fileKey + "?type=" + resourceType
	return a.downloadFeishuFile(ctx, url)
}

func (a *feishuAdapter) downloadFeishuFile(ctx context.Context, url string) ([]byte, string, error) {
	a.mu.RLock()
	token := a.token
	a.mu.RUnlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("Feishu download [%d] %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return data, strings.TrimSpace(resp.Header.Get("Content-Type")), nil
}

func (a *feishuAdapter) transcribeFeishuAudio(ctx context.Context, data []byte, mimeType string) string {
	ext := ".opus"
	if strings.Contains(mimeType, "wav") {
		ext = ".wav"
	} else if strings.Contains(mimeType, "mp3") || strings.Contains(mimeType, "mpeg") {
		ext = ".mp3"
	} else if strings.Contains(mimeType, "ogg") {
		ext = ".ogg"
	}

	src, err := os.CreateTemp("", "ggcode-feishu-audio-*"+ext)
	if err != nil {
		return ""
	}
	if _, err := src.Write(data); err != nil {
		src.Close()
		return ""
	}
	src.Close()
	audioPath := src.Name()
	cleanup := func() { _ = os.Remove(audioPath) }

	// Convert to wav if needed
	if ext != ".wav" {
		dst, err := os.CreateTemp("", "ggcode-feishu-audio-*.wav")
		if err != nil {
			cleanup()
			return ""
		}
		dst.Close()
		cmd := exec.Command("ffmpeg", "-y", "-i", audioPath, dst.Name())
		if _, err := cmd.CombinedOutput(); err != nil {
			_ = os.Remove(dst.Name())
			cleanup()
			return ""
		}
		cleanup()
		audioPath = dst.Name()
		cleanup = func() { _ = os.Remove(audioPath) }
	}
	defer cleanup()

	result, err := a.stt.Transcribe(ctx, imstt.Request{
		MIME: "audio/wav",
		Name: filepath.Base(audioPath),
		Path: audioPath,
	})
	if err != nil {
		debug.Log("feishu", "adapter=%s STT failed: %v", a.name, err)
		return ""
	}
	return strings.TrimSpace(result.Text)
}

// cacheFeishuAttachment writes attachment data to a temp file.
func cacheFeishuAttachment(data []byte, filename, mimeType string) (string, error) {
	ext := filepath.Ext(strings.TrimSpace(filename))
	if ext == "" {
		ext = feishuAttachmentExt(mimeType)
	}
	tmpFile, err := os.CreateTemp("", "ggcode-feishu-*"+ext)
	if err != nil {
		return "", err
	}
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return "", err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpFile.Name())
		return "", err
	}
	return tmpFile.Name(), nil
}

func feishuAttachmentExt(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "audio/ogg", "audio/opus":
		return ".opus"
	case "audio/wav":
		return ".wav"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "application/pdf":
		return ".pdf"
	default:
		return ".bin"
	}
}

func (a *feishuAdapter) parseMessageContent(content string) string {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return content
	}
	if text, ok := parsed["text"].(string); ok {
		return text
	}
	// Rich text (post) format
	for _, langContent := range parsed {
		langMap, ok := langContent.(map[string]any)
		if !ok {
			continue
		}
		contentArr, ok := langMap["content"].([]any)
		if !ok {
			continue
		}
		var texts []string
		for _, line := range contentArr {
			lineArr, ok := line.([]any)
			if !ok {
				continue
			}
			for _, elem := range lineArr {
				elemMap, ok := elem.(map[string]any)
				if !ok {
					continue
				}
				if text, ok := elemMap["text"].(string); ok {
					texts = append(texts, text)
				}
			}
		}
		return strings.Join(texts, "")
	}
	return content
}

func (a *feishuAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	a.mu.RLock()
	connected := a.connected
	a.mu.RUnlock()
	if !connected {
		return fmt.Errorf("Feishu bot %q is not online", a.name)
	}
	chatID := strings.TrimSpace(binding.ChannelID)
	if chatID == "" {
		return fmt.Errorf("Feishu channel is not configured for current directory")
	}
	content := strings.TrimSpace(a.outboundText(event))
	if content == "" {
		return nil
	}

	// Extract images and upload/send them
	images, remainingText := ExtractImagesFromText(content)
	for _, img := range images {
		if err := a.sendExtractedImage(ctx, chatID, img); err != nil {
			debug.Log("feishu", "adapter=%s image send failed: %v", a.name, err)
		}
	}

	// Send remaining text as post (rich text) for better formatting
	remainingText = strings.TrimSpace(remainingText)
	if remainingText == "" {
		return nil
	}
	chunks := splitFeishuMessage(remainingText, feishuMaxTextLen)
	for _, chunk := range chunks {
		if err := a.sendPostMessage(ctx, chatID, chunk); err != nil {
			// Fallback to plain text if post format fails
			debug.Log("feishu", "adapter=%s post send failed, falling back to text: %v", a.name, err)
			if fallbackErr := a.sendTextMessage(ctx, chatID, stripFeishuMarkdown(chunk)); fallbackErr != nil {
				return fallbackErr
			}
		}
	}
	return nil
}

func (a *feishuAdapter) outboundText(event OutboundEvent) string {
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

func (a *feishuAdapter) sendTextMessage(ctx context.Context, chatID, content string) error {
	a.mu.RLock()
	token := a.token
	a.mu.RUnlock()

	apiBase := a.resolveAPIBase()
	url := apiBase + "/im/v1/messages?receive_id_type=chat_id"
	msgContent, _ := json.Marshal(map[string]string{"text": content})
	body := map[string]any{
		"receive_id": chatID,
		"msg_type":   "text",
		"content":    string(msgContent),
	}
	bodyBytes, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Feishu API [%d] %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// TriggerTyping adds a "Typing" emoji reaction on the most recent message to indicate
// the bot is processing. Feishu does not have a native typing indicator, so we use
// a Typing reaction as a visual cue.
func (a *feishuAdapter) TriggerTyping(ctx context.Context, binding ChannelBinding) error {
	msgID := LastMessageID(binding)
	if msgID == "" {
		return nil
	}
	a.mu.RLock()
	token := a.token
	a.mu.RUnlock()

	apiBase := a.resolveAPIBase()
	url := apiBase + "/im/v1/messages/" + msgID + "/reactions"
	body := map[string]any{
		"reaction_type": map[string]string{"emoji_type": "Typing"},
	}
	bodyBytes, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		debug.Log("feishu", "adapter=%s typing reaction failed: %v", a.name, err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		debug.Log("feishu", "adapter=%s typing reaction [%d]: %s", a.name, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func (a *feishuAdapter) resolveAPIBase() string {
	switch strings.ToLower(a.domain) {
	case "lark":
		return "https://open.larksuite.com/open-apis"
	default:
		return "https://open.feishu.cn/open-apis"
	}
}

func (a *feishuAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:      a.name,
		Platform:  PlatformFeishu,
		Healthy:   healthy,
		Status:    status,
		LastError: lastErr,
		UpdatedAt: time.Now(),
	})
}

// sendExtractedImage resolves and sends an extracted image via Feishu.
func (a *feishuAdapter) sendExtractedImage(ctx context.Context, chatID string, img ExtractedImage) error {
	var data []byte
	var filename string

	switch img.Kind {
	case "url":
		if IsLocalFilePath(img.Data) {
			d, err := os.ReadFile(img.Data)
			if err != nil {
				return fmt.Errorf("read local image: %w", err)
			}
			data = d
			filename = filepath.Base(img.Data)
		} else {
			// Download the image
			resp, err := a.httpClient.Get(img.Data)
			if err != nil {
				return fmt.Errorf("download image: %w", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 400 {
				return fmt.Errorf("download image [%d]", resp.StatusCode)
			}
			d, err := io.ReadAll(resp.Body)
			if err != nil {
				return fmt.Errorf("read image data: %w", err)
			}
			data = d
			filename = filepath.Base(img.Data)
			if filename == "" || filename == "." {
				filename = "image.png"
			}
		}
	case "data_url":
		parts := strings.SplitN(img.Data, ",", 2)
		if len(parts) < 2 {
			return fmt.Errorf("invalid data URL")
		}
		d, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return fmt.Errorf("invalid base64: %w", err)
		}
		data = d
		ext := ".png"
		if strings.Contains(parts[0], "jpeg") || strings.Contains(parts[0], "jpg") {
			ext = ".jpg"
		} else if strings.Contains(parts[0], "gif") {
			ext = ".gif"
		} else if strings.Contains(parts[0], "webp") {
			ext = ".webp"
		}
		filename = "image" + ext
	default:
		return fmt.Errorf("unknown image kind: %s", img.Kind)
	}

	if len(data) == 0 {
		return nil
	}

	// Step 1: Upload image to get image_key
	imageKey, err := a.uploadImage(ctx, data, filename)
	if err != nil {
		return fmt.Errorf("upload image: %w", err)
	}

	// Step 2: Send image message
	return a.sendImageMessage(ctx, chatID, imageKey)
}

// uploadImage uploads an image to Feishu and returns the image_key.
func (a *feishuAdapter) uploadImage(ctx context.Context, data []byte, filename string) (string, error) {
	a.mu.RLock()
	token := a.token
	a.mu.RUnlock()

	apiBase := a.resolveAPIBase()
	url := apiBase + "/im/v1/images"

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	if err := writer.WriteField("image_type", "message"); err != nil {
		return "", fmt.Errorf("write image_type: %w", err)
	}

	part, err := writer.CreateFormFile("image", filename)
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return "", fmt.Errorf("write image data: %w", err)
	}

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respData, _ := io.ReadAll(resp.Body)

	var result map[string]any
	if err := json.Unmarshal(respData, &result); err != nil {
		return "", fmt.Errorf("parse upload response: %w", err)
	}

	code, _ := intValue(result["code"])
	if code != 0 {
		msg, _ := result["msg"].(string)
		return "", fmt.Errorf("Feishu upload [%d]: %s", code, msg)
	}

	data2, _ := result["data"].(map[string]any)
	if data2 == nil {
		return "", fmt.Errorf("Feishu upload: missing data in response")
	}
	imageKey, _ := data2["image_key"].(string)
	if imageKey == "" {
		return "", fmt.Errorf("Feishu upload: empty image_key")
	}
	return imageKey, nil
}

// sendImageMessage sends an image message to a Feishu chat using image_key.
func (a *feishuAdapter) sendImageMessage(ctx context.Context, chatID, imageKey string) error {
	a.mu.RLock()
	token := a.token
	a.mu.RUnlock()

	apiBase := a.resolveAPIBase()
	url := apiBase + "/im/v1/messages?receive_id_type=chat_id"
	msgContent, _ := json.Marshal(map[string]string{"image_key": imageKey})
	body := map[string]any{
		"receive_id": chatID,
		"msg_type":   "image",
		"content":    string(msgContent),
	}
	bodyBytes, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Feishu send image [%d] %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func splitFeishuMessage(text string, maxLen int) []string {
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

func intValueStr(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	var n int
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		n = n*10 + int(ch-'0')
	}
	return n, true
}

// feishuSilentLogger redirects SDK log output to debug.Log instead of os.Stdout.
// The default SDK logger writes to stdout which corrupts the TUI.
type feishuSilentLogger struct{}

// sendPostMessage sends an interactive card message with markdown rendering.
// Uses Feishu Card JSON 2.0 structure which supports full markdown including
// tables, headings, code blocks, and more. JSON 1.0's markdown tag does NOT
// support tables or headings.
func (a *feishuAdapter) sendPostMessage(ctx context.Context, chatID, content string) error {
	a.mu.RLock()
	token := a.token
	a.mu.RUnlock()

	apiBase := a.resolveAPIBase()
	url := apiBase + "/im/v1/messages?receive_id_type=chat_id"

	// Card JSON 2.0 structure: schema + config + body.elements
	// This enables full markdown rendering (tables, headings, code blocks, etc.)
	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"body": map[string]any{
			"elements": []any{
				map[string]any{
					"tag":     "markdown",
					"content": content,
				},
			},
		},
	}
	cardBytes, _ := json.Marshal(card)
	body := map[string]any{
		"receive_id": chatID,
		"msg_type":   "interactive",
		"content":    string(cardBytes),
	}
	bodyBytes, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Feishu card API [%d] %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// stripFeishuMarkdown removes basic markdown formatting for plain text fallback.
func stripFeishuMarkdown(text string) string {
	text = strings.ReplaceAll(text, "**", "")
	result := strings.Builder{}
	inCode := false
	for _, ch := range text {
		if ch == '`' {
			inCode = !inCode
			continue
		}
		result.WriteRune(ch)
	}
	return result.String()
}

func (l *feishuSilentLogger) Debug(_ context.Context, args ...interface{}) {
	debug.Log("feishu-sdk", "%v", args)
}
func (l *feishuSilentLogger) Info(_ context.Context, args ...interface{}) {
	debug.Log("feishu-sdk", "%v", args)
}
func (l *feishuSilentLogger) Warn(_ context.Context, args ...interface{}) {
	debug.Log("feishu-sdk", "%v", args)
}
func (l *feishuSilentLogger) Error(_ context.Context, args ...interface{}) {
	debug.Log("feishu-sdk", "%v", args)
}

// SendInteractive implements InteractiveSender using Feishu Card buttons.
// Works in both WebSocket and webhook modes. In WS mode, card callbacks
// are received via a parallel WS connection (runCardWebSocket) since the
// SDK's primary WS client silently drops MessageTypeCard frames.
func (a *feishuAdapter) SendInteractive(ctx context.Context, binding ChannelBinding, msg InteractiveMessage) (string, error) {
	a.mu.RLock()
	connected := a.connected
	a.mu.RUnlock()
	if !connected {
		return "", fmt.Errorf("Feishu bot %q is not online", a.name)
	}

	chatID := strings.TrimSpace(binding.ChannelID)
	if chatID == "" {
		return "", fmt.Errorf("Feishu channel is not configured")
	}

	// Build card with markdown + button column set
	elements := []any{
		map[string]any{
			"tag":     "markdown",
			"content": msg.Text,
		},
	}

	// Build buttons as a column_set
	var columns []map[string]any
	for _, btn := range msg.Buttons {
		btnElem := map[string]any{
			"tag": "button",
			"text": map[string]any{
				"tag":     "plain_text",
				"content": btn.Label,
			},
			"behaviors": []any{
				map[string]any{
					"type": "callback",
				},
			},
			"value": map[string]any{
				"choice": btn.Value,
			},
		}
		switch btn.Style {
		case "primary":
			btnElem["type"] = "primary"
		case "danger":
			btnElem["type"] = "danger"
		default:
			btnElem["type"] = "default"
		}
		columns = append(columns, map[string]any{
			"tag":      "column",
			"elements": []any{btnElem},
		})
	}
	if msg.MultiSelect {
		columns = append(columns, map[string]any{
			"tag": "column",
			"elements": []any{
				map[string]any{
					"tag":  "button",
					"type": "primary",
					"text": map[string]any{
						"tag":     "plain_text",
						"content": "✅ Done",
					},
					"behaviors": []any{
						map[string]any{
							"type": "callback",
						},
					},
					"value": map[string]any{
						"choice": "__done__",
					},
				},
			},
		})
	}

	if len(columns) > 0 {
		elements = append(elements, map[string]any{
			"tag":       "column_set",
			"flex_mode": "bisect",
			"columns":   columns,
		})
	}

	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"body": map[string]any{
			"elements": elements,
		},
	}

	cardBytes, _ := json.Marshal(card)
	apiBase := a.resolveAPIBase()
	url := apiBase + "/im/v1/messages?receive_id_type=chat_id"
	body := map[string]any{
		"receive_id": chatID,
		"msg_type":   "interactive",
		"content":    string(cardBytes),
	}
	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	a.mu.RLock()
	token := a.token
	a.mu.RUnlock()
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Feishu interactive API [%d] %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	// Extract message_id from response
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
		if data, ok := result["data"].(map[string]any); ok {
			if msgID, ok := data["message_id"].(string); ok {
				return msgID, nil
			}
		}
	}
	return "", nil
}
