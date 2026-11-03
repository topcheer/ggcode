package tui

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
