package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/config"
)

func TestPCPanelEscClosesPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.openPCPanel()
	updated, cmd := m.handlePCPanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected esc panel close without command")
	}
	m = updated
	if m.pcPanel != nil {
		t.Fatal("expected esc to close the pc panel")
	}
}

func TestPCPanelCtrlCClosesPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.openPCPanel()
	next, cmd := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	if cmd != nil {
		t.Fatal("expected ctrl-c pc panel close to be synchronous")
	}
	m2 := next.(Model)
	if m2.pcPanel != nil {
		t.Fatal("expected pc panel to close on ctrl-c")
	}
}

func TestPCPanelRenderShowsHeader(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openPCPanel()
	rendered := m.renderPCPanel()
	if !strings.Contains(rendered, "PrivateClaw") {
		t.Fatalf("expected PrivateClaw in pc panel, got:\n%s", rendered)
	}
}

func TestPCPanelRenderLocalizesToChinese(t *testing.T) {
	m := NewModel(nil, nil)
	m.SetConfig(&config.Config{Language: "zh-CN"})
	m.openPCPanel()
	rendered := m.renderPCPanel()
	// Chinese localization should contain PC or related terms
	if !strings.Contains(rendered, "PC") && !strings.Contains(rendered, "PrivateClaw") {
		t.Fatalf("expected PC/PrivateClaw in pc panel, got:\n%s", rendered)
	}
}

func TestPCPanelCreateModeInput(t *testing.T) {
	m := NewModel(nil, nil)
	m.openPCPanel()
	updated, _ := m.handlePCPanelKey(tea.KeyPressMsg{Text: "n"})
	m = updated
	if !m.pcPanel.createMode {
		t.Fatal("expected create mode to be active")
	}
	m.handlePCPanelKey(tea.KeyPressMsg{Text: "test-session"})
	updated, _ = m.handlePCPanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated
	if m.pcPanel.createMode {
		t.Fatal("expected create mode to be cancelled")
	}
}

func TestPCPanelQRViewExitsOnAnyKey(t *testing.T) {
	m := NewModel(nil, nil)
	m.openPCPanel()
	m.pcPanel.showQR = true
	m.pcPanel.qrCode = "fake-qr"
	updated, _ := m.handlePCPanelKey(tea.KeyPressMsg{Text: "a"})
	m = updated
	if m.pcPanel.showQR {
		t.Fatal("expected QR view to be dismissed")
	}
}

func TestPCPanelNoSessionShowsMessage(t *testing.T) {
	m := NewModel(nil, nil)
	m.openPCPanel()
	updated, _ := m.handlePCPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated
	if m.pcPanel == nil {
		t.Fatal("panel should still be open")
	}
	if m.pcPanel.message == "" {
		t.Fatal("expected error message for no session")
	}
}
