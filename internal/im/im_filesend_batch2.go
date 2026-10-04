package im

// #3316 batch-2: FileSender implementations for Discord, Matrix and Slack.
// Mirrors batch-1's per-platform semantics (im_filesend.go): images keep
// preview delivery where the platform has one; everything else rides the
// native document/file channel. The tool layer pre-reads and size-checks
// Data (OutboundFile contract), so adapters only enforce their own
// platform-specific upload ceilings.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// --- Discord ---

// discordMaxUploadBytes is the free-tier bot upload ceiling Discord enforces
// server-side; rejecting earlier gives the user a readable error instead of
// a 413 from the API.
const discordMaxUploadBytes = 10 << 20

// SendFile (#3316 batch-2): native attachment upload via POST
// /channels/{id}/messages multipart (payload_json + files[0]). The caption
// rides along as the message content of the attachment message.
func (a *discordAdapter) SendFile(ctx context.Context, binding ChannelBinding, file OutboundFile, caption string) error {
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
	if len(file.Data) > discordMaxUploadBytes {
		return fmt.Errorf("file is %d bytes; Discord upload limit is %d bytes", len(file.Data), discordMaxUploadBytes)
	}

	payload := map[string]any{
		"content": strings.TrimSpace(caption),
		"flags":   discordSuppressEmbeds,
	}
	payloadBytes, _ := json.Marshal(payload)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("payload_json", string(payloadBytes)); err != nil {
		return fmt.Errorf("write payload_json: %w", err)
	}
	part, err := writer.CreateFormFile("files[0]", file.Filename)
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(file.Data); err != nil {
		return fmt.Errorf("write file data: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close multipart writer: %w", err)
	}

	url := a.apiBase + "/channels/" + channelID + "/messages"
	for attempt := 0; attempt <= maxRateLimitRetries; attempt++ {
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
		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			if attempt < maxRateLimitRetries {
				delay := parseRetryAfter(resp)
				debug.Log("discord", "adapter=%s file upload 429 rate limited, retry %d/%d in %v", a.name, attempt+1, maxRateLimitRetries, delay)
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return ctx.Err()
				}
				continue
			}
			return fmt.Errorf("Discord file upload rate limited after %d retries", maxRateLimitRetries)
		}
		respData, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("Discord file upload: HTTP %d: %s", resp.StatusCode, string(respData))
		}
		debug.Log("discord", "adapter=%s file uploaded filename=%s mime=%s size=%d", a.name, file.Filename, file.MIME, len(file.Data))
		return nil
	}
	return fmt.Errorf("Discord file upload: exhausted retries")
}

// --- Matrix ---

// matrixMaxUploadBytes is the default Synapse max_upload_size; homeservers
// vary, but an early readable error beats an M_TOO_LARGE round-trip.
const matrixMaxUploadBytes = 50 << 20

// SendFile (#3316 batch-2): upload to the homeserver content repository,
// then send m.file (m.image for image MIME types - preview semantics) with
// the same E2EE wrapping, threading and M_LIMIT_EXCEEDED retry behavior as
// sendImage. The caption travels as a follow-up m.text event.
func (a *matrixAdapter) SendFile(ctx context.Context, binding ChannelBinding, file OutboundFile, caption string) error {
	// #433: snapshot the client under the read lock - reconnect writes it.
	a.mu.RLock()
	client := a.client
	a.mu.RUnlock()
	if client == nil {
		return fmt.Errorf("matrix adapter not connected")
	}
	roomID := strings.TrimSpace(binding.ChannelID)
	if roomID == "" {
		roomID = strings.TrimSpace(binding.TargetID)
	}
	if roomID == "" {
		return fmt.Errorf("matrix room is not configured for current directory")
	}
	if len(file.Data) > matrixMaxUploadBytes {
		return fmt.Errorf("file is %d bytes; Matrix default upload limit is %d bytes (homeserver-dependent)", len(file.Data), matrixMaxUploadBytes)
	}

	resp, err := client.UploadMedia(ctx, mautrix.ReqUploadMedia{
		ContentBytes: file.Data,
		ContentType:  file.MIME,
		FileName:     file.Filename,
	})
	if err != nil {
		return fmt.Errorf("upload media: %w", err)
	}

	msgType := event.MsgFile
	if strings.HasPrefix(file.MIME, "image/") {
		msgType = event.MsgImage
	}
	content := &event.MessageEventContent{
		MsgType:  msgType,
		Body:     file.Filename,
		FileName: file.Filename,
		URL:      resp.ContentURI.CUString(),
		Info: &event.FileInfo{
			MimeType: file.MIME,
			Size:     len(file.Data),
		},
	}
	if binding.ThreadID != "" {
		content.RelatesTo = &event.RelatesTo{
			Type:    event.RelThread,
			EventID: id.EventID(binding.ThreadID),
		}
	}
	// #2737: wrap as m.room.encrypted when the room is E2EE.
	evtType, payload := a.maybeEncryptMessage(ctx, roomID, content)

	txnID := fmt.Sprintf("ggcode-file-%d", a.txnID.Add(1))
	for attempt := 0; attempt <= matrixMaxRetries; attempt++ {
		_, err = client.SendMessageEvent(ctx, id.RoomID(roomID), evtType, payload, mautrix.ReqSendEvent{TransactionID: txnID})
		if err == nil {
			debug.Log("matrix", "adapter=%s file uploaded filename=%s mime=%s size=%d", a.name, file.Filename, file.MIME, len(file.Data))
			if cap := strings.TrimSpace(caption); cap != "" {
				// The m.file body is the filename; the caption rides as a
				// follow-up m.text (threaded when the binding says so).
				if cerr := a.sendFileCaption(ctx, client, roomID, binding.ThreadID, cap); cerr != nil {
					debug.Log("matrix", "adapter=%s file caption send failed: %v", a.name, cerr)
				}
			}
			return nil
		}
		var respErr *mautrix.RespError
		if errors.As(err, &respErr) && respErr.ErrCode == "M_LIMIT_EXCEEDED" && attempt < matrixMaxRetries {
			select {
			case <-time.After(matrixInterMessageDelay * 2):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		return err
	}
	return err
}

// sendFileCaption sends the #3316 caption as an m.text follow-up (same
// E2EE wrapping as the file event).
func (a *matrixAdapter) sendFileCaption(ctx context.Context, client *mautrix.Client, roomID, threadID, text string) error {
	content := &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    text,
	}
	if threadID != "" {
		content.RelatesTo = &event.RelatesTo{
			Type:    event.RelThread,
			EventID: id.EventID(threadID),
		}
	}
	evtType, payload := a.maybeEncryptMessage(ctx, roomID, content)
	txnID := fmt.Sprintf("ggcode-filecap-%d", a.txnID.Add(1))
	_, err := client.SendMessageEvent(ctx, id.RoomID(roomID), evtType, payload, mautrix.ReqSendEvent{TransactionID: txnID})
	return err
}

// --- Slack ---

// SendFile (#3316 batch-2): files.upload with the caption as
// initial_comment. Slack renders previews for image MIME types itself, so
// one channel serves both images and documents here.
func (a *slackAdapter) SendFile(ctx context.Context, binding ChannelBinding, file OutboundFile, caption string) error {
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
	if err := a.uploadFile(ctx, channelID, binding.ThreadID, file.Filename, file.Data, strings.TrimSpace(caption)); err != nil {
		debug.Log("slack", "adapter=%s file upload failed filename=%s: %v", a.name, file.Filename, err)
		return err
	}
	debug.Log("slack", "adapter=%s file uploaded filename=%s mime=%s size=%d", a.name, file.Filename, file.MIME, len(file.Data))
	return nil
}
