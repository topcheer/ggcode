package im

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/topcheer/ggcode/internal/debug"

	"github.com/yuin/goldmark"
)

// matrixChunkContent builds the outbound content for one markdown chunk:
// a plain-text body, best-effort HTML rendering for rich display, and an
// optional thread relation.
func matrixChunkContent(chunk, threadID string) *event.MessageEventContent {
	content := &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    chunk,
	}

	// Render markdown to HTML for rich display in Element
	var htmlBuf bytes.Buffer
	if err := goldmark.Convert([]byte(chunk), &htmlBuf); err == nil && htmlBuf.Len() > 0 {
		content.Format = event.FormatHTML
		content.FormattedBody = htmlBuf.String()
	}

	if threadID != "" {
		content.RelatesTo = &event.RelatesTo{
			Type:    event.RelThread,
			EventID: id.EventID(threadID),
		}
	}
	return content
}

// matrixRetryDelay returns the backoff for an M_LIMIT_EXCEEDED response,
// preferring the server-provided retry_after_ms when present and positive,
// falling back to twice the inter-message delay.
func matrixRetryDelay(extra map[string]any) time.Duration {
	retryAfter := matrixInterMessageDelay * 2
	if ms, ok := extra["retry_after_ms"]; ok {
		// #664: clamp BEFORE the float→Duration conversion (same family
		// as #513/#658). retry_after_ms > 9.22e12 or +Inf wraps to a
		// large negative duration and time.After(negative) fires
		// immediately, bypassing the server's backoff.
		if msFloat, ok2 := ms.(float64); ok2 && msFloat > 0 {
			retryAfter = matrixRetryAfter(msFloat)
		}
	}
	return retryAfter
}

// matrixSleepCtx waits d or until ctx is done. All outbound matrix pacing
// (inter-message throttle and rate-limit backoff) funnels through here.
func matrixSleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// sendChunkWithRetry sends one chunk under a fresh transaction ID, retrying
// M_LIMIT_EXCEEDED with the server-provided backoff up to matrixMaxRetries.
// Context cancellation is returned bare; hard failures are wrapped with the
// room for diagnosis.
func (a *matrixAdapter) sendChunkWithRetry(ctx context.Context, client *mautrix.Client, roomID string, content *event.MessageEventContent) error {
	txnID := fmt.Sprintf("ggcode-%d", a.txnID.Add(1))
	var err error
	for attempt := 0; attempt <= matrixMaxRetries; attempt++ {
		_, err = client.SendMessageEvent(ctx, id.RoomID(roomID), event.EventMessage, content, mautrix.ReqSendEvent{TransactionID: txnID})
		if err == nil {
			break
		}
		// Retry on M_LIMIT_EXCEEDED with server-provided delay.
		var respErr *mautrix.RespError
		if errors.As(err, &respErr) && respErr.ErrCode == "M_LIMIT_EXCEEDED" && attempt < matrixMaxRetries {
			retryAfter := matrixRetryDelay(respErr.ExtraData)
			debug.Log("matrix", "adapter=%s rate-limited (M_LIMIT_EXCEEDED), retry %d/%d after %v",
				a.name, attempt+1, matrixMaxRetries, retryAfter)
			if sleepErr := matrixSleepCtx(ctx, retryAfter); sleepErr != nil {
				return sleepErr
			}
			continue
		}
		break
	}
	if err != nil {
		return fmt.Errorf("matrix send to %s: %w", roomID, err)
	}
	return nil
}
