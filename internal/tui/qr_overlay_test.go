package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestQROverlay_Esc_ReturnsToQQPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openQQPanel()

	if m.qqPanel == nil {
		t.Fatal("expected QQ panel to be open")
	}

	updated, _ := m.Update(qqBindResultMsg{
		message:      "Link generated",
		shareAdapter: "test-bot",
		shareLink:    "https://example.com/qq-share",
		shareQRCode:  "QR",
	})
	m = updated.(Model)

	if m.qrOverlay == nil {
		t.Fatal("expected QR overlay to be open")
	}
	if m.qqPanel == nil {
		t.Fatal("QQ panel should still be open while QR overlay is shown")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(Model)

	if m.qrOverlay != nil {
		t.Fatal("expected QR overlay to be closed after Esc")
	}
	if m.qqPanel == nil {
		t.Fatal("expected QQ panel to still be open after closing QR overlay")
	}
}

func TestQROverlay_QKey_ClosesOverlay(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openQQPanel()

	updated, _ := m.Update(qqBindResultMsg{
		message:      "Link generated",
		shareAdapter: "test-bot",
		shareLink:    "https://example.com/qq-share",
		shareQRCode:  "QR",
	})
	m = updated.(Model)

	if m.qrOverlay == nil {
		t.Fatal("expected QR overlay to be open")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Text: "q"})
	m = updated.(Model)

	if m.qrOverlay != nil {
		t.Fatal("expected QR overlay to be closed after 'q'")
	}
	if m.qqPanel == nil {
		t.Fatal("expected QQ panel to still be open after 'q'")
	}
}

func TestQROverlay_DoubleEsc_ExitsOnlyPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openQQPanel()

	updated, _ := m.Update(qqBindResultMsg{
		message:      "Link generated",
		shareAdapter: "test-bot",
		shareLink:    "https://example.com/qq-share",
		shareQRCode:  "QR",
	})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(Model)

	if m.qrOverlay != nil {
		t.Fatal("first Esc should close QR overlay")
	}
	if m.qqPanel == nil {
		t.Fatal("first Esc should keep QQ panel open")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(Model)

	if m.qqPanel != nil {
		t.Fatal("second Esc should close QQ panel")
	}
}

func TestQROverlay_Render_ContainsEscHint(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openQQPanel()

	updated, _ := m.Update(qqBindResultMsg{
		message:      "Link generated",
		shareAdapter: "test-bot",
		shareLink:    "https://example.com/qq-share",
		shareQRCode:  "QR",
	})
	m = updated.(Model)

	rendered := m.renderQROverlay()
	if rendered == "" {
		t.Fatal("expected non-empty QR overlay render")
	}

	if !strings.Contains(rendered, "Esc") && !strings.Contains(rendered, "esc") {
		t.Fatalf("expected Esc hint in QR overlay, got:\n%s", rendered)
	}
}

func TestQROverlay_Render_ContainsDiscordChannel(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openQROverlayDirect("Mobile Tunnel", "Scan with GGCode Mobile to connect", "QR", "wss://example")

	rendered := m.renderQROverlay()
	if !strings.Contains(rendered, qrOverlayDiscordURL) {
		t.Fatalf("expected Discord link in QR overlay, got:\n%s", rendered)
	}
}

func TestWrapOverlayFooterWrapsLongURL(t *testing.T) {
	wrapped := wrapOverlayFooter(
		"wss://gateway.ggcode.dev/ws?role=client&proto=3&room_id=room-1234567890&auth_ticket=auth-abcdefghijklmnopqrstuvwxyz",
		80,
	)
	if !strings.Contains(wrapped, "room_id=room-1234567890") {
		t.Fatalf("expected wrapped footer to preserve url content, got:\n%s", wrapped)
	}
	if !strings.Contains(wrapped, "\n auth_ticket=") {
		t.Fatalf("expected wrapped footer to place auth_ticket on a new line, got:\n%s", wrapped)
	}
}
