package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestQROverlay_Esc_ReturnsToQQPanel verifies that pressing Esc in the QR overlay
// returns to the QQ panel (not exit everything).
func TestQROverlay_Esc_ReturnsToQQPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openQQPanel()

	if m.qqPanel == nil {
		t.Fatal("expected QQ panel to be open")
	}

	// Simulate share link result → opens QR overlay
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

	// Press Esc — should close QR overlay but NOT the QQ panel
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(Model)

	if m.qrOverlay != nil {
		t.Fatal("expected QR overlay to be closed after Esc")
	}
	if m.qqPanel == nil {
		t.Fatal("expected QQ panel to still be open after closing QR overlay")
	}
}

// TestQROverlay_QKey_ClosesOverlay verifies that pressing 'q' in the QR overlay
// closes it and returns to the panel behind it.
func TestQROverlay_QKey_ClosesOverlay(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openQQPanel()

	// Open QR overlay
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

	// Press 'q' — should close QR overlay
	updated, _ = m.Update(tea.KeyPressMsg{Text: "q"})
	m = updated.(Model)

	if m.qrOverlay != nil {
		t.Fatal("expected QR overlay to be closed after 'q'")
	}
	if m.qqPanel == nil {
		t.Fatal("expected QQ panel to still be open after 'q'")
	}
}

// TestQROverlay_DoubleEsc_ExitsOnlyPanel verifies that two Esc presses
// don't exit everything — first closes QR, second closes QQ panel.
func TestQROverlay_DoubleEsc_ExitsOnlyPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openQQPanel()

	// Open QR overlay
	updated, _ := m.Update(qqBindResultMsg{
		message:      "Link generated",
		shareAdapter: "test-bot",
		shareLink:    "https://example.com/qq-share",
		shareQRCode:  "QR",
	})
	m = updated.(Model)

	// First Esc — closes QR overlay, stays in QQ panel
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(Model)

	if m.qrOverlay != nil {
		t.Fatal("first Esc should close QR overlay")
	}
	if m.qqPanel == nil {
		t.Fatal("first Esc should keep QQ panel open")
	}

	// Second Esc — closes QQ panel
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(Model)

	if m.qqPanel != nil {
		t.Fatal("second Esc should close QQ panel")
	}
}

// TestQROverlay_Render_ContainsEscHint verifies the overlay shows Esc/q hint.
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
	// Should contain Esc hint
	if !strings.Contains(rendered, "Esc") && !strings.Contains(rendered, "esc") {
		t.Fatalf("expected Esc hint in QR overlay, got:\n%s", rendered)
	}
}
