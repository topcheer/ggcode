package im

import (
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
)

func TestRouteInboundTextSlashWins(t *testing.T) {
	route := RouteInboundText("/help", true, true)
	if route.Kind != InboundRouteSlash {
		t.Fatalf("Kind = %q, want %q", route.Kind, InboundRouteSlash)
	}
}

func TestRouteInboundTextApproval(t *testing.T) {
	route := RouteInboundText("always", true, true)
	if route.Kind != InboundRouteApproval {
		t.Fatalf("Kind = %q, want %q", route.Kind, InboundRouteApproval)
	}
	if route.Decision != permission.Allow || !route.AlwaysAllow {
		t.Fatalf("unexpected approval route: %#v", route)
	}
}

func TestRouteInboundTextAskUser(t *testing.T) {
	route := RouteInboundText("answer", false, true)
	if route.Kind != InboundRouteAskUser {
		t.Fatalf("Kind = %q, want %q", route.Kind, InboundRouteAskUser)
	}
}
