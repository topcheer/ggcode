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
	slackAPIBase    = "https://slack.com/api"
	slackMaxTextLen = 4000
)

type slackAdapter struct {
	name       string
	manager    *Manager
	httpClient *http.Client
	botToken   string
	appToken   string
	botUserID  string

	mu        sync.RWMutex
	connected bool
	ws        *websocket.Conn
}

func newSlackAdapter(name string, _ config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) (*slackAdapter, error) {
	botToken := strings.TrimSpace(stringValue(adapterCfg.Extra, "bot_token", "token"))
	if botToken == "" {
		return nil, fmt.Errorf("Slack bot_token is required for adapter %q", name)
	}
	appToken := strings.TrimSpace(stringValue(adapterCfg.Extra, "app_token"))
	if appToken == "" {
		return nil, fmt.Errorf("Slack app_token is required for Socket Mode (adapter %q)", name)
	}
	return &slackAdapter{
		name:       name,
		manager:    mgr,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		botToken:   botToken,
		appToken:   appToken,
	}, nil
}

func (a *slackAdapter) Name() string { return a.name }

func (a *slackAdapter) Start(ctx context.Context) {
	debug.Log("slack", "adapter=%s start", a.name)
	a.publishState(false, "connecting", "")
	go a.run(ctx)
}

func (a *slackAdapter) run(ctx context.Context) {
	backoffs := []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second}
	attempt := 0
	for {
		if ctx.Err() != nil {
			a.publishState(false, "stopped", "")
			return
		}
		if err := a.connectAndServe(ctx); err != nil {
			a.publishState(false, "error", err.Error())
			debug.Log("slack", "adapter=%s error: %v", a.name, err)
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

func (a *slackAdapter) connectAndServe(ctx context.Context) error {
	// Verify auth
	if err := a.authTest(ctx); err != nil {
		return fmt.Errorf("auth.test: %w", err)
	}

	// Open Socket Mode connection
	wsURL, err := a.appsConnectionsOpen(ctx)
	if err != nil {
		return fmt.Errorf("apps.connections.open: %w", err)
	}
	debug.Log("slack", "adapter=%s socket mode URL obtained", a.name)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial socket mode: %w", err)
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
	debug.Log("slack", "adapter=%s connected", a.name)

	// Message read loop
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
		var envelope map[string]any
		if err := json.Unmarshal(msgBytes, &envelope); err != nil {
			continue
		}

		msgType, _ := envelope["type"].(string)
		envelopeID, _ := envelope["envelope_id"].(string)
		acceptsResp := envelope["accepts_response_payload"] == true

		// Acknowledge the envelope
		if envelopeID != "" && acceptsResp {
			ack := map[string]any{
				"envelope_id": envelopeID,
			}
			ackData, _ := json.Marshal(ack)
			_ = conn.WriteMessage(websocket.TextMessage, ackData)
		}

		if msgType == "events_api" {
			payload, _ := envelope["payload"].(map[string]any)
			if payload != nil {
				event, _ := payload["event"].(map[string]any)
				if event != nil {
					eventType, _ := event["type"].(string)
					if eventType == "message" {
						a.handleMessage(ctx, event)
					} else if eventType == "app_mention" {
						a.handleMessage(ctx, event)
					}
				}
			}
		}
	}
}

func (a *slackAdapter) authTest(ctx context.Context) error {
	url := slackAPIBase + "/auth.test"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.botToken)
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
	if ok, _ := result["ok"].(bool); !ok {
		errMsg, _ := result["error"].(string)
		return fmt.Errorf("Slack auth failed: %s", errMsg)
	}
	a.mu.Lock()
	a.botUserID, _ = result["user_id"].(string)
	a.mu.Unlock()
	debug.Log("slack", "adapter=%s auth OK botUserID=%s", a.name, a.botUserID)
	return nil
}

func (a *slackAdapter) appsConnectionsOpen(ctx context.Context) (string, error) {
	url := slackAPIBase + "/apps.connections.open"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(""))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+a.appToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
	if ok, _ := result["ok"].(bool); !ok {
		errMsg, _ := result["error"].(string)
		return "", fmt.Errorf("Slack apps.connections.open failed: %s", errMsg)
	}
	wsURL, _ := result["url"].(string)
	if wsURL == "" {
		return "", fmt.Errorf("Slack socket mode URL is empty")
	}
	return wsURL, nil
}

func (a *slackAdapter) handleMessage(ctx context.Context, event map[string]any) {
	// Skip bot's own messages
	userID, _ := event["user"].(string)
	a.mu.RLock()
	botID := a.botUserID
	a.mu.RUnlock()
	if userID == botID {
		return
	}

	channel, _ := event["channel"].(string)
	text, _ := event["text"].(string)
	ts, _ := event["ts"].(string)
	subtype, _ := event["subtype"].(string)

	// Skip non-text subtypes (except file_share)
	if subtype != "" && subtype != "file_share" {
		return
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	debug.Log("slack", "adapter=%s inbound channel=%s user=%s len=%d", a.name, channel, userID, len(text))

	inbound := InboundMessage{
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformSlack,
			ChannelID:  channel,
			SenderID:   userID,
			MessageID:  ts,
			ReceivedAt: time.Now(),
		},
		Text: text,
	}

	pairingResult, err := a.manager.HandlePairingInbound(inbound)
	if err != nil && err != ErrNoSessionBound {
		a.publishState(false, "warning", err.Error())
	}
	if pairingResult.Consumed {
		if sendErr := a.sendChannelMessage(ctx, channel, pairingResult.ReplyText); sendErr != nil {
			a.publishState(false, "warning", sendErr.Error())
		}
		return
	}

	if err := a.manager.HandleInbound(ctx, inbound); err != nil {
		if err == ErrInboundChannelDenied {
			debug.Log("slack", "adapter=%s unauthorized inbound channel=%s", a.name, channel)
			return
		}
		if err != ErrNoChannelBound {
			a.publishState(false, "warning", err.Error())
		}
	}
}

func (a *slackAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	a.mu.RLock()
	connected := a.connected
	a.mu.RUnlock()
	if !connected {
		return fmt.Errorf("Slack bot %q is not online", a.name)
	}
	channelID := strings.TrimSpace(binding.ChannelID)
	if channelID == "" {
		return fmt.Errorf("Slack channel is not configured for current directory")
	}
	content := strings.TrimSpace(a.outboundText(event))
	if content == "" {
		return nil
	}

	// Extract images from text and upload them first
	images, remainingText := ExtractImagesFromText(content)
	for _, img := range images {
		if err := a.sendExtractedImage(ctx, channelID, img); err != nil {
			debug.Log("slack", "adapter=%s image upload failed: %v", a.name, err)
		}
	}

	// Send remaining text (converted to mrkdwn)
	remainingText = strings.TrimSpace(remainingText)
	if remainingText == "" {
		return nil
	}
	remainingText = markdownToMrkdwn(remainingText)
	chunks := splitSlackMessage(remainingText, slackMaxTextLen)
	for _, chunk := range chunks {
		if err := a.sendChannelMessage(ctx, channelID, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (a *slackAdapter) outboundText(event OutboundEvent) string {
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

func (a *slackAdapter) sendChannelMessage(ctx context.Context, channelID, content string) error {
	url := slackAPIBase + "/chat.postMessage"
	body := map[string]any{
		"channel": channelID,
		"text":    content,
	}
	bodyBytes, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.botToken)
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
		return fmt.Errorf("Slack API parse error: %w", err)
	}
	if ok, _ := result["ok"].(bool); !ok {
		errMsg, _ := result["error"].(string)
		return fmt.Errorf("Slack API error: %s", errMsg)
	}
	return nil
}

func (a *slackAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:      a.name,
		Platform:  PlatformSlack,
		Healthy:   healthy,
		Status:    status,
		LastError: lastErr,
		UpdatedAt: time.Now(),
	})
}

// sendExtractedImage resolves and sends an extracted image to a Slack channel.
func (a *slackAdapter) sendExtractedImage(ctx context.Context, channelID string, img ExtractedImage) error {
	switch img.Kind {
	case "url":
		if IsLocalFilePath(img.Data) {
			data, err := os.ReadFile(img.Data)
			if err != nil {
				return fmt.Errorf("read local image: %w", err)
			}
			return a.uploadFile(ctx, channelID, filepath.Base(img.Data), data, "")
		}
		// For remote URLs, download first then upload
		resp, err := a.httpClient.Get(img.Data)
		if err != nil {
			// Fallback: send URL as text
			return a.sendChannelMessage(ctx, channelID, img.Data)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return a.sendChannelMessage(ctx, channelID, img.Data)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return a.sendChannelMessage(ctx, channelID, img.Data)
		}
		filename := filepath.Base(img.Data)
		if filename == "" || filename == "." {
			filename = "image.png"
		}
		return a.uploadFile(ctx, channelID, filename, data, "")
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
		return a.uploadFile(ctx, channelID, "image"+ext, data, "")
	default:
		return fmt.Errorf("unknown image kind: %s", img.Kind)
	}
}

// uploadFile uploads a file to a Slack channel via multipart/form-data.
func (a *slackAdapter) uploadFile(ctx context.Context, channelID, filename string, data []byte, comment string) error {
	url := slackAPIBase + "/files.upload"

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	if err := writer.WriteField("channels", channelID); err != nil {
		return fmt.Errorf("write channels: %w", err)
	}
	if comment != "" {
		if err := writer.WriteField("initial_comment", comment); err != nil {
			return fmt.Errorf("write initial_comment: %w", err)
		}
	}

	part, err := writer.CreateFormFile("file", filename)
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
	req.Header.Set("Authorization", "Bearer "+a.botToken)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respData, _ := io.ReadAll(resp.Body)

	var result map[string]any
	if err := json.Unmarshal(respData, &result); err != nil {
		return fmt.Errorf("Slack files.upload parse error: %w", err)
	}
	if ok, _ := result["ok"].(bool); !ok {
		errMsg, _ := result["error"].(string)
		return fmt.Errorf("Slack files.upload error: %s", errMsg)
	}
	return nil
}

// markdownToMrkdwn converts basic Markdown to Slack mrkdwn format.
func markdownToMrkdwn(text string) string {
	// Escape HTML entities
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	// Convert **bold** to *bold*
	text = replaceDelimiters(text, "**", "*")
	// Convert *italic* to _italic_ (only single *)
	text = replaceDelimiters(text, "*", "_")
	// Convert ~~strikethrough~~ to ~strikethrough~
	text = replaceDelimiters(text, "~~", "~")
	return text
}

func replaceDelimiters(text, from, to string) string {
	parts := strings.Split(text, from)
	if len(parts) <= 1 {
		return text
	}
	var buf strings.Builder
	for i, part := range parts {
		buf.WriteString(part)
		if i < len(parts)-1 {
			buf.WriteString(to)
		}
	}
	return buf.String()
}

func splitSlackMessage(text string, maxLen int) []string {
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
