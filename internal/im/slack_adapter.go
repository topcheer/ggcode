package im

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	slackAPIBase       = "https://slack.com/api"
	slackMaxTextLen    = 4000
	slackInterMsgDelay = 500 * time.Millisecond // Slack allows ~1 msg/sec/channel; 500ms is safe with burst tolerance
)

// Slack mrkdwn link format: [text](url) → <url|text>
// Slack mrkdwn doesn't support standard markdown links.
var slackLinkRe = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

// Slack mrkdwn has no native header support; convert to bold.
var slackHeaderRe = regexp.MustCompile(`(?m)^(#{1,6})\s+(.+)$`)

type slackAdapter struct {
	name       string
	manager    *Manager
	httpClient *http.Client
	botToken   string
	appToken   string
	botUserID  string
	teamID     string
	apiBase    string // override for tests
	stt        imstt.Transcriber

	mu          sync.RWMutex
	connected   bool
	ws          *websocket.Conn
	reactionAck reactionAckState
}

func newSlackAdapter(name string, imCfg config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) (*slackAdapter, error) {
	botToken := strings.TrimSpace(stringValue(adapterCfg.Extra, "bot_token", "token"))
	if botToken == "" {
		return nil, fmt.Errorf("Slack bot_token is required for adapter %q", name)
	}
	appToken := strings.TrimSpace(stringValue(adapterCfg.Extra, "app_token"))
	if appToken == "" {
		return nil, fmt.Errorf("Slack app_token is required for Socket Mode (adapter %q)", name)
	}
	adapter := &slackAdapter{
		name:       name,
		manager:    mgr,
		httpClient: util.NewInsecureAwareClient(30 * time.Second),
		botToken:   botToken,
		appToken:   appToken,
	}
	adapter.stt = buildSTTWithFallback(imCfg.STT, adapterCfg.Extra, resolveSlackSTTConfig)
	return adapter, nil
}

func (a *slackAdapter) Name() string { return a.name }

func (a *slackAdapter) Start(ctx context.Context) {
	debug.Log("slack", "adapter=%s start", a.name)
	a.publishState(false, "connecting", "")
	safego.Go("im.slack.run", func() { a.run(ctx) })
}

func (a *slackAdapter) Close() error {
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
		} else if msgType == "interactive" {
			payload, _ := envelope["payload"].(map[string]any)
			if payload != nil {
				a.handleInteractive(ctx, payload)
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
	data, err := util.ReadAll(resp.Body, util.ReadLimitGeneral)
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
	data, err := util.ReadAll(resp.Body, util.ReadLimitGeneral)
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
	threadTS, _ := event["thread_ts"].(string)
	subtype, _ := event["subtype"].(string)

	// Skip non-text subtypes (except file_share)
	if subtype != "" && subtype != "file_share" {
		return
	}

	// Process file attachments (images, files, audio)
	attachments, voiceText := a.processSlackAttachments(ctx, event)
	if voiceText != "" {
		if text != "" {
			text += "\n\n" + voiceText
		} else {
			text = voiceText
		}
	}

	text = strings.TrimSpace(text)
	if text == "" && len(attachments) == 0 {
		return
	}

	debug.Log("slack", "adapter=%s inbound channel=%s user=%s len=%d attachments=%d", a.name, channel, userID, len(text), len(attachments))

	inbound := InboundMessage{
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformSlack,
			ChannelID:  channel,
			ThreadID:   threadTS,
			SenderID:   userID,
			MessageID:  ts,
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
		if _, sendErr := a.sendChannelMessage(ctx, channel, threadTS, pairingResult.ReplyText); sendErr != nil {
			a.publishState(false, "warning", sendErr.Error())
		}
		if err := a.manager.NotifyPreviousBindingReplaced(ctx, pairingResult); err != nil {
			a.publishState(false, "warning", err.Error())
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

// processSlackAttachments extracts file attachments (images, audio, files) from a Slack message event.
func (a *slackAdapter) processSlackAttachments(ctx context.Context, event map[string]any) ([]Attachment, string) {
	files, ok := event["files"].([]any)
	if !ok || len(files) == 0 {
		return nil, ""
	}
	var attachments []Attachment
	var voiceText string

	for _, f := range files {
		file, ok := f.(map[string]any)
		if !ok {
			continue
		}
		fileID, _ := file["id"].(string)
		mimetype, _ := file["mimetype"].(string)
		name, _ := file["name"].(string)
		// url_private_download is the direct download URL (needs Authorization header)
		downloadURL, _ := file["url_private_download"].(string)
		if downloadURL == "" {
			downloadURL, _ = file["url_private"].(string)
		}
		if downloadURL == "" {
			continue
		}

		if strings.HasPrefix(mimetype, "audio/") {
			// Audio/voice attachment — transcribe if STT is available
			transcript := ""
			if a.stt != nil {
				transcript = a.transcribeSlackAudio(ctx, downloadURL, name, mimetype)
			}
			attachments = append(attachments, Attachment{
				Kind:       AttachmentVoice,
				Name:       name,
				MIME:       mimetype,
				Transcript: transcript,
			})
			if transcript != "" {
				if voiceText != "" {
					voiceText += "\n\n" + transcript
				} else {
					voiceText = transcript
				}
			}
			continue
		}

		data, respMime, err := a.downloadSlackFile(ctx, downloadURL)
		if err != nil {
			debug.Log("slack", "adapter=%s download file %s failed: %v", a.name, fileID, err)
			continue
		}
		if strings.HasPrefix(mimetype, "image/") || strings.HasPrefix(respMime, "image/") {
			if decoded, decodeErr := imagepkg.Decode(data); decodeErr == nil && strings.TrimSpace(decoded.MIME) != "" {
				respMime = decoded.MIME
			}
			attachments = append(attachments, Attachment{
				Kind:       AttachmentImage,
				Name:       name,
				MIME:       firstNonEmpty(mimetype, respMime),
				DataBase64: base64.StdEncoding.EncodeToString(data),
			})
		} else {
			localPath, cacheErr := cacheSlackAttachment(data, name, firstNonEmpty(mimetype, respMime))
			if cacheErr != nil {
				debug.Log("slack", "adapter=%s cache file failed: %v", a.name, cacheErr)
			}
			attachments = append(attachments, Attachment{
				Kind: AttachmentFile,
				Name: name,
				MIME: firstNonEmpty(mimetype, respMime),
				Path: localPath,
			})
		}
	}
	return attachments, voiceText
}

func (a *slackAdapter) downloadSlackFile(ctx context.Context, url string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+a.botToken)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)
		return nil, "", fmt.Errorf("Slack download [%d] %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	data, err := util.ReadAll(resp.Body, util.ReadLimitGeneral)
	if err != nil {
		return nil, "", err
	}
	return data, strings.TrimSpace(resp.Header.Get("Content-Type")), nil
}

func cacheSlackAttachment(data []byte, filename, mimeType string) (string, error) {
	ext := filepath.Ext(strings.TrimSpace(filename))
	if ext == "" {
		ext = slackAttachmentExt(mimeType)
	}
	tmpFile, err := os.CreateTemp("", "ggcode-slack-*"+ext)
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

func slackAttachmentExt(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "application/pdf":
		return ".pdf"
	default:
		return ".bin"
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
		if err := a.sendExtractedImage(ctx, channelID, binding.ThreadID, img); err != nil {
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
	for i, chunk := range chunks {
		msgID, err := a.sendChannelMessage(ctx, channelID, binding.ThreadID, chunk)
		if err != nil {
			return err
		}
		a.recordOutboundMessage(binding, msgID)
		// Inter-message delay to respect Slack's ~1 msg/sec per-channel rate limit.
		// Short bursts >1 are tolerated but sustained bursts risk rate limiting.
		if i < len(chunks)-1 {
			select {
			case <-time.After(slackInterMsgDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
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

// TriggerTyping adds a reaction on the latest real user message to
// indicate the bot is processing. Slack does not have a native typing indicator
// for bots, so we use a reaction as a visual cue.
func (a *slackAdapter) TriggerTyping(ctx context.Context, binding ChannelBinding) error {
	channelID := strings.TrimSpace(binding.ChannelID)
	ts := LastReactionTargetMessageID(binding)
	if channelID == "" || ts == "" || !a.reactionAck.NeedsSend(binding, ts) {
		return nil
	}
	baseURL := slackAPIBase
	if a.apiBase != "" {
		baseURL = a.apiBase
	}
	url := baseURL + "/reactions.add"
	body := map[string]any{
		"channel":   channelID,
		"timestamp": ts,
		"name":      reactionAckValue(PlatformSlack, ts),
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
		debug.Log("slack", "adapter=%s typing reaction failed: %v", a.name, err)
		return err
	}
	defer resp.Body.Close()
	data, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil
	}
	if ok, _ := result["ok"].(bool); !ok {
		errMsg, _ := result["error"].(string)
		// "already_reacted" is not an error — typing indicator already present
		if errMsg != "already_reacted" {
			debug.Log("slack", "adapter=%s typing reaction error: %s", a.name, errMsg)
			return nil
		}
	}
	a.reactionAck.MarkSent(binding, ts)
	return nil
}

func (a *slackAdapter) sendChannelMessage(ctx context.Context, channelID, threadTS, content string) (string, error) {
	baseURL := slackAPIBase
	if a.apiBase != "" {
		baseURL = a.apiBase
	}
	url := baseURL + "/chat.postMessage"
	body := map[string]any{
		"channel": channelID,
		"text":    content,
	}
	if strings.TrimSpace(threadTS) != "" {
		body["thread_ts"] = threadTS
	}
	bodyBytes, _ := json.Marshal(body)

	for attempt := 0; attempt <= maxRateLimitRetries; attempt++ {
		if attempt > 0 {
			debug.Log("slack", "adapter=%s send retry %d/%d", a.name, attempt, maxRateLimitRetries)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+a.botToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := a.httpClient.Do(req)
		if err != nil {
			return "", err
		}

		// Handle HTTP 429 rate limit with Retry-After backoff.
		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			if attempt < maxRateLimitRetries {
				delay := parseRetryAfter(resp)
				debug.Log("slack", "adapter=%s rate limited (429), retrying in %v (attempt %d/%d)",
					a.name, delay, attempt+1, maxRateLimitRetries)
				if err := sleepRetry(ctx, delay); err != nil {
					return "", err
				}
				continue
			}
			return "", rateLimitExhausted("Slack")
		}

		data, err := util.ReadAll(resp.Body, util.ReadLimitGeneral)
		resp.Body.Close()
		if err != nil {
			return "", err
		}

		var result map[string]any
		if err := json.Unmarshal(data, &result); err != nil {
			return "", fmt.Errorf("Slack API parse error: %w", err)
		}
		if ok, _ := result["ok"].(bool); !ok {
			errMsg, _ := result["error"].(string)
			// Slack may return ok=false with error="ratelimited" inside a 200 body.
			if errMsg == "ratelimited" && attempt < maxRateLimitRetries {
				debug.Log("slack", "adapter=%s Slack ratelimited error, retrying (attempt %d/%d)",
					a.name, attempt+1, maxRateLimitRetries)
				if err := sleepRetry(ctx, defaultRetryDelay); err != nil {
					return "", err
				}
				continue
			}
			return "", fmt.Errorf("Slack API error: %s", errMsg)
		}
		ts, _ := result["ts"].(string)
		return strings.TrimSpace(ts), nil
	}
	return "", rateLimitExhausted("Slack")
}

func (a *slackAdapter) recordOutboundMessage(binding ChannelBinding, messageID string) {
	if a.manager == nil {
		return
	}
	if err := a.manager.RecordOutboundMessage(binding.Workspace, binding.Adapter, messageID); err != nil {
		debug.Log("slack", "adapter=%s record outbound failed: %v", a.name, err)
	}
}

func (a *slackAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	contactURI := ""
	if a.botUserID != "" {
		contactURI = "https://slack.com/app_redirect?channel=" + a.botUserID
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:       a.name,
		Platform:   PlatformSlack,
		Healthy:    healthy,
		Status:     status,
		LastError:  lastErr,
		ContactURI: contactURI,
		UpdatedAt:  time.Now(),
	})
}

// sendExtractedImage resolves and sends an extracted image to a Slack channel.
func (a *slackAdapter) sendExtractedImage(ctx context.Context, channelID, threadTS string, img ExtractedImage) error {
	switch img.Kind {
	case "url":
		if IsLocalFilePath(img.Data) {
			data, err := os.ReadFile(img.Data)
			if err != nil {
				return fmt.Errorf("read local image: %w", err)
			}
			return a.uploadFile(ctx, channelID, threadTS, filepath.Base(img.Data), data, "")
		}
		// For remote URLs, download first then upload (with context for cancellation)
		dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, img.Data, nil)
		if err != nil {
			_, err = a.sendChannelMessage(ctx, channelID, threadTS, img.Data)
			return err
		}
		resp, err := a.httpClient.Do(dlReq)
		if err != nil {
			// Fallback: send URL as text
			_, err = a.sendChannelMessage(ctx, channelID, threadTS, img.Data)
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			_, err = a.sendChannelMessage(ctx, channelID, threadTS, img.Data)
			return err
		}
		data, err := util.ReadAll(resp.Body, util.ReadLimitGeneral)
		if err != nil {
			_, err = a.sendChannelMessage(ctx, channelID, threadTS, img.Data)
			return err
		}
		filename := filepath.Base(img.Data)
		if filename == "" || filename == "." {
			filename = "image.png"
		}
		return a.uploadFile(ctx, channelID, threadTS, filename, data, "")
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
		return a.uploadFile(ctx, channelID, threadTS, "image"+ext, data, "")
	default:
		return fmt.Errorf("unknown image kind: %s", img.Kind)
	}
}

// uploadFile uploads a file to a Slack channel via multipart/form-data.
func (a *slackAdapter) uploadFile(ctx context.Context, channelID, threadTS, filename string, data []byte, comment string) error {
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
	if strings.TrimSpace(threadTS) != "" {
		if err := writer.WriteField("thread_ts", threadTS); err != nil {
			return fmt.Errorf("write thread_ts: %w", err)
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
	respData, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)

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
// Slack mrkdwn reference: https://docs.slack.dev/messaging/formatting-message-text/
// Bold: *text* (single asterisk), Italic: _text_ (underscore), Strike: ~text~ (single tilde)
func markdownToMrkdwn(text string) string {
	// Convert GFM tables to plain text (Slack mrkdwn doesn't support tables)
	text = mdTableRe.ReplaceAllStringFunc(text, func(match string) string {
		lines := strings.Split(match, "\n")
		var result []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if isTableSeparator(trimmed) {
				continue
			}
			core := strings.Trim(trimmed, "|")
			cells := strings.Split(core, "|")
			for i := range cells {
				cells[i] = strings.TrimSpace(cells[i])
			}
			result = append(result, strings.Join(cells, "  "))
		}
		return strings.Join(result, "\n")
	})
	// Escape HTML entities
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	// Convert **bold** to *bold* using a placeholder to avoid collision
	// with the *italic* → _italic_ conversion below.
	// Without the placeholder, step 1 would produce *bold* and step 2 would
	// then convert that *bold* to _bold_ (wrong — bold would render as italic).
	const boldPlaceholder = "\x00B\x00"
	text = replaceDelimiters(text, "**", boldPlaceholder)
	// Convert remaining *italic* to _italic_
	text = replaceDelimiters(text, "*", "_")
	// Restore bold placeholders as Slack mrkdwn *bold*
	text = strings.ReplaceAll(text, boldPlaceholder, "*")
	// Convert ~~strikethrough~~ to ~strikethrough~
	text = replaceDelimiters(text, "~~", "~")
	// Convert markdown links [text](url) to Slack mrkdwn <url|text>
	text = slackLinkRe.ReplaceAllString(text, "<$2|$1>")
	// Convert markdown headers (# H1, ## H2, etc.) to Slack bold (*text*)
	// Slack mrkdwn has no native header support.
	text = slackHeaderRe.ReplaceAllString(text, "*$2*")
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
	return splitMessageRunes(text, maxLen, true, false, false)
}

// transcribeSlackAudio downloads an audio file from Slack and transcribes it via STT.
func (a *slackAdapter) transcribeSlackAudio(ctx context.Context, downloadURL, filename, contentType string) string {
	data, _, err := a.downloadSlackFile(ctx, downloadURL)
	if err != nil || len(data) == 0 {
		debug.Log("slack", "adapter=%s download audio failed: %v", a.name, err)
		return ""
	}

	// Determine extension
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = audioExtFromMIME(contentType)
	}

	// Write to temp file
	src, err := os.CreateTemp("", "ggcode-slack-audio-*"+ext)
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
		dst, err := os.CreateTemp("", "ggcode-slack-audio-*.wav")
		if err != nil {
			cleanup()
			return ""
		}
		dst.Close()
		cmd := exec.Command("ffmpeg", "-y", "-i", audioPath, dst.Name())
		if _, err := cmd.CombinedOutput(); err != nil {
			_ = os.Remove(dst.Name())
			cleanup()
			debug.Log("slack", "adapter=%s ffmpeg convert failed: %v", a.name, err)
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
		debug.Log("slack", "adapter=%s STT failed: %v", a.name, err)
		return ""
	}
	debug.Log("slack", "adapter=%s STT result: %d chars", a.name, len(result.Text))
	return result.Text
}

func resolveSlackSTTConfig(global config.IMSTTConfig, extra map[string]interface{}) *config.IMSTTConfig {
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

// SendInteractive implements InteractiveSender using Slack Block Kit buttons.
func (a *slackAdapter) SendInteractive(ctx context.Context, binding ChannelBinding, msg InteractiveMessage) (string, error) {
	a.mu.RLock()
	connected := a.connected
	a.mu.RUnlock()
	if !connected {
		return "", fmt.Errorf("Slack bot %q is not online", a.name)
	}

	channelID := strings.TrimSpace(binding.ChannelID)
	if channelID == "" {
		return "", fmt.Errorf("Slack channel is not configured")
	}

	// Build Block Kit message
	textBlock := map[string]any{
		"type": "section",
		"text": map[string]any{
			"type": "mrkdwn",
			"text": markdownToMrkdwn(msg.Text),
		},
	}

	// Build action buttons
	var elements []map[string]any
	for _, btn := range msg.Buttons {
		style := ""
		switch btn.Style {
		case "primary":
			style = "primary"
		case "danger":
			style = "danger"
		}
		elements = append(elements, map[string]any{
			"type":  "button",
			"text":  map[string]any{"type": "plain_text", "text": btn.Label},
			"value": btn.Value,
			"style": style,
		})
	}
	if msg.MultiSelect {
		elements = append(elements, map[string]any{
			"type":  "button",
			"text":  map[string]any{"type": "plain_text", "text": "✅ Done"},
			"value": "__done__",
			"style": "primary",
		})
	}

	actionsBlock := map[string]any{
		"type":     "actions",
		"elements": elements,
	}

	blocks := []map[string]any{textBlock, actionsBlock}
	url := slackAPIBase + "/chat.postMessage"
	body := map[string]any{
		"channel": channelID,
		"blocks":  blocks,
	}
	if strings.TrimSpace(binding.ThreadID) != "" {
		body["thread_ts"] = binding.ThreadID
	}
	bodyBytes, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+a.botToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if ok, _ := result["ok"].(bool); !ok {
		errMsg, _ := result["error"].(string)
		return "", fmt.Errorf("Slack chat.postMessage: %s", errMsg)
	}
	// Extract ts (message ID)
	messageTs, _ := result["ts"].(string)
	return messageTs, nil
}

// handleInteractive processes Slack interactive payloads (button clicks).
func (a *slackAdapter) handleInteractive(ctx context.Context, payload map[string]any) {
	actionType, _ := payload["type"].(string)
	if actionType != "block_actions" {
		return
	}

	actions, _ := payload["actions"].([]any)
	if len(actions) == 0 {
		return
	}

	var values []string
	for _, act := range actions {
		action, _ := act.(map[string]any)
		if action == nil {
			continue
		}
		val, _ := action["value"].(string)
		if val != "" {
			values = append(values, val)
		}
	}
	if len(values) == 0 {
		return
	}

	// Extract sender info
	user, _ := payload["user"].(map[string]any)
	senderID, _ := user["id"].(string)
	senderName, _ := user["username"].(string)

	channel, _ := payload["channel"].(map[string]any)
	channelID, _ := channel["id"].(string)

	message, _ := payload["message"].(map[string]any)
	messageTs, _ := message["ts"].(string)

	if a.manager != nil {
		a.manager.HandleInteractiveCallback(InteractiveCallback{
			MessageID: messageTs,
			Values:    values,
			Adapter:   a.name,
			Envelope: Envelope{
				Adapter:    a.name,
				Platform:   PlatformSlack,
				ChannelID:  channelID,
				SenderID:   senderID,
				SenderName: senderName,
				MessageID:  messageTs,
				ReceivedAt: time.Now(),
			},
		})
	}
}
