package tui

import (
	"errors"
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

func TestPCPanelCreateModeShowsPasteHint(t *testing.T) {
	m := NewModel(nil, nil)
	m.openPCPanel()
	m.pcPanel.createMode = true
	rendered := m.renderPCPanel()
	if !strings.Contains(rendered, pasteShortcutHintText(m.currentLanguage())) {
		t.Fatalf("expected pc panel create mode to show paste hint, got:\n%s", rendered)
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

// TestPCResultMsgRoutesToPanel pins #1386-A: pcResultMsg now HAS an
// Update case - success fills message/showQR/qrCode/inviteURI, error
// fills the message and clears the QR.
func TestPCResultMsgRoutesToPanel(t *testing.T) {
	m := newTestModel()
	m.pcPanel = &pcPanelState{}

	m2, _ := m.Update(pcResultMsg{message: "created", showQR: true, qrCode: "QR", inviteURI: "pc://x"})
	p := m2.(Model).pcPanel
	if p.message != "created" || !p.showQR || p.qrCode != "QR" || p.inviteURI != "pc://x" {
		t.Fatalf("success not routed: %#v", p)
	}

	m3, _ := m.Update(pcResultMsg{err: errors.New("boom")})
	p3 := m3.(Model).pcPanel
	if p3.message == "" || p3.showQR {
		t.Fatalf("error not routed / QR not cleared: %#v", p3)
	}
}

// TestPCCreateModeGTypes pins #1386-C: 'g' while typing a label must
// type, not toggle group mode ('group chat' -> 'roup chat' before).
func TestPCCreateModeGTypes(t *testing.T) {
	m := newTestModel()
	m.pcPanel = &pcPanelState{createMode: true, createInput: "group"}

	m2, _ := m.handlePCPanelKey(tea.KeyPressMsg{Text: "g"})
	p := m2.pcPanel
	if p.createInput != "groupg" {
		t.Fatalf("g not typed: %q (group flag flipped: %v)", p.createInput, p.createGroup)
	}

	// Empty input still toggles.
	m3 := newTestModel()
	m3.pcPanel = &pcPanelState{createMode: true}
	m4, _ := m3.handlePCPanelKey(tea.KeyPressMsg{Text: "g"})
	if !m4.pcPanel.createGroup {
		t.Fatal("toggle on empty input lost")
	}
}
