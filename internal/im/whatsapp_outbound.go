package im

// Outbound (Send) phase seams for the whatsapp adapter, extracted
// mechanically from whatsappAdapter.Send (r196 domain split, r182-style
// domain file). Behavior is pinned by whatsapp_outbound_seams_test.go.

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/topcheer/ggcode/internal/debug"
)

// whatsappOutboundTarget resolves the destination string for an outbound
// message: ChannelID wins, TargetID is the fallback (mechanical extraction
// from Send).
func whatsappOutboundTarget(binding ChannelBinding) string {
	if binding.ChannelID != "" {
		return binding.ChannelID
	}
	return binding.TargetID
}

// whatsappParseTargetJID parses the resolved target into a WhatsApp JID,
// wrapping parse failures with the adapter name and target (mechanical
// extraction; the error text is behavior).
func whatsappParseTargetJID(adapterName, target string) (types.JID, error) {
	jid, err := types.ParseJID(target)
	if err != nil {
		return types.JID{}, fmt.Errorf("whatsapp %q: parse JID %q: %w", adapterName, target, err)
	}
	return jid, nil
}

// whatsappAllImagesFailed reports the #1256 all-images-failed condition:
// at least one image was attempted, none was delivered, and images existed.
func whatsappAllImagesFailed(failedImages int, sentImage bool, totalImages int) bool {
	return failedImages > 0 && !sentImage && totalImages > 0
}

// sendOutboundImages sends every extracted image in order, counting failures
// and spacing delivered images (waInterMsgDelay, #1256). It returns whether
// at least one image was sent, the failure count, and ctx.Err() when the
// inter-message delay was interrupted (mechanical extraction).
func (a *whatsappAdapter) sendOutboundImages(ctx context.Context, client *whatsmeow.Client, jid types.JID, images []ExtractedImage) (sentImage bool, failedImages int, err error) {
	for i, img := range images {
		if serr := a.sendExtractedImage(ctx, client, jid, img); serr != nil {
			failedImages++
			debug.Log("whatsapp", "adapter %q: image send failed [%d/%d]: %v", a.name, i+1, len(images), serr)
			continue
		}
		sentImage = true
		// #1256: space every delivered image (waInterMsgDelay, same intent as
		// the text chunks) - multi-image bursts tripped rate limiting and the
		// failures were silently absorbed. This also spaces the last image
		// from the first text chunk below.
		select {
		case <-time.After(waInterMsgDelay):
		case <-ctx.Done():
			return sentImage, failedImages, ctx.Err()
		}
	}
	return sentImage, failedImages, nil
}

// sendOutboundChunks sends the text chunks in order, spacing chunk-to-chunk
// and the image→text transition (waInterMsgDelay, #1256). The first chunk is
// spaced only when spaceFirst is set, i.e. an image was delivered before it
// (mechanical extraction).
func (a *whatsappAdapter) sendOutboundChunks(ctx context.Context, client *whatsmeow.Client, jid types.JID, chunks []string, spaceFirst bool) error {
	for i, chunk := range chunks {
		if i > 0 || spaceFirst {
			// #1256: also space the image→text transition, not just text→text.
			select {
			case <-time.After(waInterMsgDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		msg := &waE2E.Message{Conversation: proto.String(chunk)}
		if _, serr := client.SendMessage(ctx, jid, msg); serr != nil {
			debug.Log("whatsapp", "adapter %q: send chunk %d/%d failed: %v", a.name, i+1, len(chunks), serr)
			return fmt.Errorf("whatsapp %q: send chunk %d: %w", a.name, i+1, serr)
		}
	}
	return nil
}
