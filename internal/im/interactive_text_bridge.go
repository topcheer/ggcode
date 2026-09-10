package im

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

type InteractiveTextBridge struct {
	Submit func(context.Context, string, string) error // (ctx, text, adapterName)
	// PendingApprovals returns the pending approval request IDs in
	// registration order (#1657 case 2). Optional: when wired and more than
	// one approval is pending, a bare "y"/"a"/"n" is ambiguous and is NOT
	// resolved blindly - the reply must carry the target's 1-based index
	// (e.g. "y 2"); otherwise the text falls through to Submit so the user
	// and the agent both see it instead of a random approval being decided.
	PendingApprovals func() []string
	CurrentApproval  func() (requestID, toolName string, ok bool)
	ResolveApproval  func(requestID, decision string)
	CurrentAskUser   func() (requestID string, req toolpkg.AskUserRequest, ok bool)
	ResolveAskUser   func(requestID string, response toolpkg.AskUserResponse)
}

// approvalIndexFromText extracts a 1-based approval index from a reply like
// "y 2", "2 y", or "n 1" (n pending). Returns ok=false when absent or out of
// range.
func approvalIndexFromText(text string, n int) (int, bool) {
	if n <= 1 {
		return 0, false
	}
	for _, tok := range strings.Fields(text) {
		v, err := strconv.Atoi(tok)
		if err == nil && v >= 1 && v <= n && strconv.Itoa(v) == tok {
			return v, true
		}
	}
	return 0, false
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
				// #1657 case 2: with 2+ pending approvals a bare "y" is
				// ambiguous - resolving it hit whichever the selector picked
				// (possibly the dangerous one). Only resolve when the reply
				// carries the target's index; otherwise fall through to
				// Submit so the ambiguity is VISIBLE instead of decided
				// blind. Single pending stays one-tap.
				if b.PendingApprovals != nil {
					if ids := b.PendingApprovals(); len(ids) > 1 {
						if idx, ok := approvalIndexFromText(text, len(ids)); ok {
							b.ResolveApproval(ids[idx-1], decision)
							return nil
						}
						return b.Submit(ctx, text, msg.Envelope.Adapter)
					}
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
	return b.Submit(ctx, text, msg.Envelope.Adapter)
}
