package im

import (
	"context"
	"time"
)

// mattermostChunkPlan splits outbound text into markdown-aware chunks so code
// blocks aren't broken across posts. A files-only send with no text still
// produces a single empty chunk so the attachments get posted.
func mattermostChunkPlan(text string, fileIDs []string) []string {
	// Split using markdown-aware logic so code blocks aren't broken across chunks.
	chunks := SplitMarkdown(text, mattermostDefaultMaxPostLen)
	if len(chunks) == 0 && len(fileIDs) > 0 {
		// No text but we have files — send a post with just file attachments
		chunks = []string{""}
	}
	return chunks
}

// buildChunkPayload assembles the wire payload for one post chunk.
func (a *mattermostAdapter) buildChunkPayload(chunk, channelID, rootID string, fileIDs []string, isFirst bool) map[string]any {
	payload := map[string]any{
		"channel_id": channelID,
		"message":    chunk,
	}
	// Only thread the first chunk; subsequent chunks are replies in the same thread
	if rootID != "" && a.replyMode == "thread" {
		payload["root_id"] = rootID
	}
	// Attach file_ids to the first chunk only. This MUST happen BEFORE
	// apiPostCtx serializes and sends the payload — writing it after the
	// call only mutates the local map, leaving the files uploaded but never
	// attached to any post (orphaned on the server) (#963).
	if isFirst && len(fileIDs) > 0 {
		payload["file_ids"] = fileIDs
	}
	return payload
}

// mattermostPromoteRootID adopts the first chunk's post ID as the thread root
// for subsequent chunks when the caller did not supply an explicit root.
func mattermostPromoteRootID(rootID string, result map[string]any, multiChunk, isFirst bool) string {
	// If this is the first chunk in a thread and we have no rootID yet,
	// use the new post's ID as root for subsequent chunks.
	if rootID == "" && multiChunk && isFirst {
		if newID, ok := result["id"].(string); ok && newID != "" {
			return newID
		}
	}
	return rootID
}

// waitInterMessageDelay paces consecutive outgoing messages to avoid rate
// limiting, aborting early when the context is cancelled. Shared by the image
// upload loop and multi-chunk sends.
func (a *mattermostAdapter) waitInterMessageDelay(ctx context.Context) error {
	select {
	case <-time.After(mattermostInterMsgDelay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
