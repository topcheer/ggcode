package tui

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/config"
)

func TestWechatPanelEscClosesPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openWechatPanel()

	updated, cmd := m.handleWechatPanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected esc to close without command")
	}
	m = updated
	if m.wechatPanel != nil {
		t.Fatal("expected esc to close the wechat panel")
	}
}

func TestWechatPanelQAlsoClosesPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openWechatPanel()

	updated, cmd := m.handleWechatPanelKey(tea.KeyPressMsg{Text: "q"})
	if cmd != nil {
		t.Fatal("expected q to close without command")
	}
	m = updated
	if m.wechatPanel != nil {
		t.Fatal("expected q to close the wechat panel")
	}
}

func TestWechatPanelKeyATriggersQRRequest(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openWechatPanel()

	updated, cmd := m.handleWechatPanelKey(tea.KeyPressMsg{Text: "a"})
	m = updated
	if cmd == nil {
		t.Fatal("expected 'a' to return a command (QR request)")
	}
	if m.wechatPanel == nil {
		t.Fatal("expected panel to remain open")
	}
	if m.wechatPanel.authPhase != "requesting" {
		t.Errorf("expected authPhase 'requesting', got %q", m.wechatPanel.authPhase)
	}
}

func TestWechatPanelViewShowsNoBotsMessage(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openWechatPanel()

	view := m.View().Content
	if !strings.Contains(view, "Scan QR") || !strings.Contains(view, "/wechat") {
		t.Fatalf("expected wechat panel content, got:\n%s", view)
	}
}

func TestWechatPanelViewShowsQRCode(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openWechatPanel()
	m.wechatPanel.authPhase = "showing_qr"
	m.wechatPanel.qrcodeImage = "████\n█  █\n████"

	view := m.View().Content
	if !strings.Contains(view, "████") {
		t.Fatalf("expected QR code in panel, got:\n%s", view)
	}
	if !strings.Contains(view, "Scan QR") {
		t.Fatalf("expected scan QR prompt, got:\n%s", view)
	}
}

func TestWechatPanelViewShowsPollingState(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openWechatPanel()
	m.wechatPanel.authPhase = "polling"

	view := m.View().Content
	if !strings.Contains(view, "Waiting") && !strings.Contains(view, "waiting") {
		t.Fatalf("expected waiting/polling message, got:\n%s", view)
	}
}

func TestWechatPanelViewShowsConfirmed(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openWechatPanel()
	m.wechatPanel.authPhase = "confirmed"

	view := m.View().Content
	if !strings.Contains(view, "confirmed") && !strings.Contains(view, "authorized") {
		t.Fatalf("expected confirmed message, got:\n%s", view)
	}
}

func TestWechatQRCodeMsg_SetsQRAndStartsPolling(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openWechatPanel()
	m.wechatPanel.authPhase = "requesting"

	msg := wechatQRCodeMsg{
		qrcodeToken: "test-token-123",
		qrcodeImage: "████\n████",
	}
	updated, cmd := m.handleWechatQRCodeMsg(msg)
	m = updated

	if m.wechatPanel.qrcodeToken != "test-token-123" {
		t.Errorf("expected qrcode token, got %q", m.wechatPanel.qrcodeToken)
	}
	if m.wechatPanel.authPhase != "showing_qr" {
		t.Errorf("expected authPhase 'showing_qr', got %q", m.wechatPanel.authPhase)
	}
	if cmd == nil {
		t.Fatal("expected polling command after QR code received")
	}
}

func TestWechatQRCodeMsg_ErrorClearsAuth(t *testing.T) {
	m := NewModel(nil, nil)
	m.openWechatPanel()
	m.wechatPanel.authPhase = "requesting"

	msg := wechatQRCodeMsg{err: fmt.Errorf("network error")}
	updated, _ := m.handleWechatQRCodeMsg(msg)
	m = updated

	if m.wechatPanel.authPhase != "" {
		t.Errorf("expected authPhase cleared on error, got %q", m.wechatPanel.authPhase)
	}
	if !strings.Contains(m.wechatPanel.message, "network error") {
		t.Errorf("expected error message, got %q", m.wechatPanel.message)
	}
}

func TestWechatQRPollMsg_ConfirmedSavesToken(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openWechatPanel()
	m.wechatPanel.authPhase = "polling"
	m.wechatPanel.qrcodeToken = "qr-token"

	msg := wechatQRPollMsg{status: "confirmed", botToken: "my-bot-token"}
	updated, cmd := m.handleWechatQRPollMsg(msg)
	m = updated

	if m.wechatPanel.botToken != "my-bot-token" {
		t.Errorf("expected bot token saved, got %q", m.wechatPanel.botToken)
	}
	if m.wechatPanel.authPhase != "confirmed" {
		t.Errorf("expected authPhase 'confirmed', got %q", m.wechatPanel.authPhase)
	}
	_ = cmd // may be nil if no config (expected in test)
}

func TestWechatQRPollMsg_WaitContinuesPolling(t *testing.T) {
	m := NewModel(nil, nil)
	m.openWechatPanel()
	m.wechatPanel.authPhase = "showing_qr"
	m.wechatPanel.qrcodeToken = "qr-token"

	msg := wechatQRPollMsg{status: "wait"}
	updated, cmd := m.handleWechatQRPollMsg(msg)
	m = updated

	if m.wechatPanel.authPhase != "polling" {
		t.Errorf("expected authPhase 'polling', got %q", m.wechatPanel.authPhase)
	}
	if cmd == nil {
		t.Fatal("expected continue polling command")
	}
}

func TestWechatQRPollMsg_ScannedContinuesPolling(t *testing.T) {
	m := NewModel(nil, nil)
	m.openWechatPanel()
	m.wechatPanel.authPhase = "showing_qr"
	m.wechatPanel.qrcodeToken = "qr-token"

	msg := wechatQRPollMsg{status: "scanned"}
	updated, cmd := m.handleWechatQRPollMsg(msg)
	m = updated

	if m.wechatPanel.authPhase != "polling" {
		t.Errorf("expected authPhase 'polling', got %q", m.wechatPanel.authPhase)
	}
	if !strings.Contains(m.wechatPanel.message, "scanned") && !strings.Contains(m.wechatPanel.message, "Scanned") {
		t.Errorf("expected scanned message, got %q", m.wechatPanel.message)
	}
	if cmd == nil {
		t.Fatal("expected continue polling command")
	}
}

func TestWechatPanelRenderLocalizesToChinese(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.SetConfig(&config.Config{
		Language: "zh-CN",
	})
	m.openWechatPanel()

	view := m.View().Content
	// Should contain Chinese strings
	if !strings.Contains(view, "微信") {
		t.Fatalf("expected Chinese localization in wechat panel, got:\n%s", view)
	}
}

func TestRenderWechatQRFromBase64_InvalidBase64(t *testing.T) {
	_, err := renderWechatQRFromBase64("!!!invalid-base64!!!")
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestRenderWechatQRFromBase64_InvalidPNG(t *testing.T) {
	// Valid base64 but not a valid PNG
	_, err := renderWechatQRFromBase64("aW52YWxpZCBwbmc=")
	if err == nil {
		t.Fatal("expected error for invalid PNG data")
	}
}

func TestRenderQRFromImage(t *testing.T) {
	// Create a minimal 2x2 image
	img := createTestImage(2, 2)
	result := renderQRFromImage(img)
	if result == "" {
		t.Fatal("expected non-empty rendered QR")
	}
	lines := strings.Split(result, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines (border + 1 row + border), got %d", len(lines))
	}
	// Check borders use block chars
	if !strings.Contains(lines[0], "▄") && !strings.Contains(lines[0], "█") {
		t.Errorf("expected top border with block chars, got %q", lines[0])
	}
}

func TestWechatPanel_NavigateEmpty(t *testing.T) {
	m := NewModel(nil, nil)
	m.openWechatPanel()

	// Up/down on empty list should not crash
	_, _ = m.handleWechatPanelKey(tea.KeyPressMsg{Text: "up"})
	_, _ = m.handleWechatPanelKey(tea.KeyPressMsg{Text: "down"})
}

func createTestImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if (x+y)%2 == 0 {
				img.Set(x, y, color.Black)
			} else {
				img.Set(x, y, color.White)
			}
		}
	}
	return img
}
