package im

// #3316 batch-1: FileSender implementations for Telegram and QQ - the two
// platforms the operator drives today. Telegram: photos keep preview
// semantics, everything else rides sendDocument (50MB API cap). QQ: images
// keep file_type 1, everything else uploads as rich-media file (file_type 4)
// and a non-empty caption follows as a text message.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// --- Telegram ---

func (a *tgAdapter) SendFile(ctx context.Context, binding ChannelBinding, file OutboundFile, caption string) error {
	chatID := strings.TrimSpace(binding.ChannelID)
	if chatID == "" {
		return ErrNoChannelBound
	}
	if !a.isConnected() {
		return fmt.Errorf("telegram adapter %q is not connected", a.name)
	}
	if strings.HasPrefix(file.MIME, "image/") {
		// Preview semantics: reuse the photo path (caption rides along).
		return a.sendPhotoByUpload(ctx, chatID, file.Data, file.Filename, caption, "")
	}
	const maxDocBytes = 50 << 20
	if len(file.Data) > maxDocBytes {
		return fmt.Errorf("document is %d bytes; Telegram sendDocument limit is %d bytes", len(file.Data), maxDocBytes)
	}
	return a.sendDocumentByUpload(ctx, chatID, file, caption)
}

// sendDocumentByUpload mirrors sendPhotoByUpload's multipart + 429 retry
// discipline (#1242) for the document field.
func (a *tgAdapter) sendDocumentByUpload(ctx context.Context, chatID string, file OutboundFile, caption string) error {
	u := a.apiBase + fmt.Sprintf("/bot%s/sendDocument", a.botToken)
	filename := file.Filename
	if filename == "" {
		filename = "file"
	}
	for attempt := 0; attempt <= maxRateLimitRetries; attempt++ {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		if err := writer.WriteField("chat_id", chatID); err != nil {
			return fmt.Errorf("write chat_id: %w", err)
		}
		part, err := writer.CreateFormFile("document", filename)
		if err != nil {
			return fmt.Errorf("create form file: %w", err)
		}
		if _, err := part.Write(file.Data); err != nil {
			return fmt.Errorf("write document data: %w", err)
		}
		if c := strings.TrimSpace(caption); c != "" {
			if len([]rune(c)) > 1024 {
				c = string([]rune(c)[:1024])
			}
			if err := writer.WriteField("caption", c); err != nil {
				return fmt.Errorf("write caption: %w", err)
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
		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRateLimitRetries {
			retryAfter := tgExtractRetryAfter(resp)
			resp.Body.Close()
			debug.Log("tg", "adapter=%s sendDocument rate-limited (429), retry %d/%d after %v", a.name, attempt+1, maxRateLimitRetries, retryAfter)
			select {
			case <-time.After(retryAfter):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		defer resp.Body.Close()
		respData, _ := io.ReadAll(resp.Body)
		var payload map[string]any
		_ = json.Unmarshal(respData, &payload)
		if resp.StatusCode >= 400 {
			desc := strings.TrimSpace(stringFromAny(payload["description"]))
			if desc == "" {
				desc = http.StatusText(resp.StatusCode)
			}
			return fmt.Errorf("Telegram sendDocument [%d]: %s", resp.StatusCode, desc)
		}
		if ok, _ := payload["ok"].(bool); !ok {
			return fmt.Errorf("Telegram sendDocument not ok: %s", strings.TrimSpace(stringFromAny(payload["description"])))
		}
		return nil
	}
	return rateLimitExhausted("Telegram")
}

// --- QQ ---

const qqFileTypeFile = 4 // QQ rich-media file_type: 1=image, 4=generic file

func (a *qqAdapter) SendFile(ctx context.Context, binding ChannelBinding, file OutboundFile, caption string) error {
	channelID := strings.TrimSpace(binding.ChannelID)
	if channelID == "" {
		return ErrNoChannelBound
	}
	if !a.isConnected() {
		return fmt.Errorf("QQ adapter %q is not connected", a.name)
	}
	chatType := a.chatType(channelID)
	path := qqMessagePath(chatType, channelID)
	if path == "" {
		return fmt.Errorf("unknown QQ chat type %q for file upload", chatType)
	}
	fileType := qqFileTypeFile
	if strings.HasPrefix(file.MIME, "image/") {
		fileType = qqFileTypeImage
	}
	base64Data := base64.StdEncoding.EncodeToString(file.Data)
	uploadPath := qqUploadPath(chatType, channelID)
	if uploadPath == "" {
		return fmt.Errorf("unsupported QQ chat type for file upload: %s", chatType)
	}
	body := map[string]any{
		"file_type": fileType,
		"file_data": base64Data,
	}
	var out map[string]any
	if _, err := a.apiRequest(ctx, http.MethodPost, uploadPath, body, &out); err != nil {
		return fmt.Errorf("upload QQ file: %w", err)
	}
	fileInfo, _ := out["file_info"].(string)
	if fileInfo == "" {
		return fmt.Errorf("QQ file upload returned no file_info")
	}
	// #3317 discipline: the media message consumes a passive-reply slot -
	// record it like every other send.
	replyTo, replySeq := a.resolveReplyMode(binding)
	if err := a.sendMediaMessage(ctx, path, chatType, fileInfo, replyTo, replySeq); err != nil {
		return err
	}
	a.recordPassiveReplies(binding, replyTo, 1)
	if c := strings.TrimSpace(caption); c != "" {
		// QQ media messages carry no caption field; deliver it as a
		// follow-up text message on the next seq slot.
		nextSeq := replySeq + 1
		if replyTo == "" {
			nextSeq = 0
		}
		if _, err := a.sendTextMessage(ctx, path, chatType, c, replyTo, nextSeq); err != nil {
			return err
		}
		a.recordPassiveReplies(binding, replyTo, 1)
	}
	debug.Log("qq", "adapter=%s file delivered channel=%s file_type=%d bytes=%d caption=%t", a.name, channelID, fileType, len(file.Data), caption != "")
	return nil
}
