package im

import (
	"context"
	"fmt"
)

type TextSubmitBridge struct {
	Submit func(context.Context, string) error
}

func (b *TextSubmitBridge) SubmitInboundMessage(ctx context.Context, msg InboundMessage) error {
	if b == nil || b.Submit == nil {
		return fmt.Errorf("text submit bridge unavailable")
	}
	text := BuildInboundText(msg)
	if text == "" {
		return nil
	}
	return b.Submit(ctx, text)
}
