package im

// #3325 batch-3: FileSender implementations for the remaining platforms
// (feishu, wecom, signal, mattermost, whatsapp). wechat stays URL-only by
// design - the personal-account channel has no upload API, so it keeps the
// documented path-text degradation.
//
// Caption convention across FileSender adapters (#3322 review note): the
// caption rides the SAME message when the platform supports it natively
// (signal message field, whatsapp document Caption, mattermost post
// message) and follows as a separate text message when it does not
// (feishu, wecom - same as qq in batch 1).

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

// --- Feishu ---

// SendFile (#3325): images keep the batch-0 preview semantics (im/v1/images
// image_key message); every other file rides the im/v1/files endpoint
// (file_type=stream covers arbitrary formats) as a msg_type=file message.
// Feishu file messages carry no caption field, so a non-empty caption
// follows as a text message.
func (a *feishuAdapter) SendFile(ctx context.Context, binding ChannelBinding, file OutboundFile, caption string) error {
	chatID := strings.TrimSpace(binding.ChannelID)
	if chatID == "" {
		return ErrNoChannelBound
	}
	a.mu.RLock()
	token := a.token
	a.mu.RUnlock()
	if token == "" {
		return fmt.Errorf("feishu adapter not connected")
	}

	filename := file.Filename
	if filename == "" {
		filename = "file"
	}

	if strings.HasPrefix(file.MIME, "image/") {
		imageKey, err := a.uploadImage(ctx, file.Data, filename)
		if err != nil {
			return fmt.Errorf("feishu upload image: %w", err)
		}
		if err := a.sendImageMessage(ctx, chatID, imageKey); err != nil {
			return fmt.Errorf("feishu send image: %w", err)
		}
	} else {
		fileKey, err := a.feishuUploadFile(ctx, file.Data, filename)
		if err != nil {
			return fmt.Errorf("feishu upload file: %w", err)
		}
		if err := a.feishuSendFileMsg(ctx, chatID, fileKey); err != nil {
			return fmt.Errorf("feishu send file: %w", err)
		}
	}

	if c := strings.TrimSpace(caption); c != "" {
		if _, err := a.sendTextMessage(ctx, chatID, c); err != nil {
			return fmt.Errorf("feishu send caption: %w", err)
		}
	}
	debug.Log("feishu", "adapter=%s file sent to=%s name=%s bytes=%d", a.name, chatID, filename, len(file.Data))
	return nil
}

// feishuUploadFile uploads arbitrary bytes through im/v1/files (the
// document counterpart of uploadImage) and returns the file_key. The
// file_type field is an enum on Feishu's side; "stream" is the generic
// any-format entry, so no extension sniffing is needed.
func (a *feishuAdapter) feishuUploadFile(ctx context.Context, data []byte, filename string) (string, error) {
	a.mu.RLock()
	token := a.token
	a.mu.RUnlock()

	apiBase := a.resolveAPIBase()
	url := apiBase + "/im/v1/files"

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	if err := writer.WriteField("file_type", "stream"); err != nil {
		return "", fmt.Errorf("write file_type: %w", err)
	}
	if err := writer.WriteField("file_name", filename); err != nil {
		return "", fmt.Errorf("write file_name: %w", err)
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return "", fmt.Errorf("write file data: %w", err)
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
	fileKey, _ := data2["file_key"].(string)
	if fileKey == "" {
		return "", fmt.Errorf("Feishu upload: empty file_key")
	}
	return fileKey, nil
}

// feishuSendFileMsg sends a file message by file_key (the msg_type=file
// counterpart of sendImageMessage, including the 429 retry loop).
func (a *feishuAdapter) feishuSendFileMsg(ctx context.Context, chatID, fileKey string) error {
	a.mu.RLock()
	token := a.token
	a.mu.RUnlock()

	apiBase := a.resolveAPIBase()
	url := apiBase + "/im/v1/messages?receive_id_type=chat_id"
	msgContent, _ := json.Marshal(map[string]string{"file_key": fileKey})
	body := map[string]any{
		"receive_id": chatID,
		"msg_type":   "file",
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
			debug.Log("feishu", "adapter=%s sendFile rate-limited, retry %d/%d after %v",
				a.name, attempt+1, maxRateLimitRetries, delay)
			if err := sleepRetry(ctx, delay); err != nil {
				return err
			}
			continue
		}
		respBody, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("Feishu send file [%d] %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		}
		var fileResult struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if err := json.Unmarshal(respBody, &fileResult); err != nil {
			return fmt.Errorf("parse send response: %w", err)
		}
		if fileResult.Code != 0 {
			return fmt.Errorf("Feishu send file [%d]: %s", fileResult.Code, fileResult.Msg)
		}
		return nil
	}
	return nil
}

// --- WeCom ---

// SendFile (#3325): images keep the aibot image flow (10MB cap); other
// files ride the same chunked upload protocol with type=file and the
// official 20MB file cap, then a msgtype=file frame. WeCom file messages
// carry no caption, so a non-empty caption follows as a text message.
func (a *wecomAdapter) SendFile(ctx context.Context, binding ChannelBinding, file OutboundFile, caption string) error {
	chatID := strings.TrimSpace(binding.ChannelID)
	if chatID == "" {
		chatID = strings.TrimSpace(binding.TargetID)
	}
	if chatID == "" {
		return ErrNoChannelBound
	}
	a.mu.RLock()
	ws := a.ws
	a.mu.RUnlock()
	if ws == nil {
		return fmt.Errorf("wecom adapter not connected")
	}

	filename := file.Filename
	if filename == "" {
		filename = "file"
	}

	if strings.HasPrefix(file.MIME, "image/") {
		mediaID, err := a.wecomUploadMedia(ctx, file.Data, filename)
		if err != nil {
			return fmt.Errorf("wecom upload image: %w", err)
		}
		if err := a.sendWecomImageMsg(chatID, mediaID); err != nil {
			return fmt.Errorf("wecom send image: %w", err)
		}
	} else {
		mediaID, err := a.wecomUploadMediaTyped(ctx, file.Data, filename, "file", wecomMaxFileBytes)
		if err != nil {
			return fmt.Errorf("wecom upload file: %w", err)
		}
		if err := a.sendWecomFileMsg(chatID, mediaID); err != nil {
			return fmt.Errorf("wecom send file: %w", err)
		}
	}

	if c := strings.TrimSpace(caption); c != "" {
		if err := a.sendText(ctx, chatID, c); err != nil {
			return fmt.Errorf("wecom send caption: %w", err)
		}
	}
	debug.Log("wecom", "adapter=%s file sent to=%s name=%s bytes=%d", a.name, chatID, filename, len(file.Data))
	return nil
}

// sendWecomFileMsg sends one file message via aibot_send_msg (the
// msgtype=file counterpart of sendWecomImageMsg).
func (a *wecomAdapter) sendWecomFileMsg(chatID, mediaID string) error {
	frame := map[string]any{
		"cmd":     wecomCmdSend,
		"headers": map[string]any{"req_id": newWeComReqID("file")},
		"body": map[string]any{
			"chatid":  chatID,
			"msgtype": "file",
			"file":    map[string]any{"media_id": mediaID},
		},
	}
	return a.writeAndAwaitAck(payloadReqID(frame), frame)
}

// --- Signal ---

// SendFile (#3325): all files ride the signal-cli /v2/send
// base64_attachments field (the same data-URL convention
// sendExtractedImage established) - signal renders previews per MIME on
// the receiving side. The caption rides the message field natively.
func (a *signalAdapter) SendFile(ctx context.Context, binding ChannelBinding, file OutboundFile, caption string) error {
	chatID := strings.TrimSpace(binding.ChannelID)
	if chatID == "" {
		return ErrNoChannelBound
	}

	mimeType := file.MIME
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	filename := file.Filename
	if filename == "" {
		filename = "file"
	}

	att := "data:" + mimeType + ";filename=" + sanitizeSignalAttachmentName(filename) + ";base64," + base64.StdEncoding.EncodeToString(file.Data)
	payload := map[string]any{
		"number":             a.account,
		"message":            strings.TrimSpace(caption),
		"base64_attachments": []string{att},
	}
	applySignalRecipient(payload, chatID)

	respBody, err := a.postSignalSend(ctx, payload)
	if err != nil {
		return err
	}
	trackSignalTimestamp(respBody, a.addSentTimestamp)
	debug.Log("signal", "adapter=%s file sent to=%s name=%s bytes=%d", a.name, chatID, filename, len(file.Data))
	return nil
}

// --- Mattermost ---

// SendFile (#3325): Mattermost renders file previews per MIME, so images
// and documents ride the same upload + post-with-file_ids flow (the
// sendTextWithFiles chunking and thread routing apply unchanged). The
// caption rides the post message natively.
func (a *mattermostAdapter) SendFile(ctx context.Context, binding ChannelBinding, file OutboundFile, caption string) error {
	channelID := strings.TrimSpace(binding.ChannelID)
	if channelID == "" {
		channelID = strings.TrimSpace(binding.TargetID)
	}
	if channelID == "" {
		return ErrNoChannelBound
	}

	mimeType := file.MIME
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	filename := file.Filename
	if filename == "" {
		filename = "file"
	}
	if filepath.Ext(filename) == "" {
		filename += mattermostExtForMIME(mimeType)
	}

	fileID, err := a.mmUploadFileNamed(ctx, channelID, filename, file.Data, mimeType)
	if err != nil {
		return fmt.Errorf("mattermost upload file: %w", err)
	}
	if err := a.sendTextWithFiles(ctx, channelID, binding.ThreadID, strings.TrimSpace(caption), []string{fileID}); err != nil {
		return fmt.Errorf("mattermost send file: %w", err)
	}
	debug.Log("mattermost", "adapter=%s file sent to=%s name=%s bytes=%d file_id=%s", a.name, channelID, filename, len(file.Data), fileID)
	return nil
}

// mmUploadFileNamed is uploadFile with a caller-provided filename (the
// image-only helper hardcodes "image<ext>"; arbitrary files must keep
// their real name, including the extension Mattermost keys previews on).
func (a *mattermostAdapter) mmUploadFileNamed(ctx context.Context, channelID, filename string, data []byte, mimeType string) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="files"; filename=%q`, filename))
	h.Set("Content-Type", mimeType)
	part, err := writer.CreatePart(h)
	if err != nil {
		return "", fmt.Errorf("create multipart field: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return "", fmt.Errorf("write file data: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close multipart writer: %w", err)
	}

	url := a.apiURL(fmt.Sprintf("files?channel_id=%s", channelID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return "", fmt.Errorf("create upload request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := a.conn.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := util.ReadAll(resp.Body, 1024)
		return "", fmt.Errorf("upload file → %d: %s", resp.StatusCode, string(respBody))
	}

	var fileInfos []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&fileInfos); err != nil {
		return "", fmt.Errorf("decode upload response: %w", err)
	}
	if len(fileInfos) == 0 {
		return "", fmt.Errorf("upload returned no file infos")
	}
	fileID, ok := fileInfos[0]["id"].(string)
	if !ok || fileID == "" {
		return "", fmt.Errorf("upload response missing file ID")
	}
	return fileID, nil
}

// mattermostExtForMIME maps a MIME type to a filename extension for the
// extensionless-filename fallback.
func mattermostExtForMIME(mimeType string) string {
	switch mimeType {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/png":
		return ".png"
	case "application/pdf":
		return ".pdf"
	case "application/zip":
		return ".zip"
	case "text/plain", "text/markdown":
		return ".txt"
	case "application/json":
		return ".json"
	default:
		return ".bin"
	}
}

// --- WhatsApp ---

// SendFile (#3325): images keep the preview semantics via
// sendImageByUpload; every other file uploads as whatsmeow MediaDocument
// and rides a DocumentMessage. Both message types carry the caption
// natively.
func (a *whatsappAdapter) SendFile(ctx context.Context, binding ChannelBinding, file OutboundFile, caption string) error {
	client := a.currentClient()
	if client == nil || !a.Connected() {
		return fmt.Errorf("whatsapp %q: not connected, outbound dropped", a.name)
	}
	target := strings.TrimSpace(binding.ChannelID)
	if target == "" {
		target = strings.TrimSpace(binding.TargetID)
	}
	if target == "" {
		return ErrNoChannelBound
	}
	jid, err := types.ParseJID(target)
	if err != nil {
		return fmt.Errorf("whatsapp %q: parse JID %q: %w", a.name, target, err)
	}

	cap := strings.TrimSpace(caption)
	if strings.HasPrefix(file.MIME, "image/") {
		return a.sendImageByUpload(ctx, client, jid, file.Data, cap)
	}

	uploaded, err := client.Upload(ctx, file.Data, whatsmeow.MediaDocument)
	if err != nil {
		return fmt.Errorf("whatsapp upload document: %w", err)
	}
	mimeType := file.MIME
	if mimeType == "" {
		mimeType = http.DetectContentType(file.Data)
	}
	filename := file.Filename
	if filename == "" {
		filename = "file"
	}
	docMsg := &waE2E.DocumentMessage{
		Mimetype:      proto.String(mimeType),
		URL:           &uploaded.URL,
		DirectPath:    &uploaded.DirectPath,
		MediaKey:      uploaded.MediaKey,
		FileEncSHA256: uploaded.FileEncSHA256,
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    proto.Uint64(uint64(len(file.Data))),
		FileName:      proto.String(filename),
	}
	if cap != "" {
		docMsg.Caption = proto.String(cap)
	}
	if _, err := client.SendMessage(ctx, jid, &waE2E.Message{DocumentMessage: docMsg}); err != nil {
		return fmt.Errorf("whatsapp send document: %w", err)
	}
	debug.Log("whatsapp", "adapter %q: document sent to=%s name=%s bytes=%d", a.name, jid.String(), filename, len(file.Data))
	return nil
}
