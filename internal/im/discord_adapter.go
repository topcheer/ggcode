package im

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
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
)

type discordAdapter struct {
	name       string
	manager    *Manager
	httpClient *http.Client
	token      string
	apiBase    string

	mu        sync.RWMutex
	connected bool
	sessionID string
	sequence  int
	ws        *websocket.Conn
}

func newDiscordAdapter(name string, _ config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) (*discordAdapter, error) {
	token := strings.TrimSpace(stringValue(adapterCfg.Extra, "token", "bot_token"))
	if token == "" {
		return nil, fmt.Errorf("Discord bot token is required for adapter %q", name)
	}
	apiBase := strings.TrimSpace(stringValue(adapterCfg.Extra, "api_base"))
	if apiBase == "" {
		apiBase = discordAPIBase
	}
	return &discordAdapter{
		name:       name,
		manager:    mgr,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		token:      token,
		apiBase:    apiBase,
	}, nil
}

func (a *discordAdapter) Name() string { return a.name }

func (a *discordAdapter) Start(ctx context.Context) {
	debug.Log("discord", "adapter=%s start", a.name)
	a.publishState(false, "connecting", "")
	go a.run(ctx)
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
		case <-time.After(delay):
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
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
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
			go a.heartbeatLoop(heartbeatCtx, conn, interval)
			a.sendIdentify(conn)

		case discordOpHeartbeatACK:
			// acknowledged

		case discordOpDispatch:
			t, _ := payload["t"].(string)
			if t == "READY" {
				d, _ := payload["d"].(map[string]any)
				if d != nil {
					sid, _ := d["session_id"].(string)
					a.mu.Lock()
					a.sessionID = sid
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
	_ = conn.WriteMessage(websocket.TextMessage, data)
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
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				debug.Log("discord", "adapter=%s heartbeat write error: %v", a.name, err)
				return
			}
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
	if content == "" {
		return
	}

	debug.Log("discord", "adapter=%s inbound channel=%s sender=%s len=%d", a.name, channelID, senderID, len(content))

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
		Text: content,
	}

	pairingResult, err := a.manager.HandlePairingInbound(inbound)
	if err != nil && err != ErrNoSessionBound {
		a.publishState(false, "warning", err.Error())
	}
	if pairingResult.Consumed {
		if sendErr := a.sendChannelMessage(ctx, channelID, pairingResult.ReplyText); sendErr != nil {
			a.publishState(false, "warning", sendErr.Error())
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
	for _, chunk := range chunks {
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
	body := map[string]any{"content": content}
	bodyBytes, _ := json.Marshal(body)
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
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Discord API [%d] %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// TriggerTyping triggers the "Bot is typing..." indicator in a Discord channel.
func (a *discordAdapter) TriggerTyping(ctx context.Context, binding ChannelBinding) error {
	channelID := strings.TrimSpace(binding.ChannelID)
	if channelID == "" {
		return nil
	}
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
		respBody, _ := io.ReadAll(resp.Body)
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
	data, err := io.ReadAll(resp.Body)
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
	a.manager.PublishAdapterState(AdapterState{
		Name:      a.name,
		Platform:  PlatformDiscord,
		Healthy:   healthy,
		Status:    status,
		LastError: lastErr,
		UpdatedAt: time.Now(),
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
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Discord file upload [%d] %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func splitDiscordMessage(text string, maxLen int) []string {
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
