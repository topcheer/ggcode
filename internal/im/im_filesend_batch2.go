package im

// #3316 batch-2: FileSender implementations for Matrix, Discord, and Slack.
// All three ride existing generic upload primitives; the wrapper adds the
// interface, image-vs-document message typing, and connection/binding
// guards. Caption convention across FileSender adapters (#3322 review
// note): the caption rides the SAME message when the platform supports it
// natively (discord payload_json content, slack initial_comment, matrix
// filename/body), and follows as a separate message when it does not
// (qq, see batch 1).

import (
	"context"
	"fmt"
	"strings"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// --- Matrix ---

func (a *matrixAdapter) SendFile(ctx context.Context, binding ChannelBinding, file OutboundFile, caption string) error {
	roomID := strings.TrimSpace(binding.ChannelID)
	if roomID == "" {
		return ErrNoChannelBound
	}
	a.mu.RLock()
	client := a.client
	a.mu.RUnlock()
	if client == nil {
		return fmt.Errorf("matrix adapter not connected")
	}

	mimeType := file.MIME
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	resp, err := client.UploadMedia(ctx, mautrix.ReqUploadMedia{
		ContentBytes: file.Data,
		ContentType:  mimeType,
		FileName:     file.Filename,
	})
	if err != nil {
		return fmt.Errorf("upload file: %w", err)
	}

	msgType := event.MsgFile
	if strings.HasPrefix(mimeType, "image/") {
		msgType = event.MsgImage
	}
	body := file.Filename
	if body == "" {
		body = "file"
	}
	if c := strings.TrimSpace(caption); c != "" {
		body = c + " (" + body + ")"
	}
	content := &event.MessageEventContent{
		MsgType: msgType,
		Body:    body,
		URL:     resp.ContentURI.CUString(),
		Info: &event.FileInfo{
			MimeType: mimeType,
			Size:     len(file.Data),
		},
	}
	evtType, payload := a.maybeEncryptMessage(ctx, roomID, content)
	txnID := fmt.Sprintf("ggcode-file-%d", a.txnID.Add(1))
	_, err = client.SendMessageEvent(ctx, id.RoomID(roomID), evtType, payload, mautrix.ReqSendEvent{TransactionID: txnID})
	return err
}

// --- Discord ---

func (a *discordAdapter) SendFile(ctx context.Context, binding ChannelBinding, file OutboundFile, caption string) error {
	channelID := strings.TrimSpace(binding.ChannelID)
	if channelID == "" {
		return ErrNoChannelBound
	}
	// #3353: read connected under the lock like Send/SendInteractive do -
	// the gateway goroutine writes it on READY/RESUME/Close.
	a.mu.RLock()
	connected := a.connected
	a.mu.RUnlock()
	if !connected {
		return fmt.Errorf("discord adapter not connected")
	}
	filename := file.Filename
	if filename == "" {
		filename = "file"
	}
	// sendFileMessage is generic already (multipart files[0] +
	// payload_json.content caption); images and documents ride the same
	// channel and Discord renders previews by MIME.
	return a.sendFileMessage(ctx, channelID, filename, file.Data, strings.TrimSpace(caption))
}

// --- Slack ---

func (a *slackAdapter) SendFile(ctx context.Context, binding ChannelBinding, file OutboundFile, caption string) error {
	channelID := strings.TrimSpace(binding.ChannelID)
	if channelID == "" {
		return ErrNoChannelBound
	}
	// #3353: read connected under the lock like Send does - the connect
	// loop writes it on connect/close.
	a.mu.RLock()
	connected := a.connected
	a.mu.RUnlock()
	if !connected {
		return fmt.Errorf("slack adapter not connected")
	}
	filename := file.Filename
	if filename == "" {
		filename = "file"
	}
	// uploadFile is generic already (multipart file + initial_comment
	// caption + thread routing).
	return a.uploadFile(ctx, channelID, "", filename, file.Data, strings.TrimSpace(caption))
}
