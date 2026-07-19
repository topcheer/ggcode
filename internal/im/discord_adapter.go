package im

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	imstt "github.com/topcheer/ggcode/internal/im/stt"
	imagepkg "github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	discordAPIBase         = "https://discord.com/api/v10"
	discordGatewayBotPath  = "/gateway/bot"
	discordMaxTextLen      = 2000
	discordIdentifyIntents = (1 << 0) | (1 << 9) | (1 << 10) | (1 << 12) // Guilds=1, GuildMessages=512, MessageContent=1024, DirectMessages=4096 = 5625

	// Discord Gateway opcodes
	discordOpDispatch       = 0
	discordOpHeartbeat      = 1
	discordOpIdentify       = 2
	discordOpReconnect      = 7
	discordOpInvalidSession = 9
	discordOpHello          = 10
	discordOpHeartbeatACK   = 11

	// Discord allows 5 messages per 5 seconds per channel. Adding a brief delay
	// between chunks prevents hitting the per-channel rate limit.
	// Source: https://docs.discord.com/developers/topics/rate-limits
	discordInterMsgDelay = 500 * time.Millisecond

	// discordSuppressEmbeds is the MessageFlags bit that prevents Discord from
	// auto-generating embed cards (link previews) from URLs in message content.
	// Without this, code output containing URLs creates noisy preview cards.
	// Source: https://discord.com/developers/docs/resources/message#message-object-message-flags
	discordSuppressEmbeds = 1 << 2 // = 4
)

type discordAdapter struct {
	name       string
	manager    *Manager
	httpClient *http.Client
	token      string
	apiBase    string
	stt        imstt.Transcriber

	mu          sync.RWMutex
	writeMu     sync.Mutex // protects websocket writes (gorilla/websocket not concurrent-safe)
	connected   bool
	sessionID   string
	sequence    int
	ws          *websocket.Conn
	botUserID   string // set from READY event
	reactionAck reactionAckState
}

func newDiscordAdapter(name string, imCfg config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) (*discordAdapter, error) {
	token := strings.TrimSpace(stringValue(adapterCfg.Extra, "token", "bot_token"))
	if token == "" {
		return nil, fmt.Errorf("Discord bot token is required for adapter %q", name)
	}
	apiBase := strings.TrimSpace(stringValue(adapterCfg.Extra, "api_base"))
	if apiBase == "" {
		apiBase = discordAPIBase
	}
	adapter := &discordAdapter{
		name:       name,
		manager:    mgr,
		httpClient: util.NewInsecureAwareClient(30 * time.Second),
		token:      token,
		apiBase:    apiBase,
	}
	adapter.stt = buildSTTWithFallback(imCfg.STT, adapterCfg.Extra, resolveDiscordSTTConfig)
	return adapter, nil
}

func (a *discordAdapter) Name() string { return a.name }

func (a *discordAdapter) Start(ctx context.Context) {
	debug.Log("discord", "adapter=%s start", a.name)
	a.publishState(false, "connecting", "")
	safego.Go("im.discord.run", func() { a.run(ctx) })
}

func (a *discordAdapter) Close() error {
	a.mu.Lock()
	ws := a.ws
	a.ws = nil
	a.connected = false
	a.mu.Unlock()
	if ws != nil {
		return ws.Close()
	}
	return nil
}

func (a *discordAdapter) run(ctx context.Context) {
	backoffs := []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second}
	attempt := 0
	for {
		if ctx.Err() != nil {
			a.publishState(false, "stopped", "")
			return
		}
		if err := a.connectAndServe(ctx); err != nil {
			a.publishState(false, "error", err.Error())
			debug.Log("discord", "adapter=%s error: %v", a.name, err)
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

func (a *discordAdapter) connectAndServe(ctx context.Context) error {
	gatewayURL, err := a.getGatewayBotURL(ctx)
	if err != nil {
		return fmt.Errorf("get gateway URL: %w", err)
	}
	debug.Log("discord", "adapter=%s gateway URL=%s", a.name, gatewayURL)

	wsURL := gatewayURL + "?v=10&encoding=json"
	// 30s dial timeout prevents indefinite hang on unreachable Discord gateway
	dialCtx, dialCancel := context.WithTimeout(ctx, 30*time.Second)
	conn, _, err := websocket.DefaultDialer.DialContext(dialCtx, wsURL, nil)
	dialCancel()
	if err != nil {
		return fmt.Errorf("dial gateway: %w", err)
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
	a.sequence = 0
	a.mu.Unlock()

	heartbeatCtx, heartbeatCancel := context.WithCancel(ctx)
	defer heartbeatCancel()

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
		var payload map[string]any
		if err := json.Unmarshal(msgBytes, &payload); err != nil {
			continue
		}

		op := jsonInt(payload["op"])
		s := jsonInt(payload["s"])
		if s > 0 {
			a.mu.Lock()
			a.sequence = s
			a.mu.Unlock()
		}

		switch op {
		case discordOpHello:
			d, _ := payload["d"].(map[string]any)
			interval := 41250
			if d != nil {
				if iv, ok := intValue(d["heartbeat_interval"]); ok && iv > 0 {
					interval = iv
				}
			}
			safego.Go("im.discord.heartbeat", func() { a.heartbeatLoop(heartbeatCtx, conn, interval) })
			a.sendIdentify(conn)

		case discordOpHeartbeatACK:
			// acknowledged

		case discordOpDispatch:
			t, _ := payload["t"].(string)
			if t == "READY" {
				d, _ := payload["d"].(map[string]any)
				if d != nil {
					sid, _ := d["session_id"].(string)
					var botID string
					if user, _ := d["user"].(map[string]any); user != nil {
						botID, _ = user["id"].(string)
					}
					a.mu.Lock()
					a.sessionID = sid
					a.botUserID = botID
					a.connected = true
					a.mu.Unlock()
				}
				a.publishState(true, "connected", "")
				debug.Log("discord", "adapter=%s connected", a.name)
			} else if t == "MESSAGE_CREATE" {
				d, _ := payload["d"].(map[string]any)
				if d != nil {
					a.handleMessage(ctx, d)
				}
			} else if t == "INTERACTION_CREATE" {
				d, _ := payload["d"].(map[string]any)
				if d != nil {
					a.handleInteraction(ctx, d)
				}
			}

		case discordOpReconnect:
			debug.Log("discord", "adapter=%s gateway requested reconnect", a.name)
			return fmt.Errorf("gateway requested reconnect")

		case discordOpInvalidSession:
			debug.Log("discord", "adapter=%s invalid session", a.name)
			a.mu.Lock()
			a.sessionID = ""
			a.mu.Unlock()
			return fmt.Errorf("invalid session")
		}
	}
}

func (a *discordAdapter) sendIdentify(conn *websocket.Conn) {
	payload := map[string]any{
		"op": discordOpIdentify,
		"d": map[string]any{
			"token":   a.token,
			"intents": discordIdentifyIntents,
			"properties": map[string]string{
				"os":      "linux",
				"browser": "ggcode",
				"device":  "ggcode",
			},
		},
	}
	data, _ := json.Marshal(payload)
	a.writeMu.Lock()
	_ = conn.WriteMessage(websocket.TextMessage, data)
	a.writeMu.Unlock()
}

func (a *discordAdapter) heartbeatLoop(ctx context.Context, conn *websocket.Conn, intervalMs int) {
	ticker := time.NewTicker(time.Duration(intervalMs) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.mu.RLock()
			seq := a.sequence
			a.mu.RUnlock()
			payload := map[string]any{
				"op": discordOpHeartbeat,
				"d":  seq,
			}
			data, _ := json.Marshal(payload)
			a.writeMu.Lock()
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				a.writeMu.Unlock()
				debug.Log("discord", "adapter=%s heartbeat write error: %v", a.name, err)
				conn.Close() // Force ReadMessage to unblock → triggers reconnect
				return
			}
			a.writeMu.Unlock()
		}
	}
}

func (a *discordAdapter) handleMessage(ctx context.Context, d map[string]any) {
	author, _ := d["author"].(map[string]any)
	if author == nil {
		return
	}
	if isBot, _ := author["bot"].(bool); isBot {
		return
	}

	channelID, _ := d["channel_id"].(string)
	content, _ := d["content"].(string)
	senderID, _ := author["id"].(string)
	senderName, _ := author["username"].(string)
	messageID, _ := d["id"].(string)

	content = strings.TrimSpace(content)

	// Process attachments (images, files, audio)
	attachments, voiceText := a.processDiscordAttachments(ctx, d)
	if voiceText != "" {
		if content != "" {
			content += "\n\n" + voiceText
		} else {
			content = voiceText
		}
	}

	if content == "" && len(attachments) == 0 {
		return
	}

	debug.Log("discord", "adapter=%s inbound channel=%s sender=%s len=%d attachments=%d", a.name, channelID, senderID, len(content), len(attachments))

	inbound := InboundMessage{
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformDiscord,
			ChannelID:  channelID,
			SenderID:   senderID,
			SenderName: senderName,
			MessageID:  messageID,
			ReceivedAt: time.Now(),
		},
		Text:        content,
		Attachments: attachments,
	}

	pairingResult, err := a.manager.HandlePairingInbound(inbound)
	if err != nil && err != ErrNoSessionBound {
		a.publishState(false, "warning", err.Error())
	}
	if pairingResult.Consumed {
		if sendErr := a.sendChannelMessage(ctx, channelID, pairingResult.ReplyText); sendErr != nil {
			a.publishState(false, "warning", sendErr.Error())
		}
		if err := a.manager.NotifyPreviousBindingReplaced(ctx, pairingResult); err != nil {
			a.publishState(false, "warning", err.Error())
		}
		return
	}

	if err := a.manager.HandleInbound(ctx, inbound); err != nil {
		if err == ErrInboundChannelDenied {
			debug.Log("discord", "adapter=%s unauthorized inbound channel=%s", a.name, channelID)
			return
		}
		if err != ErrNoChannelBound {
			a.publishState(false, "warning", err.Error())
		}
	}
}

// processDiscordAttachments extracts image, audio, and file attachments from a Discord message.
func (a *discordAdapter) processDiscordAttachments(ctx context.Context, d map[string]any) ([]Attachment, string) {
	raw, ok := d["attachments"].([]any)
	if !ok || len(raw) == 0 {
		return nil, ""
	}
	var attachments []Attachment
	var voiceText string

	for _, item := range raw {
		att, ok := item.(map[string]any)
		if !ok {
			continue
		}
		url, _ := att["url"].(string)
		contentType, _ := att["content_type"].(string)
		filename, _ := att["filename"].(string)
		if url == "" {
			continue
		}

		if strings.HasPrefix(contentType, "image/") {
			// Download image data
			data, mimeType, err := a.downloadDiscordAttachment(ctx, url)
			if err != nil {
				debug.Log("discord", "adapter=%s download image failed: %v", a.name, err)
				continue
			}
			if decoded, decodeErr := imagepkg.Decode(data); decodeErr == nil && strings.TrimSpace(decoded.MIME) != "" {
				mimeType = decoded.MIME
			}
			attachments = append(attachments, Attachment{
				Kind:       AttachmentImage,
				Name:       filename,
				MIME:       mimeType,
				DataBase64: base64.StdEncoding.EncodeToString(data),
			})
		} else if strings.HasPrefix(contentType, "audio/") {
			// Audio/voice attachment — transcribe if STT is available
			transcript := ""
			if a.stt != nil {
				transcript = a.transcribeDiscordAudio(ctx, url, filename, contentType)
			}
			attachments = append(attachments, Attachment{
				Kind:       AttachmentVoice,
				Name:       filename,
				MIME:       contentType,
				Transcript: transcript,
			})
			if transcript != "" {
				if voiceText != "" {
					voiceText += "\n\n" + transcript
				} else {
					voiceText = transcript
				}
			}
		} else {
			// Non-image/non-audio file: download and cache locally
			data, mimeType, err := a.downloadDiscordAttachment(ctx, url)
			if err != nil {
				debug.Log("discord", "adapter=%s download file failed: %v", a.name, err)
				continue
			}
			localPath, cacheErr := cacheDiscordAttachment(data, filename, firstNonEmpty(contentType, mimeType))
			if cacheErr != nil {
				debug.Log("discord", "adapter=%s cache file failed: %v", a.name, cacheErr)
			}
			attachments = append(attachments, Attachment{
				Kind: AttachmentFile,
				Name: filename,
				MIME: firstNonEmpty(contentType, mimeType),
				Path: localPath,
			})
		}
	}
	return attachments, voiceText
}

func (a *discordAdapter) downloadDiscordAttachment(ctx context.Context, url string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("Discord download [%d]", resp.StatusCode)
	}
	data, err := util.ReadAll(resp.Body, util.ReadLimitGeneral)
	if err != nil {
		return nil, "", err
	}
	return data, strings.TrimSpace(resp.Header.Get("Content-Type")), nil
}

func cacheDiscordAttachment(data []byte, filename, mimeType string) (string, error) {
	ext := filepath.Ext(strings.TrimSpace(filename))
	if ext == "" {
		ext = discordAttachmentExt(mimeType)
	}
	tmpFile, err := os.CreateTemp("", "ggcode-discord-*"+ext)
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

func discordAttachmentExt(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "application/pdf":
		return ".pdf"
	default:
		return ".bin"
	}
}

func (a *discordAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	a.mu.RLock()
	connected := a.connected
	a.mu.RUnlock()
	if !connected {
		return fmt.Errorf("Discord bot %q is not online", a.name)
	}
	channelID := strings.TrimSpace(binding.ChannelID)
	if channelID == "" {
		return fmt.Errorf("Discord channel is not configured for current directory")
	}
	content := strings.TrimSpace(a.outboundText(event))
	if content == "" {
		return nil
	}

	// Extract images from text and send them first
	images, remainingText := ExtractImagesFromText(content)
	for _, img := range images {
		if err := a.sendExtractedImage(ctx, channelID, img); err != nil {
			debug.Log("discord", "adapter=%s image send failed: %v", a.name, err)
		}
	}

	// Send remaining text
	remainingText = strings.TrimSpace(remainingText)
	if remainingText == "" {
		return nil
	}
	chunks := splitDiscordMessage(remainingText, discordMaxTextLen)
	for i, chunk := range chunks {
		// Discord allows 5 messages per 5 seconds per channel.
		// Adding a brief delay between chunks prevents hitting the rate limit.
		if i > 0 {
			select {
			case <-time.After(discordInterMsgDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if err := a.sendChannelMessage(ctx, channelID, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (a *discordAdapter) outboundText(event OutboundEvent) string {
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

func (a *discordAdapter) sendChannelMessage(ctx context.Context, channelID, content string) error {
	url := a.apiBase + "/channels/" + channelID + "/messages"
	body := map[string]any{
		"content": content,
		"flags":   discordSuppressEmbeds, // Suppress link preview embeds for cleaner UX
	}
	bodyBytes, _ := json.Marshal(body)

	for attempt := 0; attempt <= maxRateLimitRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bot "+a.token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := a.httpClient.Do(req)
		if err != nil {
			return err
		}

		// Handle HTTP 429 (Too Many Requests) with Retry-After backoff.
		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			if attempt < maxRateLimitRetries {
				delay := parseRetryAfter(resp)
				debug.Log("discord", "adapter=%s createMessage 429 rate limited, retry %d/%d in %v",
					a.name, attempt+1, maxRateLimitRetries, delay)
				if err := sleepRetry(ctx, delay); err != nil {
					return err
				}
				continue
			}
			return rateLimitExhausted("Discord")
		}

		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			respBody, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)
			return fmt.Errorf("Discord API [%d] %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		}
		return nil
	}
	return rateLimitExhausted("Discord")
}

// TriggerTyping adds a reaction acknowledgement to the latest real user message
// when possible, and falls back to the native typing indicator when there is no
// target message to acknowledge yet.
func (a *discordAdapter) TriggerTyping(ctx context.Context, binding ChannelBinding) error {
	channelID := strings.TrimSpace(binding.ChannelID)
	if channelID == "" {
		return nil
	}
	messageID := strings.TrimSpace(LastReactionTargetMessageID(binding))
	if messageID == "" {
		return a.triggerNativeTyping(ctx, channelID)
	}
	if !a.reactionAck.NeedsSend(binding, messageID) {
		return nil
	}
	emoji := reactionAckValue(PlatformDiscord, messageID)
	if emoji == "" {
		return a.triggerNativeTyping(ctx, channelID)
	}
	url := a.apiBase + "/channels/" + channelID + "/messages/" + messageID + "/reactions/" + url.PathEscape(emoji) + "/@me"
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+a.token)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		debug.Log("discord", "adapter=%s typing reaction failed: %v", a.name, err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)
		debug.Log("discord", "adapter=%s typing reaction failed [%d]: %s", a.name, resp.StatusCode, strings.TrimSpace(string(respBody)))
		return nil
	}
	a.reactionAck.MarkSent(binding, messageID)
	return nil
}

func (a *discordAdapter) triggerNativeTyping(ctx context.Context, channelID string) error {
	url := a.apiBase + "/channels/" + channelID + "/typing"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+a.token)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		debug.Log("discord", "adapter=%s typing failed: %v", a.name, err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)
		debug.Log("discord", "adapter=%s typing failed [%d]: %s", a.name, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func (a *discordAdapter) getGatewayBotURL(ctx context.Context) (string, error) {
	url := a.apiBase + discordGatewayBotPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bot "+a.token)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := util.ReadAll(resp.Body, util.ReadLimitGeneral)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("Discord API [%d] %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return "", err
	}
	gwURL, _ := result["url"].(string)
	if gwURL == "" {
		return "", fmt.Errorf("Discord gateway URL is empty")
	}
	return gwURL, nil
}

func (a *discordAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	contactURI := ""
	if a.botUserID != "" {
		contactURI = "https://discord.com/users/" + a.botUserID
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:       a.name,
		Platform:   PlatformDiscord,
		Healthy:    healthy,
		Status:     status,
		LastError:  lastErr,
		ContactURI: contactURI,
		UpdatedAt:  time.Now(),
	})
}

// sendExtractedImage resolves and sends an extracted image to a Discord channel.
func (a *discordAdapter) sendExtractedImage(ctx context.Context, channelID string, img ExtractedImage) error {
	switch img.Kind {
	case "url":
		if IsLocalFilePath(img.Data) {
			data, err := os.ReadFile(img.Data)
			if err != nil {
				return fmt.Errorf("read local image: %w", err)
			}
			return a.sendFileMessage(ctx, channelID, filepath.Base(img.Data), data, "")
		}
		// For remote URLs, send as an embed URL in a regular message
		return a.sendChannelMessage(ctx, channelID, img.Data)
	case "data_url":
		parts := strings.SplitN(img.Data, ",", 2)
		if len(parts) < 2 {
			return fmt.Errorf("invalid data URL")
		}
		data, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return fmt.Errorf("invalid base64 in data URL: %w", err)
		}
		ext := ".png"
		if strings.Contains(parts[0], "jpeg") || strings.Contains(parts[0], "jpg") {
			ext = ".jpg"
		} else if strings.Contains(parts[0], "gif") {
			ext = ".gif"
		} else if strings.Contains(parts[0], "webp") {
			ext = ".webp"
		}
		return a.sendFileMessage(ctx, channelID, "image"+ext, data, "")
	default:
		return fmt.Errorf("unknown image kind: %s", img.Kind)
	}
}

// sendFileMessage uploads a file to a Discord channel via multipart/form-data.
func (a *discordAdapter) sendFileMessage(ctx context.Context, channelID, filename string, data []byte, caption string) error {
	url := a.apiBase + "/channels/" + channelID + "/messages"

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Build the payload_json field
	payload := map[string]any{}
	if caption != "" {
		payload["content"] = caption
	}
	payloadJSON, _ := json.Marshal(payload)
	if err := writer.WriteField("payload_json", string(payloadJSON)); err != nil {
		return fmt.Errorf("write payload_json: %w", err)
	}

	// Add file
	part, err := writer.CreateFormFile("files[0]", filename)
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return fmt.Errorf("write file data: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+a.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)
		return fmt.Errorf("Discord file upload [%d] %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func splitDiscordMessage(text string, maxLen int) []string {
	return SplitMarkdown(strings.TrimSpace(text), maxLen)
}

func jsonInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case float32:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

// transcribeDiscordAudio downloads an audio attachment and transcribes it via STT.
func (a *discordAdapter) transcribeDiscordAudio(ctx context.Context, url, filename, contentType string) string {
	data, _, err := a.downloadDiscordAttachment(ctx, url)
	if err != nil || len(data) == 0 {
		debug.Log("discord", "adapter=%s download audio failed: %v", a.name, err)
		return ""
	}

	// Determine extension
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = audioExtFromMIME(contentType)
	}

	// Write to temp file
	src, err := os.CreateTemp("", "ggcode-discord-audio-*"+ext)
	if err != nil {
		return ""
	}
	if _, err := src.Write(data); err != nil {
		src.Close()
		_ = os.Remove(src.Name())
		return ""
	}
	src.Close()
	audioPath := src.Name()
	cleanup := func() { _ = os.Remove(audioPath) }

	// Convert to wav if needed
	if ext != ".wav" {
		dst, err := os.CreateTemp("", "ggcode-discord-audio-*.wav")
		if err != nil {
			cleanup()
			return ""
		}
		dst.Close()
		cmd := exec.Command("ffmpeg", "-y", "-i", audioPath, dst.Name())
		if _, err := cmd.CombinedOutput(); err != nil {
			_ = os.Remove(dst.Name())
			cleanup()
			debug.Log("discord", "adapter=%s ffmpeg convert failed: %v", a.name, err)
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
		debug.Log("discord", "adapter=%s STT failed: %v", a.name, err)
		return ""
	}
	debug.Log("discord", "adapter=%s STT result: %d chars", a.name, len(result.Text))
	return result.Text
}

func resolveDiscordSTTConfig(global config.IMSTTConfig, extra map[string]interface{}) *config.IMSTTConfig {
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

// SendInteractive implements InteractiveSender using Discord message components.
func (a *discordAdapter) SendInteractive(ctx context.Context, binding ChannelBinding, msg InteractiveMessage) (string, error) {
	a.mu.RLock()
	connected := a.connected
	a.mu.RUnlock()
	if !connected {
		return "", fmt.Errorf("Discord bot %q is not online", a.name)
	}

	channelID := strings.TrimSpace(binding.ChannelID)
	if channelID == "" {
		return "", fmt.Errorf("Discord channel is not configured")
	}

	// Build action row with buttons
	var buttons []map[string]any
	for _, btn := range msg.Buttons {
		style := 2 // Secondary (default)
		switch btn.Style {
		case "primary":
			style = 1 // Primary (blurple)
		case "danger":
			style = 4 // Danger (red)
		}
		buttons = append(buttons, map[string]any{
			"type":      2,
			"style":     style,
			"label":     btn.Label,
			"custom_id": btn.Value,
		})
	}
	if msg.MultiSelect {
		buttons = append(buttons, map[string]any{
			"type":      2,
			"style":     3, // Success (green)
			"label":     "✅ Done",
			"custom_id": "__done__",
		})
	}

	url := a.apiBase + "/channels/" + channelID + "/messages"
	body := map[string]any{
		"content": msg.Text,
		"flags":   discordSuppressEmbeds, // Suppress link preview embeds for cleaner UX
		"components": []map[string]any{
			{
				"type":       1, // ActionRow
				"components": buttons,
			},
		},
	}
	bodyBytes, _ := json.Marshal(body)

	for attempt := 0; attempt <= maxRateLimitRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bot "+a.token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := a.httpClient.Do(req)
		if err != nil {
			return "", err
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			if attempt < maxRateLimitRetries {
				delay := parseRetryAfter(resp)
				debug.Log("discord", "adapter=%s SendInteractive 429 rate limited, retry %d/%d in %v",
					a.name, attempt+1, maxRateLimitRetries, delay)
				if err := sleepRetry(ctx, delay); err != nil {
					return "", err
				}
				continue
			}
			return "", rateLimitExhausted("Discord")
		}

		defer resp.Body.Close()
		var result map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return "", err
		}
		if id, ok := result["id"].(string); ok {
			return id, nil
		}
		return "", nil
	}
	return "", rateLimitExhausted("Discord")
}

// handleInteraction processes Discord INTERACTION_CREATE events (button clicks).
func (a *discordAdapter) handleInteraction(ctx context.Context, d map[string]any) {
	// Acknowledge the interaction (required by Discord)
	interactionID, _ := d["id"].(string)
	interactionToken, _ := d["token"].(string)
	if interactionID == "" || interactionToken == "" {
		return
	}

	// Respond with a deferred update (no visible change to the message)
	safego.Go("im.discord.ackInteraction", func() {
		if a.httpClient == nil {
			return
		}
		url := fmt.Sprintf("%s/interactions/%s/%s/callback", a.apiBase, interactionID, interactionToken)
		body := map[string]any{"type": 6} // UPDATE_MESSAGE (deferred)
		bodyBytes, _ := json.Marshal(body)
		// Use a timeout context instead of context.Background() to prevent
		// the HTTP request from hanging indefinitely if the connection stalls.
		ackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ackCtx, http.MethodPost, url, bytes.NewReader(bodyBytes))
		req.Header.Set("Authorization", "Bot "+a.token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := a.httpClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	})

	// Extract button click data
	data, _ := d["data"].(map[string]any)
	if data == nil {
		return
	}
	customID, _ := data["custom_id"].(string)
	if customID == "" {
		return
	}

	// Extract sender
	member, _ := d["member"].(map[string]any)
	senderID, _ := member["user"].(map[string]any)
	senderIDStr := ""
	senderName := ""
	if senderID != nil {
		senderIDStr, _ = senderID["id"].(string)
		senderName, _ = senderID["username"].(string)
	}

	channelID, _ := d["channel_id"].(string)
	messageID, _ := d["message"].(map[string]any)
	messageIDStr := ""
	if messageID != nil {
		messageIDStr, _ = messageID["id"].(string)
	}

	if a.manager != nil {
		a.manager.HandleInteractiveCallback(InteractiveCallback{
			MessageID: messageIDStr,
			Values:    []string{customID},
			Adapter:   a.name,
			Envelope: Envelope{
				Adapter:    a.name,
				Platform:   PlatformDiscord,
				ChannelID:  channelID,
				SenderID:   senderIDStr,
				SenderName: senderName,
				MessageID:  messageIDStr,
				ReceivedAt: time.Now(),
			},
		})
	}
}
