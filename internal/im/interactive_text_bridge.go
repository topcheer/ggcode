package im

import (
	"context"
	"fmt"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

type InteractiveTextBridge struct {
	Submit          func(context.Context, string) error
	CurrentApproval func() (requestID, toolName string, ok bool)
	ResolveApproval func(requestID, decision string)
	CurrentAskUser  func() (requestID string, req toolpkg.AskUserRequest, ok bool)
	ResolveAskUser  func(requestID string, response toolpkg.AskUserResponse)
}

func (b *InteractiveTextBridge) SubmitInboundMessage(ctx context.Context, msg InboundMessage) error {
	if b == nil || b.Submit == nil {
		return fmt.Errorf("interactive text bridge unavailable")
	}
	text := BuildInboundText(msg)
	if text == "" {
		return nil
	}
	if b.CurrentApproval != nil && b.ResolveApproval != nil {
		if requestID, _, ok := b.CurrentApproval(); ok {
			if route := RouteInboundText(text, true, false); route.Kind == InboundRouteApproval {
				decision := "deny"
				if route.AlwaysAllow {
					decision = "always_allow"
				} else if route.Decision.String() == "allow" {
					decision = "allow"
				}
				b.ResolveApproval(requestID, decision)
				return nil
			}
		}
	}
	if b.CurrentAskUser != nil && b.ResolveAskUser != nil {
		if requestID, req, ok := b.CurrentAskUser(); ok {
			if route := RouteInboundText(text, false, true); route.Kind == InboundRouteAskUser {
				b.ResolveAskUser(requestID, BuildAskUserResponseFromText(req, route.Text))
				return nil
			}
		}
	}
	return b.Submit(ctx, text)
}
