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
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tgmd "github.com/eekstunt/telegramify-markdown-go"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	imstt "github.com/topcheer/ggcode/internal/im/stt"
	imagepkg "github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	tgDefaultAPIBase = "https://api.telegram.org"
	tgPollTimeout    = 30
	tgMaxTextLen     = 4096
	// tgInterMessageDelay is the delay between consecutive messages to the same
	// chat. Telegram recommends avoiding more than 1 message/second per chat.
	// Source: https://core.telegram.org/bots/faq#my-bot-is-hitting-limits-how-do-i-avoid-this
	// Quote: "In a single chat, avoid sending more than one message per second."
	tgInterMessageDelay = 1000 * time.Millisecond
	tgGetUpdatesPath    = "/bot%s/getUpdates"
	tgSendMessagePath   = "/bot%s/sendMessage"
	tgSetReactionPath   = "/bot%s/setMessageReaction"
	tgSendPhotoPath     = "/bot%s/sendPhoto"
	tgGetMePath         = "/bot%s/getMe"
	tgGetFileBase       = "https://api.telegram.org/file/bot%s/%s"
	tgGetFilePath       = "/bot%s/getFile"
)

type tgAdapter struct {
	name        string
	manager     *Manager
	httpClient  *http.Client
	botToken    string
	apiBase     string
	botUsername string
	parseMode   string
	stt         imstt.Transcriber

	mu           sync.RWMutex
	lastUpdateID int
	connected    bool
	seen         map[int]time.Time
	reactionAck  reactionAckState
}

func newTGAdapter(name string, imCfg config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) (*tgAdapter, error) {
	botToken := strings.TrimSpace(stringValue(adapterCfg.Extra, "bot_token", "bottoken", "token"))
	if botToken == "" {
		return nil, fmt.Errorf("Telegram bot_token is required for adapter %q", name)
	}
	apiBase := strings.TrimSpace(stringValue(adapterCfg.Extra, "api_root"))
	if apiBase == "" {
		apiBase = tgDefaultAPIBase
	}
	parseMode := strings.TrimSpace(stringValue(adapterCfg.Extra, "parse_mode"))
	switch strings.ToLower(parseMode) {
	case "markdown", "markdownv2":
		parseMode = "MarkdownV2"
	case "html":
		parseMode = "HTML"
	case "none", "plain", "text":
		parseMode = ""
	default:
		parseMode = "" // default: use entities (tgmd.Convert)
	}
	adapter := &tgAdapter{
		name:       name,
		manager:    mgr,
		httpClient: util.NewInsecureAwareClient(60 * time.Second),
		botToken:   botToken,
		apiBase:    apiBase,
		parseMode:  parseMode,
		seen:       make(map[int]time.Time),
	}
	adapter.stt = buildSTTWithFallback(imCfg.STT, adapterCfg.Extra, resolveTGSTTConfig)
	return adapter, nil
}

func (a *tgAdapter) Name() string { return a.name }

func (a *tgAdapter) Start(ctx context.Context) {
	debug.Log("tg", "adapter=%s start", a.name)
	a.publishState(false, "connecting", "")
	safego.Go("im.tg.run", func() { a.run(ctx) })
}

// Close signals the polling loop to stop immediately.
func (a *tgAdapter) Close() error {
	a.mu.Lock()
	if a.httpClient != nil {
		a.httpClient.CloseIdleConnections()
	}
	a.connected = false
	a.mu.Unlock()
	return nil
}

func (a *tgAdapter) run(ctx context.Context) {
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

func (a *tgAdapter) connectAndServe(ctx context.Context) error {
	debug.Log("tg", "adapter=%s fetching bot info", a.name)
	me, err := a.getMe(ctx)
	if err != nil {
		return fmt.Errorf("getMe: %w", err)
	}
	// Telegram getMe returns {"ok":true,"result":{"id":...,"username":"botname"}}
	// apiRequest stores the full payload, so extract result.username.
	if result, _ := me["result"].(map[string]any); result != nil {
		if username, ok := result["username"].(string); ok {
			a.mu.Lock()
			a.botUsername = username
			a.mu.Unlock()
			debug.Log("tg", "adapter=%s bot username=%s", a.name, username)
		}
	}
	a.mu.Lock()
	a.connected = true
	a.mu.Unlock()
	a.publishState(true, "connected", "")

	for {
		if ctx.Err() != nil {
			return nil
		}
		updates, err := a.pollUpdates(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("poll updates: %w", err)
		}
		for _, update := range updates {
			a.handleUpdate(ctx, update)
		}
	}
}

func (a *tgAdapter) getMe(ctx context.Context) (map[string]any, error) {
	var result map[string]any
	path := fmt.Sprintf(tgGetMePath, a.botToken)
	_, err := a.apiRequest(ctx, http.MethodGet, path, nil, &result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (a *tgAdapter) pollUpdates(ctx context.Context) ([]map[string]any, error) {
	a.mu.RLock()
	offset := a.lastUpdateID + 1
	a.mu.RUnlock()
	path := fmt.Sprintf(tgGetUpdatesPath, a.botToken)
	body := map[string]any{
		"offset":          offset,
		"timeout":         tgPollTimeout,
		"allowed_updates": []string{"message", "callback_query"},
	}
	var result map[string]any
	_, err := a.apiRequest(ctx, http.MethodPost, path, body, &result)
	if err != nil {
		return nil, err
	}
	raw, ok := result["result"].([]any)
	if !ok {
		return nil, nil
	}
	updates := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		update, ok := item.(map[string]any)
		if !ok {
			continue
		}
		updateID, _ := intValue(update["update_id"])
		if updateID > 0 {
			a.mu.Lock()
			if updateID > a.lastUpdateID {
				a.lastUpdateID = updateID
			}
			a.mu.Unlock()
		}
		updates = append(updates, update)
	}
	return updates, nil
}

func (a *tgAdapter) handleUpdate(ctx context.Context, update map[string]any) {
	// Handle callback queries (button clicks) first
	if cb, ok := update["callback_query"].(map[string]any); ok {
		a.handleCallbackQuery(ctx, cb)
		return
	}

	msg, ok := update["message"].(map[string]any)
	if !ok {
		return
	}
	updateID, _ := intValue(update["update_id"])
	if a.seenUpdate(updateID) {
		return
	}
	msgID := jsonInt64Str(msg["message_id"])
	chat, _ := msg["chat"].(map[string]any)
	if chat == nil {
		return
	}
	chatID := jsonInt64Str(chat["id"])
	chatType, _ := chat["type"].(string)
	from, _ := msg["from"].(map[string]any)
	senderID := ""
	senderName := ""
	if from != nil {
		senderID = jsonInt64Str(from["id"])
		firstName, _ := from["first_name"].(string)
		lastName, _ := from["last_name"].(string)
		senderName = strings.TrimSpace(firstName + " " + lastName)
		username, _ := from["username"].(string)
		if senderName == "" && username != "" {
			senderName = username
		}
	}

	text := strings.TrimSpace(stringFromAny(msg["text"]))
	if chatType == "group" || chatType == "supergroup" {
		a.mu.RLock()
		botUN := a.botUsername
		a.mu.RUnlock()
		if botUN != "" && strings.Contains(text, "@"+botUN) {
			text = strings.TrimSpace(strings.ReplaceAll(text, "@"+botUN, ""))
		}
	}

	attachments, voiceText := a.processAttachments(ctx, msg)
	if voiceText != "" {
		if text != "" {
			text += "\n\n" + voiceText
		} else {
			text = voiceText
		}
	}

	inbound := InboundMessage{
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformTelegram,
			ChannelID:  chatID,
			SenderID:   senderID,
			SenderName: senderName,
			MessageID:  msgID,
			ReceivedAt: time.Now(),
		},
		Text:        text,
		Attachments: attachments,
	}

	debug.Log("tg", "adapter=%s inbound chat=%s type=%s sender=%s len=%d", a.name, chatID, chatType, senderID, len(text))

	pairingResult, err := a.manager.HandlePairingInbound(inbound)
	if err != nil && err != ErrNoSessionBound {
		a.publishState(false, "warning", err.Error())
	}
	if pairingResult.Consumed {
		if sendErr := a.sendReplyText(ctx, chatID, msgID, pairingResult.ReplyText); sendErr != nil {
			a.publishState(false, "warning", sendErr.Error())
		}
		if err := a.manager.NotifyPreviousBindingReplaced(ctx, pairingResult); err != nil {
			a.publishState(false, "warning", err.Error())
		}
		return
	}

	if err := a.manager.HandleInbound(ctx, inbound); err != nil {
		if err == ErrInboundChannelDenied {
			debug.Log("tg", "adapter=%s unauthorized inbound chat=%s", a.name, chatID)
			_ = a.sendUnauthorized(ctx, chatID, msgID)
			return
		}
		if err != ErrNoChannelBound {
			a.publishState(false, "warning", err.Error())
		}
	}
}

func (a *tgAdapter) processAttachments(ctx context.Context, msg map[string]any) ([]Attachment, string) {
	var attachments []Attachment
	var voiceText string

	// Photo attachments (Telegram sends multiple sizes, use the largest)
	if photos, ok := msg["photo"].([]any); ok && len(photos) > 0 {
		photo, ok := photos[len(photos)-1].(map[string]any)
		if ok {
			fileID, _ := photo["file_id"].(string)
			if fileID != "" {
				data, mimeType, err := a.downloadTGFile(ctx, fileID)
				if err == nil && len(data) > 0 {
					if decoded, decodeErr := imagepkg.Decode(data); decodeErr == nil && strings.TrimSpace(decoded.MIME) != "" {
						mimeType = decoded.MIME
					}
					localPath, cacheErr := cacheTGAttachment(data, "photo.jpg", mimeType)
					if cacheErr == nil {
						attachments = append(attachments, Attachment{
							Kind:       AttachmentImage,
							Name:       "photo.jpg",
							MIME:       mimeType,
							Path:       localPath,
							DataBase64: base64.StdEncoding.EncodeToString(data),
						})
					}
				}
			}
		}
	}

	// Voice / audio
	if voice, ok := msg["voice"].(map[string]any); ok {
		fileID, _ := voice["file_id"].(string)
		if fileID != "" {
			transcript := ""
			if a.stt != nil {
				transcript = a.transcribeTGVoice(ctx, fileID)
			}
			if transcript != "" {
				attachments = append(attachments, Attachment{
					Kind:       AttachmentVoice,
					Name:       "voice.ogg",
					MIME:       "audio/ogg",
					Transcript: transcript,
				})
				voiceText = transcript
			}
		}
	}

	// Document / file
	if doc, ok := msg["document"].(map[string]any); ok {
		fileID, _ := doc["file_id"].(string)
		filename, _ := doc["file_name"].(string)
		mimeType, _ := doc["mime_type"].(string)
		if fileID != "" {
			data, respMime, err := a.downloadTGFile(ctx, fileID)
			if err == nil && len(data) > 0 {
				localPath, cacheErr := cacheTGAttachment(data, filename, firstNonEmpty(mimeType, respMime))
				if cacheErr == nil {
					attachments = append(attachments, Attachment{
						Kind: AttachmentFile,
						Name: filename,
						MIME: firstNonEmpty(mimeType, respMime),
						Path: localPath,
					})
				}
			}
		}
	}

	return attachments, voiceText
}

func (a *tgAdapter) transcribeTGVoice(ctx context.Context, fileID string) string {
	data, mimeType, err := a.downloadTGFile(ctx, fileID)
	if err != nil || len(data) == 0 {
		return ""
	}
	ext := ".ogg"
	if strings.Contains(mimeType, "wav") {
		ext = ".wav"
	}
	src, err := os.CreateTemp("", "ggcode-tg-audio-*"+ext)
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
		dst, err := os.CreateTemp("", "ggcode-tg-audio-*.wav")
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
		return ""
	}
	return strings.TrimSpace(result.Text)
}

func (a *tgAdapter) downloadTGFile(ctx context.Context, fileID string) ([]byte, string, error) {
	// Get file path first
	path := fmt.Sprintf(tgGetFilePath, a.botToken)
	body := map[string]any{"file_id": fileID}
	var result map[string]any
	_, err := a.apiRequest(ctx, http.MethodPost, path, body, &result)
	if err != nil {
		return nil, "", err
	}
	filePath, _ := result["file_path"].(string)
	if filePath == "" {
		return nil, "", fmt.Errorf("Telegram file_path is empty for file_id %s", fileID)
	}
	downloadURL := fmt.Sprintf(tgGetFileBase, a.botToken, filePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("download Telegram file [%d]", resp.StatusCode)
	}
	data, err := util.ReadAll(resp.Body, util.ReadLimitGeneral)
	if err != nil {
		return nil, "", err
	}
	return data, strings.TrimSpace(resp.Header.Get("Content-Type")), nil
}

func cacheTGAttachment(data []byte, filename, mimeType string) (string, error) {
	ext := filepath.Ext(strings.TrimSpace(filename))
	if ext == "" {
		ext = tgAttachmentExt(mimeType)
	}
	tmpFile, err := os.CreateTemp("", "ggcode-tg-*"+ext)
	if err != nil {
		return "", fmt.Errorf("cache TG attachment: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return "", fmt.Errorf("write TG attachment: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpFile.Name())
		return "", fmt.Errorf("close TG attachment: %w", err)
	}
	return tmpFile.Name(), nil
}

func tgAttachmentExt(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav":
		return ".wav"
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

func (a *tgAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	a.mu.RLock()
	connected := a.connected
	a.mu.RUnlock()
	if !connected {
		return fmt.Errorf("Telegram bot %q is not online", a.name)
	}
	channelID := strings.TrimSpace(binding.ChannelID)
	if channelID == "" {
		return fmt.Errorf("Telegram channel is not configured for current directory")
	}
	content := strings.TrimSpace(a.outboundText(event))
	if content == "" {
		return nil
	}
	debug.Log("tg", "adapter=%s outbound kind=%s channel=%s len=%d", a.name, event.Kind, channelID, len(content))

	// Extract images from text and send them as photos
	images, remainingText := ExtractImagesFromText(content)
	for i, img := range images {
		if err := a.sendExtractedImage(ctx, channelID, img, ""); err != nil {
			debug.Log("tg", "adapter=%s image send failed [%d/%d]: %v", a.name, i+1, len(images), err)
		}
	}

	// Send remaining text
	remainingText = strings.TrimSpace(remainingText)
	if remainingText != "" {
		messages, err := a.formatMessages(remainingText)
		if err != nil {
			return err
		}
		for i, msg := range messages {
			// Rate limit: avoid >1 msg/sec per chat (official Telegram limit).
			// Source: https://core.telegram.org/bots/faq#my-bot-is-hitting-limits-how-do-i-avoid-this
			if i > 0 {
				select {
				case <-time.After(tgInterMessageDelay):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			if err := a.sendTextMessage(ctx, channelID, msg, ""); err != nil {
				return err
			}
		}
	}
	debug.Log("tg", "adapter=%s outbound delivered kind=%s channel=%s", a.name, event.Kind, channelID)
	return nil
}

func (a *tgAdapter) sendUnauthorized(ctx context.Context, chatID, replyTo string) error {
	return a.sendReplyText(ctx, chatID, replyTo, UnauthorizedMessage(a.manager.Language()))
}

func (a *tgAdapter) sendReplyText(ctx context.Context, chatID, replyTo, content string) error {
	chatID = strings.TrimSpace(chatID)
	content = strings.TrimSpace(content)
	if chatID == "" || content == "" {
		return nil
	}
	msgs, err := a.formatMessages(content)
	if err != nil {
		return err
	}
	for i, msg := range msgs {
		if i > 0 {
			select {
			case <-time.After(tgInterMessageDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if err := a.sendTextMessage(ctx, chatID, msg, replyTo); err != nil {
			return err
		}
		replyTo = ""
	}
	return nil
}

// formatMessages converts markdown text into one or more Telegram-ready messages.
// For entity mode (parseMode==""), it uses tgmd.ConvertAndSplit which safely splits
// at entity boundaries. For legacy parse modes, it falls back to manual split + escape.
func (a *tgAdapter) formatMessages(text string) ([]tgmd.Message, error) {
	if a.parseMode == "" {
		return tgmd.ConvertAndSplit(text, tgmd.WithMaxMessageLen(tgMaxTextLen)), nil
	}
	// Legacy mode: manual split, wrap each chunk in a plain Message
	chunks := splitTGMessage(text, tgMaxTextLen)
	msgs := make([]tgmd.Message, len(chunks))
	for i, chunk := range chunks {
		if a.parseMode == "MarkdownV2" {
			chunk = EscapeMarkdownV2(chunk)
		}
		msgs[i] = tgmd.Message{Text: chunk}
	}
	return msgs, nil
}

func (a *tgAdapter) sendTextMessage(ctx context.Context, chatID string, msg tgmd.Message, replyTo string) error {
	path := fmt.Sprintf(tgSendMessagePath, a.botToken)
	body := map[string]any{
		"chat_id":                  chatID,
		"text":                     msg.Text,
		"disable_web_page_preview": true, // Suppress link preview cards for cleaner UX
	}

	if a.parseMode == "" {
		// Entity-based: attach entities if present
		if len(msg.Entities) > 0 {
			body["entities"] = tgEntitiesToRaw(msg.Entities)
		}
	} else {
		// Legacy parse_mode (text already escaped in formatMessages)
		body["parse_mode"] = a.parseMode
	}

	if strings.TrimSpace(replyTo) != "" {
		replyToID, err := parseInt(replyTo)
		if err == nil && replyToID != 0 {
			body["reply_to_message_id"] = replyToID
		}
	}
	_, err := a.apiRequest(ctx, http.MethodPost, path, body, nil)
	return err
}

// sendExtractedImage resolves and sends an extracted image.
func (a *tgAdapter) sendExtractedImage(ctx context.Context, chatID string, img ExtractedImage, replyTo string) error {
	switch img.Kind {
	case "url":
		if IsLocalFilePath(img.Data) {
			data, err := os.ReadFile(img.Data)
			if err != nil {
				return fmt.Errorf("read local image: %w", err)
			}
			filename := filepath.Base(img.Data)
			return a.sendPhotoByUpload(ctx, chatID, data, filename, "", replyTo)
		}
		return a.sendPhotoByURL(ctx, chatID, img.Data, "", replyTo)
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
		return a.sendPhotoByUpload(ctx, chatID, data, "image"+ext, "", replyTo)
	default:
		return fmt.Errorf("unknown image kind: %s", img.Kind)
	}
}

// sendPhotoByURL sends a photo using a URL. Telegram will fetch the image server-side.
func (a *tgAdapter) sendPhotoByURL(ctx context.Context, chatID, imageURL, caption, replyTo string) error {
	path := fmt.Sprintf(tgSendPhotoPath, a.botToken)
	body := map[string]any{
		"chat_id": chatID,
		"photo":   imageURL,
	}
	if caption != "" {
		cap := caption
		if len(cap) > 1024 {
			cap = cap[:1024]
		}
		if a.parseMode == "" {
			msg := tgmd.Convert(cap)
			if len(msg.Entities) > 0 {
				body["caption"] = msg.Text
				body["caption_entities"] = tgEntitiesToRaw(msg.Entities)
			} else {
				body["caption"] = cap
			}
		} else {
			if a.parseMode == "MarkdownV2" {
				cap = EscapeMarkdownV2(cap)
			}
			body["caption"] = cap
			body["parse_mode"] = a.parseMode
		}
	}
	if strings.TrimSpace(replyTo) != "" {
		replyToID, err := parseInt(replyTo)
		if err == nil && replyToID != 0 {
			body["reply_to_message_id"] = replyToID
		}
	}
	_, err := a.apiRequest(ctx, http.MethodPost, path, body, nil)
	return err
}

// sendPhotoByUpload sends a photo by uploading file data via multipart/form-data.
func (a *tgAdapter) sendPhotoByUpload(ctx context.Context, chatID string, data []byte, filename, caption, replyTo string) error {
	path := fmt.Sprintf(tgSendPhotoPath, a.botToken)
	u := a.apiBase + path

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	if err := writer.WriteField("chat_id", chatID); err != nil {
		return fmt.Errorf("write chat_id: %w", err)
	}

	part, err := writer.CreateFormFile("photo", filename)
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return fmt.Errorf("write photo data: %w", err)
	}

	if caption != "" {
		cap := caption
		if len(cap) > 1024 {
			cap = cap[:1024]
		}
		if a.parseMode == "" {
			msg := tgmd.Convert(cap)
			if len(msg.Entities) > 0 {
				if err := writer.WriteField("caption", msg.Text); err != nil {
					return fmt.Errorf("write caption: %w", err)
				}
				entitiesJSON, _ := json.Marshal(tgEntitiesToRaw(msg.Entities))
				if err := writer.WriteField("caption_entities", string(entitiesJSON)); err != nil {
					return fmt.Errorf("write caption_entities: %w", err)
				}
			} else {
				if err := writer.WriteField("caption", cap); err != nil {
					return fmt.Errorf("write caption: %w", err)
				}
			}
		} else {
			if a.parseMode == "MarkdownV2" {
				cap = EscapeMarkdownV2(cap)
			}
			if err := writer.WriteField("caption", cap); err != nil {
				return fmt.Errorf("write caption: %w", err)
			}
			if err := writer.WriteField("parse_mode", a.parseMode); err != nil {
				return fmt.Errorf("write parse_mode: %w", err)
			}
		}
	}

	if strings.TrimSpace(replyTo) != "" {
		replyToID, perr := parseInt(replyTo)
		if perr == nil && replyToID != 0 {
			if err := writer.WriteField("reply_to_message_id", strconv.FormatInt(replyToID, 10)); err != nil {
				return fmt.Errorf("write reply_to_message_id: %w", err)
			}
		}
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respData, err := util.ReadAll(resp.Body, util.ReadLimitGeneral)
	if err != nil {
		return err
	}

	var payload map[string]any
	if err := json.Unmarshal(respData, &payload); err != nil && len(respData) > 0 {
		return fmt.Errorf("Telegram sendPhoto parse error [%d]: %s", resp.StatusCode, strings.TrimSpace(string(respData)))
	}
	if resp.StatusCode >= 400 {
		desc := strings.TrimSpace(stringFromAny(payload["description"]))
		if desc == "" {
			desc = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("Telegram sendPhoto [%d]: %s", resp.StatusCode, desc)
	}
	if ok, _ := payload["ok"].(bool); !ok {
		desc := strings.TrimSpace(stringFromAny(payload["description"]))
		return fmt.Errorf("Telegram sendPhoto not ok: %s", desc)
	}
	return nil
}

// TriggerTyping sends a "typing" chat action to the Telegram chat.
func (a *tgAdapter) TriggerTyping(ctx context.Context, binding ChannelBinding) error {
	channelID := strings.TrimSpace(binding.ChannelID)
	if channelID == "" {
		return nil
	}
	messageID := strings.TrimSpace(LastReactionTargetMessageID(binding))
	if messageID != "" {
		if !a.reactionAck.NeedsSend(binding, messageID) {
			return nil
		}
		parsedID, err := parseInt(messageID)
		if err == nil && parsedID != 0 {
			path := fmt.Sprintf(tgSetReactionPath, a.botToken)
			body := map[string]any{
				"chat_id":    channelID,
				"message_id": parsedID,
				"reaction": []map[string]string{{
					"type":  "emoji",
					"emoji": "👍",
				}},
			}
			_, err = a.apiRequest(ctx, http.MethodPost, path, body, nil)
			if err == nil {
				a.reactionAck.MarkSent(binding, messageID)
				return nil
			}
			debug.Log("tg", "adapter=%s reaction failed, falling back to typing: %v", a.name, err)
		}
	}
	return a.triggerNativeTyping(ctx, channelID)
}

func (a *tgAdapter) triggerNativeTyping(ctx context.Context, channelID string) error {
	path := "/bot" + a.botToken + "/sendChatAction"
	body := map[string]any{"chat_id": channelID, "action": "typing"}
	_, err := a.apiRequest(ctx, http.MethodPost, path, body, nil)
	if err != nil {
		debug.Log("tg", "adapter=%s typing failed: %v", a.name, err)
	}
	return err
}

func (a *tgAdapter) resolveReplyTo(binding ChannelBinding) string {
	return strings.TrimSpace(binding.LastInboundMessageID)
}

func (a *tgAdapter) recordPassiveReply(binding ChannelBinding, replyTo string) {
	if a.manager == nil || strings.TrimSpace(binding.Workspace) == "" || strings.TrimSpace(replyTo) == "" {
		return
	}
	if err := a.manager.RecordPassiveReply(binding.Workspace, replyTo, time.Now()); err != nil && err != ErrNoChannelBound {
		debug.Log("tg", "adapter=%s record passive reply failed: %v", a.name, err)
	}
}

// handleCallbackQuery processes Telegram inline keyboard button callbacks.
func (a *tgAdapter) handleCallbackQuery(ctx context.Context, cb map[string]any) {
	cbID, _ := cb["id"].(string)
	data, _ := cb["data"].(string)
	if cbID == "" {
		return
	}

	// Answer the callback to remove the loading spinner
	safego.Go("im.tg.answerCallback", func() {
		if a.httpClient == nil {
			return
		}
		path := fmt.Sprintf("/bot%s/answerCallbackQuery", a.botToken)
		a.apiRequest(context.Background(), http.MethodPost, path, map[string]any{
			"callback_query_id": cbID,
			"text":              "✓",
		}, nil)
	})

	if data == "" || a.manager == nil {
		return
	}

	// Extract sender info
	from, _ := cb["from"].(map[string]any)
	senderID := ""
	senderName := ""
	if from != nil {
		senderID = jsonInt64Str(from["id"])
		firstName, _ := from["first_name"].(string)
		lastName, _ := from["last_name"].(string)
		senderName = strings.TrimSpace(firstName + " " + lastName)
	}

	msg, _ := cb["message"].(map[string]any)
	messageID := ""
	if msg != nil {
		messageID = jsonInt64Str(msg["message_id"])
	}

	chat, _ := msg["chat"].(map[string]any)
	if chat == nil {
		// Try cb.message
		return
	}
	channelID := jsonInt64Str(chat["id"])

	a.manager.HandleInteractiveCallback(InteractiveCallback{
		MessageID: messageID,
		Values:    []string{data},
		Adapter:   a.name,
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformTelegram,
			ChannelID:  channelID,
			SenderID:   senderID,
			SenderName: senderName,
			MessageID:  messageID,
			ReceivedAt: time.Now(),
		},
	})
}

func (a *tgAdapter) seenUpdate(updateID int) bool {
	if updateID <= 0 {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for id, seenAt := range a.seen {
		if now.Sub(seenAt) > 5*time.Minute {
			delete(a.seen, id)
		}
	}
	if _, ok := a.seen[updateID]; ok {
		return true
	}
	a.seen[updateID] = now
	return false
}

func (a *tgAdapter) isConnected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.connected
}

func (a *tgAdapter) apiRequest(ctx context.Context, method, path string, body any, out *map[string]any) (*http.Response, error) {
	var bodyBytes []byte
	if body != nil {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, err
		}
		bodyBytes = buf.Bytes()
	}
	url := a.apiBase + path

	for attempt := 0; attempt <= maxRateLimitRetries; attempt++ {
		var reader io.Reader
		if bodyBytes != nil {
			reader = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, reader)
		if err != nil {
			return nil, err
		}
		if bodyBytes != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := a.httpClient.Do(req)
		if err != nil {
			return nil, err
		}

		// Handle HTTP 429 (Too Many Requests). Telegram returns retry_after
		// in the JSON response body: {"ok":false,"error_code":429,
		// "parameters":{"retry_after":N}} where retry_after is seconds.
		// Source: https://core.telegram.org/bots/api#responseparameters
		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRateLimitRetries {
			retryAfter := tgExtractRetryAfter(resp)
			resp.Body.Close()
			debug.Log("tg", "adapter=%s rate-limited (429), retry %d/%d after %v",
				a.name, attempt+1, maxRateLimitRetries, retryAfter)
			if err := sleepRetry(ctx, retryAfter); err != nil {
				return nil, err
			}
			continue
		}

		data, err := util.ReadAll(resp.Body, util.ReadLimitGeneral)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil && len(data) > 0 {
			return nil, fmt.Errorf("Telegram API parse error [%d]: %s", resp.StatusCode, strings.TrimSpace(string(data)))
		}
		if resp.StatusCode >= 400 {
			desc := strings.TrimSpace(stringFromAny(payload["description"]))
			if desc == "" {
				desc = http.StatusText(resp.StatusCode)
			}
			return nil, fmt.Errorf("Telegram API [%d] %s: %s", resp.StatusCode, path, desc)
		}
		if ok, _ := payload["ok"].(bool); !ok {
			desc := strings.TrimSpace(stringFromAny(payload["description"]))
			return nil, fmt.Errorf("Telegram API not ok: %s", desc)
		}
		if out != nil {
			*out = payload
		}
		return resp, nil
	}

	return nil, rateLimitExhausted("Telegram")
}

// tgExtractRetryAfter reads the response body and extracts the retry_after
// value from a Telegram 429 response.
// Response format: {"ok":false,"error_code":429,"description":"...",
//
//	"parameters":{"retry_after":N}}
//
// retry_after is in seconds (integer). Falls back to the Retry-After header
// via parseRetryAfter, then defaultRetryDelay.
func tgExtractRetryAfter(resp *http.Response) time.Duration {
	data, err := util.ReadAll(resp.Body, util.ReadLimitGeneral)
	if err != nil {
		return parseRetryAfter(resp)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return parseRetryAfter(resp)
	}
	if params, ok := payload["parameters"].(map[string]any); ok {
		switch v := params["retry_after"].(type) {
		case float64:
			if v > 0 {
				return capDuration(time.Duration(int(v)) * time.Second)
			}
		case int:
			if v > 0 {
				return capDuration(time.Duration(v) * time.Second)
			}
		}
	}
	// Final fallback: check the standard Retry-After header.
	return parseRetryAfter(resp)
}

func (a *tgAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:       a.name,
		Platform:   PlatformTelegram,
		Healthy:    healthy,
		Status:     status,
		LastError:  lastErr,
		ContactURI: "https://t.me/" + a.botUsername,
		UpdatedAt:  time.Now(),
	})
}

func splitTGMessage(text string, maxLen int) []string {
	return splitMessageRunes(text, maxLen, true, false, false)
}

// jsonInt64Str converts a JSON-decoded numeric value to its exact int64 string
// representation. JSON numbers decode as float64 in Go, which loses precision
// for large integers. This function handles float64 → int64 conversion safely
// and also handles negative numbers (e.g. Telegram supergroup chat IDs).
func jsonInt64Str(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.FormatInt(int64(n), 10)
	case float32:
		return strconv.FormatInt(int64(n), 10)
	case int64:
		return strconv.FormatInt(n, 10)
	case int:
		return strconv.FormatInt(int64(n), 10)
	case json.Number:
		return n.String()
	case string:
		return n
	default:
		return fmt.Sprintf("%v", v)
	}
}

func parseInt(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty string")
	}
	var n int64
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("non-digit character")
		}
		n = n*10 + int64(ch-'0')
	}
	return n, nil
}

// tgEntitiesToRaw converts tgmd.Entity slices to Telegram API's
// []map[string]any format for JSON serialization.
func tgEntitiesToRaw(entities []tgmd.Entity) []map[string]any {
	result := make([]map[string]any, len(entities))
	for i, e := range entities {
		m := map[string]any{
			"type":   string(e.Type),
			"offset": e.Offset,
			"length": e.Length,
		}
		if e.URL != "" {
			m["url"] = e.URL
		}
		if e.Language != "" {
			m["language"] = e.Language
		}
		result[i] = m
	}
	return result
}

func resolveTGSTTConfig(global config.IMSTTConfig, extra map[string]interface{}) *config.IMSTTConfig {
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

func tgPayloadKeys(value any) string {
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

// SendInteractive implements InteractiveSender.
// Sends a message with Telegram InlineKeyboard buttons.
func (a *tgAdapter) SendInteractive(ctx context.Context, binding ChannelBinding, msg InteractiveMessage) (string, error) {
	a.mu.RLock()
	connected := a.connected
	a.mu.RUnlock()
	if !connected {
		return "", fmt.Errorf("Telegram bot %q is not online", a.name)
	}

	chatID := strings.TrimSpace(binding.ChannelID)
	if chatID == "" {
		return "", fmt.Errorf("Telegram channel is not configured")
	}

	// Build InlineKeyboard buttons
	var rows [][]map[string]any
	for _, btn := range msg.Buttons {
		button := map[string]any{
			"text":          btn.Label,
			"callback_data": btn.Value,
		}
		rows = append(rows, []map[string]any{button})
	}
	// For multi-select, add a "Done" button
	if msg.MultiSelect {
		rows = append(rows, []map[string]any{
			{"text": "✅ Done", "callback_data": "__done__"},
		})
	}

	// Convert markdown to Telegram format
	messages, _ := a.formatMessages(msg.Text)
	textContent := ""
	if len(messages) > 0 {
		textContent = messages[0].Text
	}
	if textContent == "" {
		textContent = msg.Text
	}

	path := fmt.Sprintf(tgSendMessagePath, a.botToken)
	body := map[string]any{
		"chat_id":                  chatID,
		"text":                     textContent,
		"disable_web_page_preview": true, // Suppress link preview cards for cleaner UX
		"reply_markup": map[string]any{
			"inline_keyboard": rows,
		},
	}
	if a.parseMode != "" {
		body["parse_mode"] = a.parseMode
	}

	resp, err := a.apiRequest(ctx, http.MethodPost, path, body, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var respData map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		return "", nil
	}

	// Extract message_id from response
	result, _ := respData["result"].(map[string]any)
	if result != nil {
		if msgID := jsonInt64Str(result["message_id"]); msgID != "" {
			return msgID, nil
		}
	}
	return "", nil
}
