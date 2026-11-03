package im

import (
	"context"
	"fmt"
	"strings"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

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

func (a *discordAdapter) SendFile(ctx context.Context, binding ChannelBinding, file OutboundFile, caption string) error {
	channelID := strings.TrimSpace(binding.ChannelID)
	if channelID == "" {
		return ErrNoChannelBound
	}

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

	return a.sendFileMessage(ctx, channelID, filename, file.Data, strings.TrimSpace(caption))
}

func (a *slackAdapter) SendFile(ctx context.Context, binding ChannelBinding, file OutboundFile, caption string) error {
	channelID := strings.TrimSpace(binding.ChannelID)
	if channelID == "" {
		return ErrNoChannelBound
	}

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

	return a.uploadFile(ctx, channelID, "", filename, file.Data, strings.TrimSpace(caption))
}
