package im

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
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

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	imstt "github.com/topcheer/ggcode/internal/im/stt"
	imagepkg "github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	feishuDefaultDomain    = "feishu"
	feishuMaxTextLen       = 28000
	feishuTokenExpireDelta = 300
	feishuInterMsgDelay    = 300 * time.Millisecond
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
	webhookPort int
	ctx         context.Context
	cancel      context.CancelFunc

	mu          sync.RWMutex
	connected   bool
	token       string
	tokenExpire time.Time
	botOpenID   string
	httpServer  *http.Server
	stt         imstt.Transcriber

	seenMu      sync.Mutex
	seenEvents  map[string]time.Time
	reactionAck reactionAckState
	seenNonces  map[string]time.Time
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
		if n, ok := intValueStr(v); ok && n > 0 {
			webhookPort = n
		} else {
			debug.Log("im", "feishu adapter %s: ignoring invalid webhook_port %q", name, v)
		}
	}
	adapter := &feishuAdapter{
		name:        name,
		manager:     mgr,
		httpClient:  util.NewInsecureAwareClient(30 * time.Second),
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

	cfg := global
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

		if strings.TrimSpace(global.BaseURL) == "" || strings.TrimSpace(global.APIKey) == "" || strings.TrimSpace(global.Model) == "" {
			return nil
		}
		return &global
	}

	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.APIKey) == "" || strings.TrimSpace(cfg.Model) == "" {
		return nil
	}
	return &cfg
}

func (a *feishuAdapter) Name() string { return a.name }

func (a *feishuAdapter) Start(ctx context.Context) {
	debug.Log("feishu", "adapter=%s start domain=%s webhookPort=%d", a.name, a.domain, a.webhookPort)
	ctx, cancel := context.WithCancel(ctx)
	a.ctx = ctx
	a.cancel = cancel
	a.publishState(false, "connecting", "")
	safego.Go("im.feishu.run", func() { a.run(ctx) })
}

func (a *feishuAdapter) Close() error {
	if a.cancel != nil {
		a.cancel()
	}
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

	if err := a.fetchTokenWithRetry(ctx, []time.Duration{3 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second}); err != nil {
		if ctx.Err() == nil {
			a.publishState(false, "error", fmt.Sprintf("token refresh: %v", err))
		}
		return
	}
	a.mu.Lock()
	a.connected = true
	a.mu.Unlock()

	if err := a.fetchBotInfo(ctx); err != nil {
		debug.Log("feishu", "adapter=%s fetchBotInfo error (non-fatal): %v", a.name, err)
	}

	a.publishState(true, "connected", "")
	debug.Log("feishu", "adapter=%s connected (token obtained)", a.name)

	safego.Go("im.feishu.tokenRefresh", func() { a.tokenRefreshLoop(ctx) })

	var wsErr error
	if a.webhookPort > 0 {
		a.startWebhookServer(ctx)
		<-ctx.Done()
	} else {

		wsErr = a.runWebSocket(ctx)
	}

	a.mu.Lock()
	a.connected = false
	a.mu.Unlock()
	if wsErr != nil {

		a.publishState(false, "error", wsErr.Error())
	} else {
		a.publishState(false, "stopped", "")
	}
}

func (a *feishuAdapter) fetchTokenWithRetry(ctx context.Context, backoffs []time.Duration) error {
	if len(backoffs) == 0 {
		backoffs = []time.Duration{time.Second}
	}
	attempt := 0
	for {
		err := a.refreshToken(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		delay := backoffs[min(attempt, len(backoffs)-1)]
		attempt++
		a.publishState(false, "error", fmt.Sprintf("token refresh: %v (retrying in %v)", err, delay))
		debug.Log("feishu", "adapter=%s startup token refresh failed (attempt %d), retrying in %v: %v", a.name, attempt, delay, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

func (a *feishuAdapter) runWebSocket(ctx context.Context) error {

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

	wsClient := larkws.NewClient(
		a.appID, a.appSecret,
		larkws.WithEventHandler(eventHandler),
		larkws.WithAutoReconnect(true),
		larkws.WithDomain(domain),
		larkws.WithLogLevel(larkcore.LogLevelError),
		larkws.WithLogger(&feishuSilentLogger{}),
	)

	a.mu.Lock()
	a.connected = true
	a.mu.Unlock()
	a.publishState(true, "online", "")
	debug.Log("feishu", "adapter=%s WS connected (SDK client)", a.name)

	if err := wsClient.Start(ctx); err != nil {
		a.mu.Lock()
		a.connected = false
		a.mu.Unlock()
		if ctx.Err() != nil {
			return nil
		}
		debug.Log("feishu", "adapter=%s SDK WS client exited: %v", a.name, err)

		return fmt.Errorf("ws client: %w", err)
	}

	a.mu.Lock()
	a.connected = false
	a.mu.Unlock()
	return nil
}

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

	attachments, voiceText := a.processAttachments(ctx, msgType, content, messageID)

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
		if _, sendErr := a.sendTextMessage(ctx, chatID, pairingResult.ReplyText); sendErr != nil {
			a.publishState(false, "warning", sendErr.Error())
		}
		if err := a.manager.NotifyPreviousBindingReplaced(ctx, pairingResult); err != nil {
			a.publishState(false, "warning", err.Error())
		}
		return
	}

	if err := a.manager.HandleInbound(ctx, inbound); err != nil {
		if err == ErrInboundChannelDenied {
			debug.Log("feishu", "adapter=%s unauthorized inbound chat=%s", a.name, chatID)
			return
		}

		if _, sendErr := a.sendTextMessage(ctx, chatID, "message could not be delivered (session not ready), please retry"); sendErr != nil {
			debug.Log("feishu", "adapter=%s failed to notify sender of delivery error: %v", a.name, sendErr)
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
	data, err := util.ReadAll(resp.Body, util.ReadLimitGeneral)
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
		Addr:         fmt.Sprintf(":%d", a.webhookPort),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
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

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	bodyBytes, err := util.ReadAll(r.Body, util.ReadLimitGeneral)
	if err != nil {
		http.Error(w, "read body error", http.StatusRequestEntityTooLarge)
		return
	}
	defer r.Body.Close()

	if a.encryptKey != "" {
		timestamp := r.Header.Get("X-Lark-Request-Timestamp")
		nonce := r.Header.Get("X-Lark-Request-Nonce")
		signature := r.Header.Get("X-Lark-Signature")
		if !a.verifySignature(timestamp, nonce, string(bodyBytes), signature) {
			debug.Log("feishu", "adapter=%s webhook signature verification failed", a.name)
			http.Error(w, "signature mismatch", http.StatusUnauthorized)
			return
		}

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

	if enc, ok := payload["encrypt"].(string); ok && enc != "" {
		if a.encryptKey == "" {
			debug.Log("feishu", "adapter=%s webhook body is encrypted (encrypt key set in the console) but no encrypt_key configured - dropping", a.name)
			http.Error(w, "encrypted callback but no encrypt_key configured", http.StatusBadRequest)
			return
		}
		decrypted, derr := decryptFeishuPayload(enc, a.encryptKey)
		if derr != nil {
			debug.Log("feishu", "adapter=%s webhook decrypt failed: %v", a.name, derr)
			http.Error(w, "decrypt failed", http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal(decrypted, &payload); err != nil {
			debug.Log("feishu", "adapter=%s decrypted webhook payload is not JSON: %v", a.name, err)
			http.Error(w, "invalid decrypted JSON", http.StatusBadRequest)
			return
		}
	}

	if !a.webhookTokenValid(payload) {
		debug.Log("feishu", "adapter=%s webhook verification token mismatch", a.name)
		http.Error(w, "token mismatch", http.StatusUnauthorized)
		return
	}

	if challenge, ok := payload["challenge"].(string); ok {
		resp := map[string]any{
			"challenge": challenge,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	header, _ := payload["header"].(map[string]any)
	if header == nil {
		return
	}

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

			w.WriteHeader(http.StatusOK)
			safego.Go("im.feishu.handleMessageEvent", func() { a.handleMessageEvent(a.ctx, event) })
			return
		}
	}

	if eventType == "card.action.trigger" {
		w.WriteHeader(http.StatusOK)
		safego.Go("im.feishu.handleCardAction", func() { a.handleCardAction(a.ctx, payload) })
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *feishuAdapter) webhookTokenValid(payload map[string]any) bool {
	if a.verifyToken == "" {
		return true
	}
	token := ""
	if header, ok := payload["header"].(map[string]any); ok {
		token, _ = header["token"].(string)
	}
	if token == "" {
		token, _ = payload["token"].(string)
	}
	return token == a.verifyToken
}

func decryptFeishuPayload(encB64, encryptKey string) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encB64)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	key := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext length %d is not a multiple of the block size", len(ciphertext))
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, key[:aes.BlockSize]).CryptBlocks(plain, ciphertext)

	pad := int(plain[len(plain)-1])
	if pad <= 0 || pad > aes.BlockSize || pad > len(plain) {
		return nil, fmt.Errorf("invalid PKCS7 padding")
	}
	for _, p := range plain[len(plain)-pad:] {
		if int(p) != pad {
			return nil, fmt.Errorf("invalid PKCS7 padding bytes")
		}
	}
	return plain[:len(plain)-pad], nil
}

func (a *feishuAdapter) verifySignature(timestamp, nonce, body, signature string) bool {
	mac := hmac.New(sha256.New, []byte(a.encryptKey))
	mac.Write([]byte(timestamp + nonce + a.encryptKey + body))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expected))
}

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

func (a *feishuAdapter) acceptNonce(timestamp, nonce string) bool {
	if nonce == "" {

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

func (a *feishuAdapter) handleCardAction(ctx context.Context, payload map[string]any) {
	if a.manager == nil {
		return
	}

	event, _ := payload["event"].(map[string]any)
	lookup := func(key string) (map[string]any, bool) {
		if event != nil {
			if v, ok := event[key].(map[string]any); ok {
				return v, true
			}
		}
		v, ok := payload[key].(map[string]any)
		return v, ok
	}

	action, _ := lookup("action")
	if action == nil {
		return
	}
	value, _ := action["value"].(map[string]any)
	if value == nil {
		return
	}
	choice, _ := value["choice"].(string)
	if choice == "" {
		return
	}

	openID := ""
	if operator, ok := lookup("operator"); ok {
		openID, _ = operator["open_id"].(string)
	}

	messageID, chatID := "", ""
	if actionCtx, ok := lookup("context"); ok {
		messageID, _ = actionCtx["open_message_id"].(string)
		chatID, _ = actionCtx["open_chat_id"].(string)
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

	// #1550: the WS branch reads message_type and routes non-text types
	// through processAttachments; the webhook branch did neither, so an
	// image message's {"image_key":...} JSON fell through
	// parseMessageContent (no text key, no post structure) and the RAW
	// JSON string reached the agent as text.
	msgType, _ := message["message_type"].(string)

	text := a.parseMessageContent(content)
	text = strings.TrimSpace(text)
	attachments, voiceText := a.processAttachments(ctx, msgType, content, messageID)
	if voiceText != "" {
		if text != "" {
			text += "\n\n" + voiceText
		} else {
			text = voiceText
		}
	}
	if text == "" && len(attachments) == 0 {
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
		Text:        text,
		Attachments: attachments,
	}

	pairingResult, err := a.manager.HandlePairingInbound(inbound)
	if err != nil && err != ErrNoSessionBound {
		a.publishState(false, "warning", err.Error())
	}
	if pairingResult.Consumed {
		if _, sendErr := a.sendTextMessage(ctx, chatID, pairingResult.ReplyText); sendErr != nil {
			a.publishState(false, "warning", sendErr.Error())
		}
		if err := a.manager.NotifyPreviousBindingReplaced(ctx, pairingResult); err != nil {
			a.publishState(false, "warning", err.Error())
		}
		return
	}

	if err := a.manager.HandleInbound(ctx, inbound); err != nil {
		if err == ErrInboundChannelDenied {
			debug.Log("feishu", "adapter=%s unauthorized inbound chat=%s", a.name, chatID)
			return
		}

		if _, sendErr := a.sendTextMessage(ctx, chatID, "message could not be delivered (session not ready), please retry"); sendErr != nil {
			debug.Log("feishu", "adapter=%s failed to notify sender of delivery error: %v", a.name, sendErr)
		}
		if err != ErrNoChannelBound {
			a.publishState(false, "warning", err.Error())
		}
	}
}

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

func (a *feishuAdapter) downloadFeishuImage(ctx context.Context, imageKey string) ([]byte, string, error) {
	apiBase := a.resolveAPIBase()
	url := apiBase + "/im/v1/images/" + imageKey
	return a.downloadFeishuFile(ctx, url)
}

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
		respBody, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)
		return nil, "", fmt.Errorf("Feishu download [%d] %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	data, err := util.ReadAll(resp.Body, util.ReadLimitGeneral)
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

	if post, ok := parsed["post"].(map[string]any); ok {
		parsed = post
	}
	var bestText string
	bestRank := -1
	for lang, langContent := range parsed {
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
		joined := strings.Join(texts, "")
		if joined == "" {
			continue
		}
		rank := 2
		switch lang {
		case "zh_cn":
			rank = 0
		case "en_us":
			rank = 1
		}
		if rank < bestRank || bestRank < 0 {
			bestText = joined
			bestRank = rank
		}
	}
	if bestRank >= 0 {
		return bestText
	}

	if strings.HasPrefix(strings.TrimSpace(content), "{") {
		return ""
	}
	return content
}

func (a *feishuAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	if !a.isConnected() {
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

	images, remainingText := ExtractImagesFromText(content)
	for _, img := range images {
		if err := a.sendExtractedImage(ctx, chatID, img); err != nil {
			debug.Log("feishu", "adapter=%s image send failed: %v", a.name, err)
		}
	}

	remainingText = strings.TrimSpace(remainingText)
	if remainingText == "" {
		return nil
	}
	chunks := splitFeishuMessage(remainingText, feishuMaxTextLen)
	for i, chunk := range chunks {
		msgID, err := a.sendPostMessage(ctx, chatID, chunk)
		if err != nil {

			debug.Log("feishu", "adapter=%s post send failed, falling back to text: %v", a.name, err)
			msgID, err = a.sendTextMessage(ctx, chatID, stripMarkdown(chunk))
			if err != nil {

				if !a.isConnected() {
					return fmt.Errorf("Feishu bot %q is not online", a.name)
				}
				return err
			}
		}
		a.recordOutboundMessage(binding, msgID)

		if i < len(chunks)-1 {
			select {
			case <-time.After(feishuInterMsgDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return nil
}

func (a *feishuAdapter) isConnected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.connected
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

func (a *feishuAdapter) sendTextMessage(ctx context.Context, chatID, content string) (string, error) {
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

	for attempt := 0; attempt <= maxRateLimitRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := a.httpClient.Do(req)
		if err != nil {
			return "", err
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRateLimitRetries {
			delay := parseRetryAfter(resp)
			resp.Body.Close()
			debug.Log("feishu", "adapter=%s sendText rate-limited, retry %d/%d after %v",
				a.name, attempt+1, maxRateLimitRetries, delay)
			if err := sleepRetry(ctx, delay); err != nil {
				return "", err
			}
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			respBody, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)
			return "", fmt.Errorf("Feishu API [%d] %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		}
		return parseFeishuMessageID(resp.Body)
	}
	return "", rateLimitExhausted("Feishu")
}

func (a *feishuAdapter) TriggerTyping(ctx context.Context, binding ChannelBinding) error {
	msgID := LastReactionTargetMessageID(binding)
	if msgID == "" || !a.reactionAck.NeedsSend(binding, msgID) {
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
		respBody, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)
		debug.Log("feishu", "adapter=%s typing reaction [%d]: %s", a.name, resp.StatusCode, strings.TrimSpace(string(respBody)))
		return nil
	}
	a.reactionAck.MarkSent(binding, msgID)
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
	a.mu.RLock()
	botOpenID := a.botOpenID
	a.mu.RUnlock()
	contactURI := ""
	if botOpenID != "" {
		contactURI = "https://applink.feishu.cn/client/chat/open?openId=" + botOpenID
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:       a.name,
		Platform:   PlatformFeishu,
		Healthy:    healthy,
		Status:     status,
		LastError:  lastErr,
		ContactURI: contactURI,
		UpdatedAt:  time.Now(),
	})
}

func (a *feishuAdapter) fetchBotInfo(ctx context.Context) error {
	apiBase := a.resolveAPIBase()
	url := apiBase + "/bot/v3/info"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	a.mu.RLock()
	tok := a.token
	a.mu.RUnlock()
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := util.ReadAll(resp.Body, util.ReadLimitGeneral)
	if err != nil {
		return err
	}
	var payload struct {
		Code int `json:"code"`
		Bot  struct {
			OpenID string `json:"open_id"`
		} `json:"bot"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("parse bot info: %w", err)
	}
	if payload.Code != 0 {
		return fmt.Errorf("bot info API returned code %d", payload.Code)
	}
	if payload.Bot.OpenID != "" {
		a.mu.Lock()
		a.botOpenID = payload.Bot.OpenID
		a.mu.Unlock()
		debug.Log("feishu", "adapter=%s bot open_id=%s", a.name, payload.Bot.OpenID)
	}
	return nil
}

func (a *feishuAdapter) sendExtractedImage(ctx context.Context, chatID string, img ExtractedImage) error {
	var data []byte
	var filename string

	switch img.Kind {
	case "url":
		if IsLocalFilePath(img.Data) {

			f, err := os.Open(img.Data)
			if err != nil {
				return fmt.Errorf("read local image: %w", err)
			}
			d, err := imagepkg.ReadLimited(f, imagepkg.MaxSize)
			f.Close()
			if err != nil {
				return fmt.Errorf("read local image: %w", err)
			}
			data = d
			filename = filepath.Base(img.Data)
		} else {

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, img.Data, nil)
			if err != nil {
				return fmt.Errorf("create image download request: %w", err)
			}
			resp, err := a.httpClient.Do(req)
			if err != nil {
				return fmt.Errorf("download image: %w", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 400 {
				return fmt.Errorf("download image [%d]", resp.StatusCode)
			}
			d, err := util.ReadAll(resp.Body, util.ReadLimitGeneral)
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

	imageKey, err := a.uploadImage(ctx, data, filename)
	if err != nil {
		return fmt.Errorf("upload image: %w", err)
	}

	return a.sendImageMessage(ctx, chatID, imageKey)
}

func (a *feishuAdapter) uploadImage(ctx context.Context, data []byte, filename string) (string, error) {
	const maxImageBytes = 10 << 20
	if len(data) > maxImageBytes {
		return "", fmt.Errorf("image is %d bytes; Feishu image upload limit is %d bytes, use a smaller image", len(data), maxImageBytes)
	}

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
	respData, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)

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

	for attempt := 0; attempt <= maxRateLimitRetries; attempt++ {
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

		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRateLimitRetries {
			delay := parseRetryAfter(resp)
			resp.Body.Close()
			debug.Log("feishu", "adapter=%s sendImage rate-limited, retry %d/%d after %v",
				a.name, attempt+1, maxRateLimitRetries, delay)
			if err := sleepRetry(ctx, delay); err != nil {
				return err
			}
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			respBody, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)
			return fmt.Errorf("Feishu send image [%d] %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		}
		var imgResult struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		imgBody, _ := util.ReadAll(io.LimitReader(resp.Body, 2048), util.ReadLimitGeneral)
		if json.Unmarshal(imgBody, &imgResult) == nil && imgResult.Code != 0 {
			return fmt.Errorf("Feishu send image error [%d]: %s", imgResult.Code, imgResult.Msg)
		}
		return nil
	}
	return rateLimitExhausted("Feishu")
}

func splitFeishuMessage(text string, maxLen int) []string {

	return splitMessageBytes(text, maxLen, true)
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

type feishuSilentLogger struct{}

func (a *feishuAdapter) sendPostMessage(ctx context.Context, chatID, content string) (string, error) {
	a.mu.RLock()
	token := a.token
	a.mu.RUnlock()

	apiBase := a.resolveAPIBase()
	url := apiBase + "/im/v1/messages?receive_id_type=chat_id"

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
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)
		return "", fmt.Errorf("Feishu card API [%d] %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return parseFeishuMessageID(resp.Body)
}

func (a *feishuAdapter) recordOutboundMessage(binding ChannelBinding, messageID string) {
	if a.manager == nil {
		return
	}
	if err := a.manager.RecordOutboundMessage(binding.Workspace, binding.Adapter, messageID); err != nil {
		debug.Log("feishu", "adapter=%s record outbound failed: %v", a.name, err)
	}
}

func parseFeishuMessageID(r io.Reader) (string, error) {
	data, err := util.ReadAll(r, util.ReadLimitGeneral)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", nil
	}
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", nil
	}

	if result.Code != 0 {
		return "", fmt.Errorf("Feishu API error [%d]: %s", result.Code, result.Msg)
	}
	return strings.TrimSpace(result.Data.MessageID), nil
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

	elements := []any{
		map[string]any{
			"tag":     "markdown",
			"content": msg.Text,
		},
	}

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
					"type":  "callback",
					"value": map[string]any{"choice": btn.Value},
				},
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
							"type":  "callback",
							"value": map[string]any{"choice": "__done__"},
						},
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
	debug.Log("feishu", "adapter=%s SendInteractive card JSON: %s", a.name, string(cardBytes))
	apiBase := a.resolveAPIBase()
	url := apiBase + "/im/v1/messages?receive_id_type=chat_id"
	body := map[string]any{
		"receive_id": chatID,
		"msg_type":   "interactive",
		"content":    string(cardBytes),
	}
	bodyBytes, _ := json.Marshal(body)

	a.mu.RLock()
	token := a.token
	a.mu.RUnlock()

	for attempt := 0; attempt <= maxRateLimitRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := a.httpClient.Do(req)
		if err != nil {
			return "", err
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRateLimitRetries {
			delay := parseRetryAfter(resp)
			resp.Body.Close()
			debug.Log("feishu", "adapter=%s SendInteractive rate-limited, retry %d/%d after %v",
				a.name, attempt+1, maxRateLimitRetries, delay)
			if err := sleepRetry(ctx, delay); err != nil {
				return "", err
			}
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			respBody, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)
			return "", fmt.Errorf("Feishu interactive API [%d] %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		}

		var result struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				MessageID string `json:"message_id"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
			if result.Code != 0 {
				return "", fmt.Errorf("Feishu interactive API error [%d]: %s", result.Code, result.Msg)
			}
			return strings.TrimSpace(result.Data.MessageID), nil
		}
		return "", nil
	}
	return "", rateLimitExhausted("Feishu")
}
