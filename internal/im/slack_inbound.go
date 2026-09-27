package im

import (
	"context"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Inbound seam helpers for the Slack adapter, extracted r195 from
// slack_adapter.go handleMessage (CC 21, the top non-test complexity in
// internal/im at the r195 base point; wecom Send CC 22 above it is the
// r182-taboo domain). Mechanical extraction: logic, comment anchors
// (#1236/#968/#540/#260), debug lines and guard ordering are byte-identical
// to the pre-refactor body.

// slackOwnMessage reports whether a Slack event was posted by this bot
// itself. #1236: bot-posted events (chat.postMessage, files.upload) carry
// bot_id and NO user field, so the userID comparison never matched them;
// file_share events (whitelisted) fed the agent its own images as fresh
// user input. The bot id fields are read by the caller under a.mu.RLock.
func slackOwnMessage(userID, eventBotID, botUserID, ownBotID string) bool {
	if userID == botUserID {
		return true
	}
	return ownBotID != "" && eventBotID == ownBotID
}

// slackMessageFields extracts the scalar fields handleMessage consumes from
// a Slack message event; missing keys degrade to zero values.
func slackMessageFields(event map[string]any) (channel, text, ts, threadTS, subtype string) {
	channel, _ = event["channel"].(string)
	text, _ = event["text"].(string)
	ts, _ = event["ts"].(string)
	threadTS, _ = event["thread_ts"].(string)
	subtype, _ = event["subtype"].(string)
	return channel, text, ts, threadTS, subtype
}

// slackSubtypeBlocked skips non-text subtypes (except file_share).
func slackSubtypeBlocked(subtype string) bool {
	return subtype != "" && subtype != "file_share"
}

// slackMergeVoiceText appends transcribed voice text to the event text,
// using the transcript alone when the message carries no text of its own.
func slackMergeVoiceText(text, voiceText string) string {
	if voiceText != "" {
		if text != "" {
			return text + "\n\n" + voiceText
		}
		return voiceText
	}
	return text
}

// slackBuildInbound assembles the platform inbound envelope for a Slack
// message event (ReceivedAt injected for testability).
func slackBuildInbound(name, channel, threadTS, userID, ts, text string, attachments []Attachment, receivedAt time.Time) InboundMessage {
	return InboundMessage{
		Envelope: Envelope{
			Adapter:    name,
			Platform:   PlatformSlack,
			ChannelID:  channel,
			ThreadID:   threadTS,
			SenderID:   userID,
			MessageID:  ts,
			ReceivedAt: receivedAt,
		},
		Text:        text,
		Attachments: attachments,
	}
}

// handleMessagePairing runs the pairing state machine for an inbound Slack
// message and reports whether pairing consumed it (reply sent + previous
// binding notified). Pairing errors publish a warning state without
// aborting; ErrNoSessionBound (unpaired DMs, the normal case) stays silent.
func (a *slackAdapter) handleMessagePairing(ctx context.Context, channel, threadTS string, inbound InboundMessage) bool {
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
		return true
	}
	return false
}

// dispatchInbound hands a non-pairing inbound message to the Manager.
// Processing failed — the message would otherwise be silently dropped:
// acknowledge the failure back to the sender so they know to retry (#260).
// Reply failure is only logged, never recursed. Unauthorized channels are
// dropped with a debug line only; ErrNoChannelBound stays silent.
func (a *slackAdapter) dispatchInbound(ctx context.Context, channel, threadTS string, inbound InboundMessage) {
	if err := a.manager.HandleInbound(ctx, inbound); err != nil {
		if err == ErrInboundChannelDenied {
			debug.Log("slack", "adapter=%s unauthorized inbound channel=%s", a.name, channel)
			return
		}
		if _, sendErr := a.sendChannelMessage(ctx, channel, threadTS, "message could not be delivered (session not ready), please retry"); sendErr != nil {
			debug.Log("slack", "adapter=%s failed to notify sender of delivery error: %v", a.name, sendErr)
		}
		if err != ErrNoChannelBound {
			a.publishState(false, "warning", err.Error())
		}
	}
}
