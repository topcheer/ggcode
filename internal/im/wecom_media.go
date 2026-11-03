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

const (
	wecomCmdUploadInit   = "aibot_upload_media_init"
	wecomCmdUploadChunk  = "aibot_upload_media_chunk"
	wecomCmdUploadFinish = "aibot_upload_media_finish"

	wecomMaxImageBytes = 10 << 20
	wecomMaxFileBytes  = 20 << 20
	wecomChunkBytes    = 512 << 10

	wecomChunkAckRetries = 3
	wecomChunkRetryDelay = 500 * time.Millisecond
)

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

func (a *wecomAdapter) wecomUploadMedia(ctx context.Context, data []byte, filename string) (string, error) {
	return a.wecomUploadMediaTyped(ctx, data, filename, "image", wecomMaxImageBytes)
}

func (a *wecomAdapter) wecomUploadMediaTyped(ctx context.Context, data []byte, filename, mediaType string, maxBytes int) (string, error) {
	if len(data) < 5 {
		return "", fmt.Errorf("WeCom: media too small (%d bytes, minimum 5)", len(data))
	}
	if len(data) > maxBytes {
		return "", fmt.Errorf("WeCom: %s is %d bytes; limit is %d bytes", mediaType, len(data), maxBytes)
	}

	sum := md5.Sum(data)
	totalChunks := (len(data) + wecomChunkBytes - 1) / wecomChunkBytes

	initFrame := map[string]any{
		"cmd":     wecomCmdUploadInit,
		"headers": map[string]any{"req_id": newWeComReqID("upload-init")},
		"body": map[string]any{
			"type":         mediaType,
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
		var ackErr error
		for attempt := 1; attempt <= wecomChunkAckRetries; attempt++ {
			_, ackErr = a.writeAndAwaitAckFrame(payloadReqID(chunkFrame), chunkFrame)
			if ackErr == nil {
				break
			}
			if attempt < wecomChunkAckRetries {
				select {
				case <-time.After(wecomChunkRetryDelay):
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
		}
		if ackErr != nil {
			return "", fmt.Errorf("WeCom upload chunk %d/%d (after %d attempts): %w", i+1, totalChunks, wecomChunkAckRetries, ackErr)
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}

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

func wecomDownloadImage(ctx context.Context, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
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
	if u := strings.LastIndexByte(rawURL, '/'); u >= 0 && u+1 < len(rawURL) {
		name = rawURL[u+1:]
	}
	return data, name, nil
}

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

			f, err := os.Open(img.Data)
			if err != nil {
				return nil, "", fmt.Errorf("read local image: %w", err)
			}
			data, err := imagepkg.ReadLimited(f, imagepkg.MaxSize)
			f.Close()
			if err != nil {
				return nil, "", fmt.Errorf("read local image: %w", err)
			}
			if err != nil {
				return nil, "", fmt.Errorf("read local image: %w", err)
			}
			return data, filepath.Base(img.Data), nil
		}
		return wecomDownloadImage(ctx, img.Data)

	default:
		return nil, "", fmt.Errorf("unknown image kind: %s", img.Kind)
	}
}

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
