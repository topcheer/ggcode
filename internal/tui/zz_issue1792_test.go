package tui

// #1792 case 1 regression: expired/canceled/timeout QR statuses used to
// fall into a default branch whose error check was dead code (the error
// path returns earlier) - polling silently stopped with the expired QR
// still on screen and no hint to press 'a'.

import (
	"strings"
	"testing"
)

func TestWechatPollTerminalStatusSurfaces(t *testing.T) {
	m := newTestModel()
	p := &wechatPanelState{
		authPhase:   "polling",
		qrcodeToken: "tok",
	}
	m.wechatPanel = p

	// Simulate the status-msg handler directly with an expired status.
	msg := wechatQRPollMsg{status: "expired"}
	updated, _ := m.handleWechatQRPollMsg(msg)
	pp := updated.wechatPanel
	if pp == nil {
		t.Fatal("panel must survive")
	}
	if pp.authPhase != "failed" {
		t.Fatalf("terminal status must set authPhase=failed, got %q", pp.authPhase)
	}
	if !strings.Contains(pp.message, "expired") {
		t.Fatalf("message must name the terminal status, got %q", pp.message)
	}
}
