package im

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	imstt "github.com/topcheer/ggcode/internal/im/stt"
	imagepkg "github.com/topcheer/ggcode/internal/image"
)

const (
	qqAPIBase         = "https://api.sgroup.qq.com"
	qqTokenURL        = "https://bots.qq.com/app/getAppAccessToken"
	qqGatewayPath     = "/gateway"
	qqShareURLPath    = "/v2/generate_url_link"
	qqMsgTypeText     = 0
	qqMsgTypeMarkdown = 2
	qqMsgTypeMedia    = 7
	qqFileTypeImage   = 1

	qqUploadCacheMaxEntries = 500
	qqUploadCacheTTL        = 30 * time.Minute
)

var qqMentionPrefix = regexp.MustCompile(`^@\S+\s*`)

type qqAdapter struct {
	name             string
	manager          *Manager
	httpClient       *http.Client
	appID            string
	appSecret        string
	credentialSource string
	markdownSupport  bool
	defaultChatType  string
	stt              imstt.Transcriber

	mu             sync.RWMutex
	writeMu        sync.Mutex
	ws             *websocket.Conn
	connected      bool
	token          string
	tokenExpiresAt time.Time
	lastSeq        int
	sessionID      string
	heartbeatEvery time.Duration
	chatTypes      map[string]string
	seen           map[string]time.Time

	uploadCache   map[string]qqUploadCacheEntry
	uploadCacheMu sync.RWMutex
}

type qqUploadCacheEntry struct {
	FileInfo  string
	ExpiresAt time.Time
}

func newQQAdapter(name string, imCfg config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) (*qqAdapter, error) {
	appID, appSecret, source := resolveQQCredentials(adapterCfg)
	adapter := &qqAdapter{
		name:             name,
		manager:          mgr,
		httpClient:       &http.Client{Timeout: 60 * time.Second},
		appID:            appID,
		appSecret:        appSecret,
		credentialSource: source,
		markdownSupport:  boolValue(adapterCfg.Extra, true, "markdown_support"),
		defaultChatType:  normalizeQQChatType(firstNonEmpty(strings.TrimSpace(stringValue(adapterCfg.Extra, "chat_type", "default_chat_type")), "c2c")),
		heartbeatEvery:   24 * time.Second,
		chatTypes:        make(map[string]string),
		seen:             make(map[string]time.Time),
		uploadCache:      make(map[string]qqUploadCacheEntry),
	}
	adapter.stt = buildSTTWithFallback(imCfg.STT, adapterCfg.Extra, resolveQQSTTConfig)
	return adapter, nil
}

func (a *qqAdapter) Name() string { return a.name }

func (a *qqAdapter) Start(ctx context.Context) {
	debug.Log("qq", "adapter=%s start credentials=%s", a.name, firstNonEmpty(strings.TrimSpace(a.credentialSource), "unknown"))
	a.publishState(false, "connecting", "")
	go a.run(ctx)
}

func resolveQQCredentials(adapterCfg config.IMAdapterConfig) (appID, appSecret, source string) {
	configAppID := strings.TrimSpace(stringValue(adapterCfg.Extra, "appid", "app_id"))
	configSecret := strings.TrimSpace(stringValue(adapterCfg.Extra, "appsecret", "app_secret", "client_secret"))
	if configAppID == "" && configSecret == "" {
		return "", "", "unconfigured"
	}
	return configAppID, configSecret, "config"
}

func (a *qqAdapter) run(ctx context.Context) {
	backoffs := []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second}
	attempt := 0
	for {
		if ctx.Err() != nil {
			a.publishState(false, "stopped", "")
			return
		}
		if err := a.connectAndServe(ctx); err != nil {
			a.publishState(false, "error", err.Error())
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

func (a *qqAdapter) connectAndServe(ctx context.Context) error {
	if a.appID == "" || a.appSecret == "" {
		return fmt.Errorf("QQ appid/appsecret are required")
	}
	debug.Log("qq", "adapter=%s requesting gateway", a.name)
	gatewayURL, err := a.gatewayURL(ctx)
	if err != nil {
		return err
	}
	debug.Log("qq", "adapter=%s gateway ready", a.name)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, gatewayURL, nil)
	if err != nil {
		return fmt.Errorf("connect QQ gateway: %w", err)
	}
	debug.Log("qq", "adapter=%s websocket connected", a.name)
	a.mu.Lock()
	a.ws = conn
	a.connected = false
	a.mu.Unlock()
	a.publishState(false, "handshaking", "")
	defer func() {
		a.mu.Lock()
		a.connected = false
		if a.ws != nil {
			_ = a.ws.Close()
		}
		a.ws = nil
		a.mu.Unlock()
	}()

	heartbeatCtx, heartbeatCancel := context.WithCancel(ctx)
	defer heartbeatCancel()
	go a.heartbeatLoop(heartbeatCtx)

	for {
		if ctx.Err() != nil {
			return nil
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			debug.Log("qq", "adapter=%s read error: %v", a.name, err)
			return fmt.Errorf("read QQ gateway: %s", qqCloseReason(err))
		}
		if err := a.handleGatewayPayload(ctx, data); err != nil {
			a.publishState(false, "warning", err.Error())
		}
	}
}

func (a *qqAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	if !a.isConnected() {
		return fmt.Errorf("QQ bot %q is not online", a.name)
	}
	channelID := strings.TrimSpace(binding.ChannelID)
	if channelID == "" {
		return fmt.Errorf("QQ channel is not configured for current directory")
	}
	content := strings.TrimSpace(a.outboundText(event))
	if content == "" {
		return nil
	}
	chatType := a.chatType(channelID)
	replyTo, replySeq := a.resolveReplyMode(binding)
	path := qqMessagePath(chatType, channelID)
	if path == "" {
		return fmt.Errorf("unknown QQ chat type %q for %s", chatType, channelID)
	}
	debug.Log("qq", "adapter=%s outbound kind=%s channel=%s len=%d reply_to=%q msg_seq=%d markdown=%t", a.name, event.Kind, channelID, len(content), replyTo, replySeq, a.markdownSupport)

	// Extract images from text and send them as rich media (msg_type: 7)
	images, remainingText := ExtractImagesFromText(content)
	var lastMsgID string
	for i, img := range images {
		b64, err := a.resolveImageSource(ctx, img)
		if err != nil {
			debug.Log("qq", "adapter=%s image resolve failed [%d/%d]: %v", a.name, i+1, len(images), err)
			continue
		}
		useReplyTo := replyTo
		useReplySeq := replySeq
		if lastMsgID != "" {
			useReplyTo = lastMsgID
			useReplySeq = 0
		}
		if err := a.sendImageFromBase64(ctx, chatType, channelID, b64, useReplyTo, useReplySeq); err != nil {
			debug.Log("qq", "adapter=%s image send failed [%d/%d]: %v", a.name, i+1, len(images), err)
			continue
		}
		debug.Log("qq", "adapter=%s image sent [%d/%d]", a.name, i+1, len(images))
	}

	// Send remaining text (images stripped)
	remainingText = strings.TrimSpace(remainingText)
	if remainingText != "" {
		useReplyTo := replyTo
		useReplySeq := replySeq
		if lastMsgID != "" {
			useReplyTo = lastMsgID
			useReplySeq = 0
		}
		deliveredReplyTo, err := a.sendTextMessage(ctx, path, chatType, remainingText, useReplyTo, useReplySeq)
		if err != nil {
			return err
		}
		if deliveredReplyTo != "" {
			lastMsgID = deliveredReplyTo
		}
	}

	if lastMsgID != "" {
		a.recordPassiveReply(binding, lastMsgID)
	}
	debug.Log("qq", "adapter=%s outbound delivered kind=%s channel=%s images=%d", a.name, event.Kind, channelID, len(images))
	return nil
}

func (a *qqAdapter) sendUnauthorized(ctx context.Context, channelID, replyTo string) error {
	return a.sendReplyText(ctx, channelID, replyTo, "你是未授权用户")
}

func (a *qqAdapter) sendReplyText(ctx context.Context, channelID, replyTo, content string) error {
	channelID = strings.TrimSpace(channelID)
	content = strings.TrimSpace(content)
	if channelID == "" || content == "" || !a.isConnected() {
		return nil
	}
	chatType := a.chatType(channelID)
	path := qqMessagePath(chatType, channelID)
	if path == "" {
		return nil
	}
	useMarkdown := a.markdownSupport
	replySeq := 0
	if strings.TrimSpace(replyTo) != "" {
		replySeq = 1
	}
	body := a.buildTextBodyWithMode(content, chatType, replyTo, replySeq, useMarkdown)
	if _, err := a.apiRequest(ctx, http.MethodPost, path, body, nil); err != nil {
		if useMarkdown && isQQMarkdownRejected(err) {
			body = a.buildTextBodyWithMode(content, chatType, replyTo, replySeq, false)
			_, retryErr := a.apiRequest(ctx, http.MethodPost, path, body, nil)
			return retryErr
		}
		return err
	}
	return nil
}

func (a *qqAdapter) outboundText(event OutboundEvent) string {
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

func (a *qqAdapter) buildTextBody(content, chatType, replyTo string, replySeq int) map[string]any {
	return a.buildTextBodyWithMode(content, chatType, replyTo, replySeq, a.markdownSupport)
}

func (a *qqAdapter) buildTextBodyWithMode(content, chatType, replyTo string, replySeq int, markdown bool) map[string]any {
	replyTo = strings.TrimSpace(replyTo)
	if markdown {
		body := map[string]any{
			"markdown": map[string]any{"content": content},
			"msg_type": qqMsgTypeMarkdown,
		}
		if replyTo != "" {
			body["msg_id"] = replyTo
			body["msg_seq"] = max(replySeq, 1)
		}
		return body
	}
	body := map[string]any{
		"content":  content,
		"msg_type": qqMsgTypeText,
	}
	if replyTo != "" {
		body["msg_id"] = replyTo
		body["msg_seq"] = max(replySeq, 1)
		if chatType == "guild" {
			body["message_reference"] = map[string]any{"message_id": replyTo}
		}
	}
	return body
}

func (a *qqAdapter) formatOutboundContent(content string) string {
	return content
}

func isQQMarkdownRejected(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "QQ API [500]") &&
		strings.Contains(msg, "invalid request") &&
		strings.Contains(msg, `"code":11255`)
}

func (a *qqAdapter) heartbeatLoop(ctx context.Context) {
	for {
		wait := a.currentHeartbeatEvery()
		if wait <= 0 {
			wait = 30 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
			seq, ok := a.sequence()
			payload := map[string]any{"op": 1, "d": nil}
			if ok {
				payload["d"] = seq
			}
			debug.Log("qq", "adapter=%s send heartbeat seq=%v interval=%s", a.name, payload["d"], wait)
			_ = a.writeJSON(payload)
		}
	}
}

func (a *qqAdapter) handleGatewayPayload(ctx context.Context, raw []byte) error {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("parse QQ gateway payload: %w", err)
	}
	if seq, ok := intValue(payload["s"]); ok {
		a.mu.Lock()
		if seq > a.lastSeq {
			a.lastSeq = seq
		}
		a.mu.Unlock()
	}
	op, _ := intValue(payload["op"])
	eventType, _ := payload["t"].(string)
	debug.Log("qq", "adapter=%s payload op=%d t=%q s=%v d_keys=%s", a.name, op, eventType, payload["s"], qqPayloadKeys(payload["d"]))
	switch op {
	case 10:
		debug.Log("qq", "adapter=%s gateway hello", a.name)
		if d, ok := payload["d"].(map[string]any); ok {
			if intervalMS, ok := intValue(d["heartbeat_interval"]); ok && intervalMS > 0 {
				a.mu.Lock()
				a.heartbeatEvery = time.Duration(float64(intervalMS)*0.8) * time.Millisecond
				a.mu.Unlock()
			}
		}
		return a.sendIdentify()
	case 11:
		debug.Log("qq", "adapter=%s heartbeat ack", a.name)
		return nil
	case 0:
		if eventType != "" {
			debug.Log("qq", "adapter=%s dispatch=%s", a.name, eventType)
		}
		if eventType == "READY" {
			debug.Log("qq", "adapter=%s gateway ready event", a.name)
			if d, ok := payload["d"].(map[string]any); ok {
				if sessionID, _ := d["session_id"].(string); sessionID != "" {
					a.mu.Lock()
					a.sessionID = sessionID
					a.connected = true
					a.mu.Unlock()
					a.publishState(true, "connected", "")
				}
			}
			return nil
		}
		switch eventType {
		case "C2C_MESSAGE_CREATE", "GROUP_AT_MESSAGE_CREATE", "DIRECT_MESSAGE_CREATE", "GUILD_MESSAGE_CREATE", "GUILD_AT_MESSAGE_CREATE":
			if d, ok := payload["d"].(map[string]any); ok {
				go a.handleMessageEvent(ctx, eventType, d)
			}
		default:
			if eventType != "" {
				debug.Log("qq", "adapter=%s unhandled dispatch=%s", a.name, eventType)
			}
		}
	default:
		debug.Log("qq", "adapter=%s unhandled op=%d", a.name, op)
	}
	return nil
}

func (a *qqAdapter) handleMessageEvent(ctx context.Context, eventType string, payload map[string]any) {
	msgID := strings.TrimSpace(stringFromAny(payload["id"]))
	if msgID == "" || a.seenMessage(msgID) {
		return
	}
	channelID, senderID, senderName, chatType := qqEnvelopeFields(eventType, payload)
	if channelID == "" {
		debug.Log("qq", "adapter=%s event=%s missing channel id", a.name, eventType)
		return
	}
	debug.Log("qq", "adapter=%s inbound event=%s channel=%s sender=%s", a.name, eventType, channelID, senderID)
	a.rememberChatType(channelID, chatType)
	text := strings.TrimSpace(stringFromAny(payload["content"]))
	if eventType == "GROUP_AT_MESSAGE_CREATE" {
		text = strings.TrimSpace(qqMentionPrefix.ReplaceAllString(text, ""))
	}
	attachments, voiceText := a.processAttachments(ctx, payload)
	if strings.TrimSpace(voiceText) != "" {
		if text != "" {
			text += "\n\n" + voiceText
		} else {
			text = voiceText
		}
	}
	inbound := InboundMessage{
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformQQ,
			ChannelID:  channelID,
			SenderID:   senderID,
			SenderName: senderName,
			MessageID:  msgID,
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
		if sendErr := a.sendReplyText(ctx, channelID, msgID, pairingResult.ReplyText); sendErr != nil {
			a.publishState(false, "warning", sendErr.Error())
		}
		if pairingResult.Bound && pairingResult.PreviousBinding != nil &&
			(pairingResult.NewBinding == nil ||
				pairingResult.PreviousBinding.Adapter != pairingResult.NewBinding.Adapter ||
				pairingResult.PreviousBinding.ChannelID != pairingResult.NewBinding.ChannelID) {
			if err := a.manager.SendDirect(ctx, *pairingResult.PreviousBinding, OutboundEvent{
				Kind: OutboundEventText,
				Text: "当前目录已解绑到其他 QQ 渠道，如需重新绑定请再次发起配对。",
			}); err != nil {
				a.publishState(false, "warning", err.Error())
			}
		}
		return
	}
	if err := a.manager.HandleInbound(ctx, inbound); err != nil {
		if err == ErrInboundChannelDenied {
			debug.Log("qq", "adapter=%s unauthorized inbound channel=%s", a.name, channelID)
			_ = a.sendUnauthorized(ctx, channelID, msgID)
			return
		}
		if err != ErrNoChannelBound {
			a.publishState(false, "warning", err.Error())
		}
	}
}

func (a *qqAdapter) processAttachments(ctx context.Context, payload map[string]any) ([]Attachment, string) {
	raw, _ := payload["attachments"].([]any)
	if len(raw) == 0 {
		return nil, ""
	}
	attachments := make([]Attachment, 0, len(raw))
	for _, item := range raw {
		att, ok := item.(map[string]any)
		if !ok {
			continue
		}
		url := normalizeQQURL(stringFromAny(att["url"]))
		filename := stringFromAny(att["filename"])
		contentType := strings.ToLower(strings.TrimSpace(stringFromAny(att["content_type"])))
		if isQQVoiceAttachment(contentType, filename) {
			transcript := strings.TrimSpace(stringFromAny(att["asr_refer_text"]))
			if transcript == "" && a.stt != nil {
				transcript = a.transcribeQQVoice(ctx, normalizeQQURL(stringFromAny(att["voice_wav_url"])), url, filename, contentType)
			}
			if transcript != "" {
				attachments = append(attachments, Attachment{
					Kind:       AttachmentVoice,
					Name:       filename,
					MIME:       contentType,
					URL:        url,
					Transcript: transcript,
				})
			}
			continue
		}
		if strings.HasPrefix(contentType, "image/") && url != "" {
			data, mimeType, err := a.downloadAttachment(ctx, url)
			if err != nil {
				continue
			}
			if decoded, decodeErr := imagepkg.Decode(data); decodeErr == nil && strings.TrimSpace(decoded.MIME) != "" {
				mimeType = decoded.MIME
			}
			localPath, err := cacheQQAttachment(data, filename, mimeType)
			if err != nil {
				continue
			}
			attachments = append(attachments, Attachment{
				Kind:       AttachmentImage,
				Name:       filename,
				MIME:       mimeType,
				Path:       localPath,
				URL:        url,
				DataBase64: base64.StdEncoding.EncodeToString(data),
			})
			continue
		}
		if url != "" {
			data, mimeType, err := a.downloadAttachment(ctx, url)
			if err != nil {
				continue
			}
			localPath, err := cacheQQAttachment(data, filename, firstNonEmpty(strings.TrimSpace(contentType), strings.TrimSpace(mimeType)))
			if err != nil {
				continue
			}
			attachments = append(attachments, Attachment{
				Kind: AttachmentFile,
				Name: filename,
				MIME: firstNonEmpty(strings.TrimSpace(contentType), strings.TrimSpace(mimeType)),
				Path: localPath,
				URL:  url,
			})
		}
	}
	return attachments, ""
}

func cacheQQAttachment(data []byte, filename, mimeType string) (string, error) {
	ext := filepath.Ext(strings.TrimSpace(filename))
	if ext == "" {
		ext = qqAttachmentExt(mimeType)
	}
	tmpFile, err := os.CreateTemp("", "ggcode-qq-*"+ext)
	if err != nil {
		return "", fmt.Errorf("cache QQ attachment: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return "", fmt.Errorf("write QQ attachment: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpFile.Name())
		return "", fmt.Errorf("close QQ attachment: %w", err)
	}
	return tmpFile.Name(), nil
}

func qqAttachmentExt(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "application/pdf":
		return ".pdf"
	case "text/plain":
		return ".txt"
	case "application/json":
		return ".json"
	default:
		return ""
	}
}

func (a *qqAdapter) transcribeQQVoice(ctx context.Context, wavURL, fallbackURL, filename, contentType string) string {
	downloadURL := firstNonEmpty(wavURL, fallbackURL)
	if downloadURL == "" {
		return ""
	}
	data, mimeType, err := a.downloadAttachment(ctx, downloadURL)
	if err != nil || len(data) == 0 {
		return ""
	}
	audioPath, cleanup, err := prepareQQAudioForSTT(data, filename, mimeType)
	if err != nil {
		return ""
	}
	defer cleanup()
	result, err := a.stt.Transcribe(ctx, imstt.Request{
		MIME: mimeType,
		Name: filepath.Base(audioPath),
		Path: audioPath,
	})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(result.Text)
}

func prepareQQAudioForSTT(data []byte, filename, mimeType string) (string, func(), error) {
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".bin"
	}
	src, err := os.CreateTemp("", "ggcode-qq-audio-*"+ext)
	if err != nil {
		return "", nil, err
	}
	if _, err := src.Write(data); err != nil {
		src.Close()
		return "", nil, err
	}
	if err := src.Close(); err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.Remove(src.Name()) }
	if strings.EqualFold(mimeType, "audio/wav") || strings.EqualFold(ext, ".wav") {
		return src.Name(), cleanup, nil
	}
	dst, err := os.CreateTemp("", "ggcode-qq-audio-*.wav")
	if err != nil {
		cleanup()
		return "", nil, err
	}
	dst.Close()
	cmd := exec.Command("ffmpeg", "-y", "-i", src.Name(), dst.Name())
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(dst.Name())
		return "", cleanup, fmt.Errorf("convert QQ audio with ffmpeg: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return dst.Name(), func() { cleanup(); _ = os.Remove(dst.Name()) }, nil
}

func (a *qqAdapter) downloadAttachment(ctx context.Context, url string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	token, err := a.ensureToken(ctx)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "QQBot "+token)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("download QQ attachment [%d]", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return data, strings.TrimSpace(resp.Header.Get("Content-Type")), nil
}

func (a *qqAdapter) sendIdentify() error {
	token, err := a.ensureToken(context.Background())
	if err != nil {
		return err
	}
	debug.Log("qq", "adapter=%s send identify", a.name)
	payload := map[string]any{
		"op": 2,
		"d": map[string]any{
			"token":   "QQBot " + token,
			"intents": (1 << 25) | (1 << 30) | (1 << 12),
			"shard":   []int{0, 1},
			"properties": map[string]any{
				"$os":      "darwin",
				"$browser": "ggcode",
				"$device":  "ggcode",
			},
		},
	}
	return a.writeJSON(payload)
}

func (a *qqAdapter) writeJSON(v any) error {
	a.mu.RLock()
	ws := a.ws
	a.mu.RUnlock()
	if ws == nil {
		return fmt.Errorf("QQ websocket is not connected")
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	return ws.WriteJSON(v)
}

func (a *qqAdapter) gatewayURL(ctx context.Context) (string, error) {
	var payload map[string]any
	if _, err := a.apiRequest(ctx, http.MethodGet, qqGatewayPath, nil, &payload); err != nil {
		return "", err
	}
	url := strings.TrimSpace(stringFromAny(payload["url"]))
	if url == "" {
		return "", fmt.Errorf("QQ gateway response missing url")
	}
	return url, nil
}

func (a *qqAdapter) apiRequest(ctx context.Context, method, path string, body any, out *map[string]any) (*http.Response, error) {
	var bodyBytes []byte
	if body != nil {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, err
		}
		bodyBytes = buf.Bytes()
	}
	return a.apiRequestWithRetry(ctx, method, path, bodyBytes, out, false)
}

func (a *qqAdapter) apiRequestWithRetry(ctx context.Context, method, path string, body []byte, out *map[string]any, retried bool) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	url := qqAPIBase + path
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	token, err := a.ensureToken(ctx)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "QQBot "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if out != nil {
		defer resp.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil && err != io.EOF {
			return nil, err
		}
		if resp.StatusCode >= 400 {
			if !retried && isQQTokenExpiredPayload(resp.StatusCode, payload) {
				debug.Log("qq", "adapter=%s request %s %s hit expired token, refreshing and retrying", a.name, method, path)
				a.clearToken()
				return a.apiRequestWithRetry(ctx, method, path, body, out, true)
			}
			*out = payload
			return nil, fmt.Errorf("QQ API [%d] %s: %s", resp.StatusCode, path, firstNonEmpty(stringFromAny(payload["message"]), http.StatusText(resp.StatusCode)))
		}
		*out = payload
		return resp, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		if !retried && isQQTokenExpiredBody(resp.StatusCode, data) {
			debug.Log("qq", "adapter=%s request %s %s hit expired token, refreshing and retrying", a.name, method, path)
			a.clearToken()
			return a.apiRequestWithRetry(ctx, method, path, body, out, true)
		}
		return nil, fmt.Errorf("QQ API [%d] %s: %s", resp.StatusCode, path, strings.TrimSpace(string(data)))
	}
	return resp, nil
}

func (a *qqAdapter) ensureToken(ctx context.Context) (string, error) {
	a.mu.RLock()
	token := a.token
	expires := a.tokenExpiresAt
	a.mu.RUnlock()
	if token != "" && time.Now().Before(expires.Add(-60*time.Second)) {
		return token, nil
	}
	debug.Log("qq", "adapter=%s refreshing token", a.name)
	payload := map[string]string{
		"appId":        a.appID,
		"clientSecret": a.appSecret,
	}
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, qqTokenURL, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	accessToken := strings.TrimSpace(stringFromAny(data["access_token"]))
	expiresIn, err := parseQQExpiresIn(data["expires_in"])
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 || accessToken == "" {
		return "", fmt.Errorf("QQ token request failed [%d]", resp.StatusCode)
	}
	debug.Log("qq", "adapter=%s token refreshed", a.name)
	a.mu.Lock()
	a.token = accessToken
	a.tokenExpiresAt = time.Now().Add(time.Duration(max(expiresIn, 3600)) * time.Second)
	a.mu.Unlock()
	return a.token, nil
}

func (a *qqAdapter) accessToken() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.token
}

func (a *qqAdapter) clearToken() {
	a.mu.Lock()
	a.token = ""
	a.tokenExpiresAt = time.Time{}
	a.mu.Unlock()
}

func parseQQExpiresIn(v any) (int, error) {
	raw := strings.TrimSpace(stringFromAny(v))
	if raw == "" {
		return 0, nil
	}
	secs, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parse QQ expires_in %q: %w", raw, err)
	}
	return secs, nil
}

func isQQTokenExpiredPayload(status int, payload map[string]any) bool {
	if status < 400 {
		return false
	}
	code, ok := intValue(payload["code"])
	if ok && code == 11244 {
		return true
	}
	errCode, ok := intValue(payload["err_code"])
	if ok && errCode == 11244 {
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(stringFromAny(payload["message"])))
	return strings.Contains(msg, "token not exist or expire")
}

func isQQTokenExpiredBody(status int, body []byte) bool {
	if status < 400 {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(string(body)))
	return strings.Contains(text, `"code":11244`) ||
		strings.Contains(text, `"err_code":11244`) ||
		strings.Contains(text, "token not exist or expire")
}

func (a *qqAdapter) isConnected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.connected && a.ws != nil
}

func qqCloseReason(err error) string {
	if closeErr, ok := err.(*websocket.CloseError); ok {
		switch closeErr.Code {
		case 4914:
			return "bot is offline/sandbox-only"
		case 4915:
			return "bot is banned"
		case 4004:
			return "invalid token"
		case 4006, 4007, 4009:
			return "session invalid"
		case 4008:
			return "rate limited"
		default:
			return fmt.Sprintf("close code %d: %s", closeErr.Code, closeErr.Text)
		}
	}
	return err.Error()
}

func (a *qqAdapter) sequence() (int, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.lastSeq <= 0 {
		return 0, false
	}
	return a.lastSeq, true
}

func (a *qqAdapter) currentHeartbeatEvery() time.Duration {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.heartbeatEvery
}

func (a *qqAdapter) chatType(channelID string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if chatType := strings.TrimSpace(a.chatTypes[channelID]); chatType != "" {
		return chatType
	}
	return a.defaultChatType
}

func (a *qqAdapter) rememberChatType(channelID, chatType string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.chatTypes[channelID] = normalizeQQChatType(chatType)
}

// TriggerTyping sends an input notify to indicate the bot is typing.
// Only supported for C2C chats; group chats do not support this API.
// Must include msg_id to be treated as a passive reply (avoids proactive message rate limits).
func (a *qqAdapter) TriggerTyping(ctx context.Context, binding ChannelBinding) error {
	channelID := strings.TrimSpace(binding.ChannelID)
	if channelID == "" {
		return nil
	}
	chatType := a.chatType(channelID)
	if chatType != "c2c" {
		return nil // QQ typing notify only works for C2C
	}

	// Need a msg_id to send as passive reply; without it QQ treats this as proactive message.
	msgID := strings.TrimSpace(binding.LastInboundMessageID)
	if msgID == "" {
		return nil // No passive context, skip to avoid proactive rate limit
	}

	path := "/v2/users/" + channelID + "/messages"
	seq := binding.PassiveReplyCount + 1
	if seq < 1 {
		seq = 1
	}
	body := map[string]any{
		"msg_type": 6,
		"input_notify": map[string]any{
			"input_type":   1,
			"input_second": 60,
		},
		"msg_id":  msgID,
		"msg_seq": seq,
	}
	if _, err := a.apiRequest(ctx, http.MethodPost, path, body, nil); err != nil {
		debug.Log("qq", "adapter=%s typing notify failed: %v", a.name, err)
		return err
	}
	return nil
}

func (a *qqAdapter) resolveReplyMode(binding ChannelBinding) (string, int) {
	replyTo := strings.TrimSpace(binding.LastInboundMessageID)
	if replyTo == "" {
		return "", 0
	}
	replySeq := binding.PassiveReplyCount + 1
	if replySeq < 1 {
		replySeq = 1
	}
	return replyTo, replySeq
}

func (a *qqAdapter) recordPassiveReply(binding ChannelBinding, replyTo string) {
	if a.manager == nil || strings.TrimSpace(binding.Workspace) == "" || strings.TrimSpace(replyTo) == "" {
		return
	}
	if err := a.manager.RecordPassiveReply(binding.Workspace, replyTo, time.Now()); err != nil && err != ErrNoChannelBound {
		debug.Log("qq", "adapter=%s record passive reply failed: %v", a.name, err)
	}
}

func (a *qqAdapter) sendTextMessage(ctx context.Context, path, chatType, content, replyTo string, replySeq int) (string, error) {
	useMarkdown := a.markdownSupport
	body := a.buildTextBodyWithMode(content, chatType, replyTo, replySeq, useMarkdown)
	if _, err := a.apiRequest(ctx, http.MethodPost, path, body, nil); err != nil {
		if useMarkdown && isQQMarkdownRejected(err) {
			debug.Log("qq", "adapter=%s outbound markdown rejected, retrying as text", a.name)
			body = a.buildTextBodyWithMode(content, chatType, replyTo, replySeq, false)
			if _, retryErr := a.apiRequest(ctx, http.MethodPost, path, body, nil); retryErr != nil {
				return "", retryErr
			}
			return replyTo, nil
		}
		return "", err
	}
	return replyTo, nil
}

// --- Image upload and media message ---

func qqUploadPath(chatType, targetID string) string {
	switch normalizeQQChatType(chatType) {
	case "c2c", "dm":
		return "/v2/users/" + targetID + "/files"
	case "group":
		return "/v2/groups/" + targetID + "/files"
	default:
		return ""
	}
}

func (a *qqAdapter) uploadCacheKey(base64Data, scope, targetID string, fileType int) string {
	h := md5.Sum([]byte(base64Data))
	return fmt.Sprintf("%x:%s:%s:%d", h, scope, targetID, fileType)
}

func (a *qqAdapter) getUploadCache(key string) (string, bool) {
	a.uploadCacheMu.RLock()
	defer a.uploadCacheMu.RUnlock()
	entry, ok := a.uploadCache[key]
	if !ok || time.Now().After(entry.ExpiresAt) {
		return "", false
	}
	return entry.FileInfo, true
}

func (a *qqAdapter) setUploadCache(key, fileInfo string) {
	a.uploadCacheMu.Lock()
	defer a.uploadCacheMu.Unlock()
	// Evict expired entries if at capacity
	if len(a.uploadCache) >= qqUploadCacheMaxEntries {
		now := time.Now()
		for k, v := range a.uploadCache {
			if now.After(v.ExpiresAt) {
				delete(a.uploadCache, k)
			}
		}
	}
	a.uploadCache[key] = qqUploadCacheEntry{
		FileInfo:  fileInfo,
		ExpiresAt: time.Now().Add(qqUploadCacheTTL),
	}
}

// uploadMedia uploads an image to QQ CDN and returns the file_info string.
func (a *qqAdapter) uploadMedia(ctx context.Context, chatType, targetID, base64Data string) (string, error) {
	uploadPath := qqUploadPath(chatType, targetID)
	if uploadPath == "" {
		return "", fmt.Errorf("unsupported chat type for media upload: %s", chatType)
	}

	// Check cache
	cacheKey := a.uploadCacheKey(base64Data, chatType, targetID, qqFileTypeImage)
	if fileInfo, ok := a.getUploadCache(cacheKey); ok {
		debug.Log("qq", "adapter=%s upload cache hit for %s", a.name, targetID)
		return fileInfo, nil
	}

	body := map[string]any{
		"file_type": qqFileTypeImage,
		"file_data": base64Data,
	}
	debug.Log("qq", "adapter=%s uploading image to %s data_len=%d", a.name, uploadPath, len(base64Data))

	var out map[string]any
	if _, err := a.apiRequest(ctx, http.MethodPost, uploadPath, body, &out); err != nil {
		return "", fmt.Errorf("upload QQ media: %w", err)
	}

	fileInfo := strings.TrimSpace(stringFromAny(out["file_info"]))
	if fileInfo == "" {
		if nested, ok := out["data"].(map[string]any); ok {
			fileInfo = strings.TrimSpace(stringFromAny(nested["file_info"]))
		}
	}
	if fileInfo == "" {
		return "", fmt.Errorf("upload QQ media: response missing file_info (keys=%s)", mapKeysCSV(out))
	}

	debug.Log("qq", "adapter=%s upload ok file_info=%s", a.name, truncateStr(fileInfo, 60))
	a.setUploadCache(cacheKey, fileInfo)
	return fileInfo, nil
}

// sendMediaMessage sends a rich media message (msg_type: 7) with an uploaded image.
func (a *qqAdapter) sendMediaMessage(ctx context.Context, path, chatType, fileInfo, replyTo string, replySeq int) error {
	body := map[string]any{
		"msg_type": qqMsgTypeMedia,
		"media": map[string]any{
			"file_info": fileInfo,
		},
	}
	if strings.TrimSpace(replyTo) != "" {
		body["msg_id"] = replyTo
		body["msg_seq"] = max(replySeq, 1)
	}
	debug.Log("qq", "adapter=%s send media msg_type=7 path=%s", a.name, path)
	if _, err := a.apiRequest(ctx, http.MethodPost, path, body, nil); err != nil {
		return fmt.Errorf("send QQ media message: %w", err)
	}
	return nil
}

// sendImageFromBase64 uploads a base64 image and sends it as a media message.
func (a *qqAdapter) sendImageFromBase64(ctx context.Context, chatType, channelID, base64Data, replyTo string, replySeq int) error {
	path := qqMessagePath(chatType, channelID)
	if path == "" {
		return fmt.Errorf("unknown QQ chat type %q for media", chatType)
	}
	fileInfo, err := a.uploadMedia(ctx, chatType, channelID, base64Data)
	if err != nil {
		return err
	}
	return a.sendMediaMessage(ctx, path, chatType, fileInfo, replyTo, replySeq)
}

// sendImageFromURL downloads an image from a URL and sends it as a media message.
func (a *qqAdapter) sendImageFromURL(ctx context.Context, chatType, channelID, imageURL, replyTo string, replySeq int) error {
	debug.Log("qq", "adapter=%s downloading image from URL: %s", a.name, truncateStr(imageURL, 120))
	data, mimeType, err := a.downloadImageURL(ctx, imageURL)
	if err != nil {
		return fmt.Errorf("download image %s: %w", truncateStr(imageURL, 60), err)
	}
	// Validate it's an image
	if decoded, decodeErr := imagepkg.Decode(data); decodeErr == nil && decoded.MIME != "" {
		mimeType = decoded.MIME
	}
	if !strings.HasPrefix(mimeType, "image/") {
		return fmt.Errorf("downloaded content is not an image: %s", mimeType)
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	return a.sendImageFromBase64(ctx, chatType, channelID, b64, replyTo, replySeq)
}

// sendImageFromLocal reads a local file and sends it as a media message.
func (a *qqAdapter) sendImageFromLocal(ctx context.Context, chatType, channelID, filePath, replyTo string, replySeq int) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read local image %s: %w", filePath, err)
	}
	decoded, err := imagepkg.Decode(data)
	if err != nil {
		return fmt.Errorf("decode local image %s: %w", filePath, err)
	}
	b64 := imagepkg.EncodeBase64(decoded)
	return a.sendImageFromBase64(ctx, chatType, channelID, b64, replyTo, replySeq)
}

func (a *qqAdapter) downloadImageURL(ctx context.Context, imageURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, imagepkg.MaxSize))
	if err != nil {
		return nil, "", err
	}
	mimeType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if mimeType == "" {
		mimeType = imagepkg.DetectMIME(data)
	}
	return data, mimeType, nil
}

func (a *qqAdapter) GenerateShareLink(ctx context.Context, callbackData string) (string, error) {
	payload := map[string]any{}
	callbackData = strings.TrimSpace(callbackData)
	if callbackData != "" {
		if len(callbackData) > 32 {
			return "", fmt.Errorf("QQ callback_data must be 32 characters or less")
		}
		payload["callback_data"] = callbackData
	}
	var out map[string]any
	if _, err := a.apiRequest(ctx, http.MethodPost, qqShareURLPath, payload, &out); err != nil {
		return "", err
	}
	debug.Log("qq", "adapter=%s share link response keys=%s", a.name, mapKeysCSV(out))
	url := strings.TrimSpace(stringFromAny(out["url"]))
	if url == "" {
		if data, ok := out["data"].(map[string]any); ok {
			url = strings.TrimSpace(stringFromAny(data["url"]))
			debug.Log("qq", "adapter=%s share link nested data keys=%s", a.name, mapKeysCSV(data))
		}
	}
	if url == "" {
		return "", fmt.Errorf("QQ share link response missing url")
	}
	return url, nil
}

func (a *qqAdapter) seenMessage(messageID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for id, seenAt := range a.seen {
		if now.Sub(seenAt) > 5*time.Minute {
			delete(a.seen, id)
		}
	}
	if _, ok := a.seen[messageID]; ok {
		return true
	}
	a.seen[messageID] = now
	return false
}

func qqPayloadKeys(value any) string {
	d, ok := value.(map[string]any)
	if !ok || len(d) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(d))
	for key := range d {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func (a *qqAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:      a.name,
		Platform:  PlatformQQ,
		Healthy:   healthy,
		Status:    status,
		LastError: lastErr,
		UpdatedAt: time.Now(),
	})
}

func qqEnvelopeFields(eventType string, payload map[string]any) (channelID, senderID, senderName, chatType string) {
	author, _ := payload["author"].(map[string]any)
	switch eventType {
	case "C2C_MESSAGE_CREATE":
		return stringFromAny(author["user_openid"]), stringFromAny(author["user_openid"]), stringFromAny(author["username"]), "c2c"
	case "GROUP_AT_MESSAGE_CREATE":
		return stringFromAny(payload["group_openid"]), stringFromAny(author["member_openid"]), stringFromAny(author["username"]), "group"
	case "DIRECT_MESSAGE_CREATE":
		return stringFromAny(payload["guild_id"]), stringFromAny(author["id"]), stringFromAny(author["username"]), "dm"
	default:
		member, _ := payload["member"].(map[string]any)
		name := firstNonEmpty(stringFromAny(member["nick"]), stringFromAny(author["username"]))
		return stringFromAny(payload["channel_id"]), stringFromAny(author["id"]), name, "guild"
	}
}

func qqMessagePath(chatType, channelID string) string {
	switch normalizeQQChatType(chatType) {
	case "c2c", "dm":
		return "/v2/users/" + channelID + "/messages"
	case "group":
		return "/v2/groups/" + channelID + "/messages"
	case "guild":
		return "/channels/" + channelID + "/messages"
	default:
		return ""
	}
}

func normalizeQQChatType(chatType string) string {
	switch strings.ToLower(strings.TrimSpace(chatType)) {
	case "user", "users", "c2c":
		return "c2c"
	case "group", "groups":
		return "group"
	case "dm":
		return "dm"
	case "guild", "channel", "channels":
		return "guild"
	default:
		return strings.ToLower(strings.TrimSpace(chatType))
	}
}

func normalizeQQURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "//") {
		return "https:" + raw
	}
	return raw
}

func isQQVoiceAttachment(contentType, filename string) bool {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	filename = strings.ToLower(strings.TrimSpace(filename))
	if contentType == "voice" || strings.HasPrefix(contentType, "audio/") {
		return true
	}
	for _, ext := range []string{".silk", ".amr", ".wav", ".mp3", ".m4a", ".ogg"} {
		if strings.HasSuffix(filename, ext) {
			return true
		}
	}
	return false
}

func resolveQQSTTConfig(global config.IMSTTConfig, extra map[string]interface{}) *config.IMSTTConfig {
	cfg := global
	if sttExtra, ok := extra["stt"].(map[string]interface{}); ok {
		cfg.Provider = firstNonEmpty(strings.TrimSpace(stringFromAny(sttExtra["provider"])), cfg.Provider)
		cfg.BaseURL = firstNonEmpty(strings.TrimSpace(stringFromAny(sttExtra["baseUrl"])), strings.TrimSpace(stringFromAny(sttExtra["base_url"])), cfg.BaseURL)
		cfg.APIKey = firstNonEmpty(strings.TrimSpace(stringFromAny(sttExtra["apiKey"])), strings.TrimSpace(stringFromAny(sttExtra["api_key"])), cfg.APIKey)
		cfg.Model = firstNonEmpty(strings.TrimSpace(stringFromAny(sttExtra["model"])), cfg.Model)
	}
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.APIKey) == "" || strings.TrimSpace(cfg.Model) == "" {
		return nil
	}
	return &cfg
}

func stringValue(extra map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := extra[key]; ok {
			if text := strings.TrimSpace(stringFromAny(value)); text != "" {
				return text
			}
		}
	}
	return ""
}

func boolValue(extra map[string]interface{}, defaultValue bool, keys ...string) bool {
	for _, key := range keys {
		if value, ok := extra[key]; ok {
			switch typed := value.(type) {
			case bool:
				return typed
			case string:
				switch strings.ToLower(strings.TrimSpace(typed)) {
				case "true", "1", "yes", "on":
					return true
				case "false", "0", "no", "off":
					return false
				}
			}
		}
	}
	return defaultValue
}

func stringFromAny(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprint(value)
	}
}

func mapKeysCSV(payload map[string]any) string {
	if len(payload) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func intValue(value interface{}) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		v, err := typed.Int64()
		return int(v), err == nil
	default:
		return 0, false
	}
}

func truncateStr(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

// resolveImageSource resolves an extracted image to a base64 string for upload.
func (a *qqAdapter) resolveImageSource(ctx context.Context, img ExtractedImage) (string, error) {
	switch img.Kind {
	case "data_url":
		// data:image/png;base64,XXXXX
		parts := strings.SplitN(img.Data, ",", 2)
		if len(parts) < 2 {
			return "", fmt.Errorf("invalid data URL")
		}
		// Validate base64 by decoding
		data, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return "", fmt.Errorf("invalid base64 in data URL: %w", err)
		}
		if _, err := imagepkg.Decode(data); err != nil {
			return "", fmt.Errorf("data URL is not a valid image: %w", err)
		}
		return parts[1], nil
	case "url":
		if IsLocalFilePath(img.Data) {
			// Local file path
			data, err := os.ReadFile(img.Data)
			if err != nil {
				return "", fmt.Errorf("read local image: %w", err)
			}
			decoded, err := imagepkg.Decode(data)
			if err != nil {
				return "", fmt.Errorf("decode local image: %w", err)
			}
			return imagepkg.EncodeBase64(decoded), nil
		}
		// HTTP(S) URL
		data, mimeType, err := a.downloadImageURL(ctx, img.Data)
		if err != nil {
			return "", err
		}
		if decoded, decodeErr := imagepkg.Decode(data); decodeErr == nil && decoded.MIME != "" {
			mimeType = decoded.MIME
		}
		if !strings.HasPrefix(mimeType, "image/") {
			return "", fmt.Errorf("content is not an image: %s", mimeType)
		}
		return base64.StdEncoding.EncodeToString(data), nil
	default:
		return "", fmt.Errorf("unknown image kind: %s", img.Kind)
	}
}
