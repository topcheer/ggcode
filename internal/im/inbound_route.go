package im

import (
	"strings"

	"github.com/topcheer/ggcode/internal/permission"
)

type InboundRouteKind string

const (
	InboundRouteEmpty    InboundRouteKind = "empty"
	InboundRouteSlash    InboundRouteKind = "slash"
	InboundRouteShell    InboundRouteKind = "shell"
	InboundRouteApproval InboundRouteKind = "approval"
	InboundRouteAskUser  InboundRouteKind = "ask_user"
	InboundRouteMessage  InboundRouteKind = "message"
)

type InboundRoute struct {
	Kind        InboundRouteKind
	Text        string
	Decision    permission.Decision
	AlwaysAllow bool
}

func RouteInboundText(text string, hasPendingApproval, hasPendingAskUser bool) InboundRoute {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return InboundRoute{Kind: InboundRouteEmpty}
	}
	if strings.HasPrefix(trimmed, "/") {
		return InboundRoute{Kind: InboundRouteSlash, Text: trimmed}
	}

	if IsInboundShellCommand(trimmed) {
		return InboundRoute{Kind: InboundRouteShell, Text: trimmed}
	}

	if hasPendingAskUser {
		return InboundRoute{Kind: InboundRouteAskUser, Text: trimmed}
	}
	if hasPendingApproval {
		if decision, ok := ParseApprovalReply(trimmed); ok {
			return InboundRoute{
				Kind:        InboundRouteApproval,
				Text:        trimmed,
				Decision:    decision,
				AlwaysAllow: decision == permission.Allow && IsApprovalAlwaysReply(trimmed),
			}
		}
	}
	return InboundRoute{Kind: InboundRouteMessage, Text: trimmed}
}
