package im

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	imagepkg "github.com/topcheer/ggcode/internal/image"
)

// WeCom AI Bot media support (#1017 follow-up parity work).
//
// The long-connection protocol sends images as msgtype=image frames carrying
// a media_id from the chunked temporary-media upload:
//   aibot_upload_media_init   → upload_id
//   aibot_upload_media_chunk  (base64 chunks, idempotent, order-free)
//   aibot_upload_media_finish → media_id (valid 3 days)
// Then: aibot_send_msg {msgtype: "image", image: {media_id}}.
//
// Official limits: image <=10MB, png/jpg/jpeg/gif; <=100 chunks; upload
// session valid 30min; 30 uploads/min per bot.

const (
	wecomCmdUploadInit   = "aibot_upload_media_init"
	wecomCmdUploadChunk  = "aibot_upload_media_chunk"
	wecomCmdUploadFinish = "aibot_upload_media_finish"

	wecomMaxImageBytes = 10 << 20 // official image cap
	wecomChunkBytes    = 512 << 10
)

// writeAndAwaitAckFrame is writeAndAwaitAck for commands whose ack body
// carries data (upload_id, media_id). The ack frame is returned alongside the
// error so callers can extract response fields.
func (a *wecomAdapter) writeAndAwaitAckFrame(reqID string, frame map[string]any) (map[string]any, error) {
	a.mu.RLock()
	ws := a.ws
	a.mu.RUnlock()
	if ws == nil {
		return nil, fmt.Errorf("WeCom: not connected")
	}

	ch := make(chan map[string]any, 1)
	a.pendingAcks.Store(reqID, ch)
	defer a.pendingAcks.Delete(reqID)

	a.writeMu.Lock()
	err := ws.WriteJSON(frame)
	a.writeMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("WeCom: write %s: %w", frame["cmd"], err)
	}

	timeout := a.ackWait()
	select {
	case payload := <-ch:
		return payload, wecomAckError(reqID, payload)
	case <-time.After(timeout):
		return nil, fmt.Errorf("WeCom: no ack for req_id=%s within %v", reqID, timeout)
	}
}

// wecomUploadMedia uploads image bytes through the chunked media protocol and
// returns the media_id.
func (a *wecomAdapter) wecomUploadMedia(ctx context.Context, data []byte, filename string) (string, error) {
	if len(data) < 5 {
		return "", fmt.Errorf("WeCom: media too small (%d bytes, minimum 5)", len(data))
	}
	if len(data) > wecomMaxImageBytes {
		return "", fmt.Errorf("WeCom: image is %d bytes; limit is %d bytes", len(data), wecomMaxImageBytes)
	}

	sum := md5.Sum(data)
	totalChunks := (len(data) + wecomChunkBytes - 1) / wecomChunkBytes

	// Step 1: init
	initFrame := map[string]any{
		"cmd":     wecomCmdUploadInit,
		"headers": map[string]any{"req_id": newWeComReqID("upload-init")},
		"body": map[string]any{
			"type":         "image",
			"filename":     filename,
			"total_size":   len(data),
			"total_chunks": totalChunks,
			"md5":          hex.EncodeToString(sum[:]),
		},
	}
	initAck, err := a.writeAndAwaitAckFrame(payloadReqID(initFrame), initFrame)
	if err != nil {
		return "", fmt.Errorf("WeCom upload init: %w", err)
	}
	initBody, _ := initAck["body"].(map[string]any)
	uploadID, _ := initBody["upload_id"].(string)
	if uploadID == "" {
		return "", fmt.Errorf("WeCom upload init: no upload_id in ack")
	}

	// Step 2: chunks (base64, sequential for simplicity; server accepts any order)
	for i := 0; i < totalChunks; i++ {
		end := (i + 1) * wecomChunkBytes
		if end > len(data) {
			end = len(data)
		}
		chunkFrame := map[string]any{
			"cmd":     wecomCmdUploadChunk,
			"headers": map[string]any{"req_id": newWeComReqID("upload-chunk")},
			"body": map[string]any{
				"upload_id":   uploadID,
				"chunk_index": i,
				"base64_data": base64.StdEncoding.EncodeToString(data[i*wecomChunkBytes : end]),
			},
		}
		if _, err := a.writeAndAwaitAckFrame(payloadReqID(chunkFrame), chunkFrame); err != nil {
			return "", fmt.Errorf("WeCom upload chunk %d/%d: %w", i+1, totalChunks, err)
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}

	// Step 3: finish → media_id
	finFrame := map[string]any{
		"cmd":     wecomCmdUploadFinish,
		"headers": map[string]any{"req_id": newWeComReqID("upload-finish")},
		"body":    map[string]any{"upload_id": uploadID},
	}
	finAck, err := a.writeAndAwaitAckFrame(payloadReqID(finFrame), finFrame)
	if err != nil {
		return "", fmt.Errorf("WeCom upload finish: %w", err)
	}
	finBody, _ := finAck["body"].(map[string]any)
	mediaID, _ := finBody["media_id"].(string)
	if mediaID == "" {
		return "", fmt.Errorf("WeCom upload finish: no media_id in ack")
	}
	return mediaID, nil
}

// sendWecomImageMsg sends one image message via aibot_send_msg.
func (a *wecomAdapter) sendWecomImageMsg(chatID, mediaID string) error {
	frame := map[string]any{
		"cmd":     wecomCmdSend,
		"headers": map[string]any{"req_id": newWeComReqID("img")},
		"body": map[string]any{
			"chatid":  chatID,
			"msgtype": "image",
			"image":   map[string]any{"media_id": mediaID},
		},
	}
	return a.writeAndAwaitAck(payloadReqID(frame), frame)
}

// wecomResolveImageBytes turns an ExtractedImage into raw bytes (WeCom needs
// bytes for the chunked upload; URLs must be downloaded first).
func (a *wecomAdapter) wecomResolveImageBytes(ctx context.Context, img ExtractedImage) ([]byte, string, error) {
	switch img.Kind {
	case "data_url":
		parts := strings.SplitN(img.Data, ",", 2)
		if len(parts) < 2 {
			return nil, "", fmt.Errorf("invalid data URL")
		}
		data, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, "", fmt.Errorf("decode data URL: %w", err)
		}
		return data, "image" + signalMIMEExt(parts[0]), nil

	case "url":
		if IsLocalFilePath(img.Data) {
			data, err := os.ReadFile(img.Data)
			if err != nil {
				return nil, "", fmt.Errorf("read local image: %w", err)
			}
			return data, filepath.Base(img.Data), nil
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, img.Data, nil)
		if err != nil {
			return nil, "", fmt.Errorf("create request: %w", err)
		}
		resp, err := imageDownloadClient.Do(req)
		if err != nil {
			return nil, "", fmt.Errorf("download image: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, "", fmt.Errorf("download image: HTTP %d", resp.StatusCode)
		}
		data, err := imagepkg.ReadLimited(resp.Body, wecomMaxImageBytes)
		if err != nil {
			return nil, "", fmt.Errorf("read image response: %w", err)
		}
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") &&
			!strings.HasPrefix(imagepkg.DetectMIME(data), "image/") {
			return nil, "", fmt.Errorf("content is not an image")
		}
		name := "image.png"
		if u := strings.LastIndexByte(img.Data, '/'); u >= 0 && u+1 < len(img.Data) {
			name = img.Data[u+1:]
		}
		return data, name, nil

	default:
		return nil, "", fmt.Errorf("unknown image kind: %s", img.Kind)
	}
}

// sendWecomImage uploads and sends one extracted image to chatID.
func (a *wecomAdapter) sendWecomImage(ctx context.Context, chatID string, img ExtractedImage) error {
	data, filename, err := a.wecomResolveImageBytes(ctx, img)
	if err != nil {
		return err
	}
	mediaID, err := a.wecomUploadMedia(ctx, data, filename)
	if err != nil {
		return err
	}
	return a.sendWecomImageMsg(chatID, mediaID)
}
