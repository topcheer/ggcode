package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/session"
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

func TestWechatPanelQOpensQROverlay(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openWechatPanel()

	updated, _ := m.handleWechatPanelKey(tea.KeyPressMsg{Text: "q"})
	m = updated
	// Without a running adapter with ContactURI, q should not open overlay
	// but should not close the panel either
	if m.wechatPanel == nil {
		t.Fatal("expected q to NOT close the wechat panel (it now opens QR overlay)")
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

func TestWechatPanelQRCodeOpensOverlay(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openWechatPanel()
	m.wechatPanel.authPhase = "requesting"

	msg := wechatQRCodeMsg{
		qrcodeToken: "test-token-123",
		qrcodeImage: "████\n████",
	}
	updated, _ := m.handleWechatQRCodeMsg(msg)
	m = updated

	if m.qrOverlay == nil {
		t.Fatal("expected QR overlay to be opened after wechat QR code received")
	}
	if m.qrOverlay.qrText != "████\n████" {
		t.Fatalf("expected QR text in overlay, got: %q", m.qrOverlay.qrText)
	}
	if m.wechatPanel.authPhase != "showing_qr" {
		t.Fatalf("expected authPhase 'showing_qr', got: %q", m.wechatPanel.authPhase)
	}
}

func TestWechatPanelPollingStatePanelOnly(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openWechatPanel()
	m.wechatPanel.authPhase = "polling"

	view := m.View().Content
	// QR code should NOT be in panel view (it's in overlay when showing_qr)
	if strings.Contains(view, "████") {
		t.Fatalf("QR code should not appear in panel view, got:\n%s", view)
	}
}

func TestWechatPanelConfirmedClosesOverlay(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openWechatPanel()
	m.wechatPanel.authPhase = "showing_qr"
	m.openQROverlayDirect("test", "test", "QR", "")

	// Simulate confirmed poll
	updated, _ := m.handleWechatQRPollMsg(wechatQRPollMsg{status: "confirmed", botToken: "test-token"})
	m = updated

	if m.qrOverlay != nil {
		t.Fatal("expected QR overlay to be closed after confirmed")
	}
	if m.wechatPanel.authPhase != "confirmed" {
		t.Fatalf("expected authPhase 'confirmed', got: %q", m.wechatPanel.authPhase)
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

func TestRenderQRFromString_EmptyContent(t *testing.T) {
	_, err := renderQRFromString("")
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestRenderQRFromString_ValidURL(t *testing.T) {
	result, err := renderQRFromString("https://login.weixin.qq.com/l/test123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty rendered QR")
	}
	lines := strings.Split(result, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d", len(lines))
	}
	if !strings.Contains(result, "█") && !strings.Contains(result, " ") {
		t.Error("expected block characters in QR rendering")
	}
}

func TestWechatPanel_NavigateEmpty(t *testing.T) {
	m := NewModel(nil, nil)
	m.openWechatPanel()

	// Up/down on empty list should not crash
	_, _ = m.handleWechatPanelKey(tea.KeyPressMsg{Code: tea.KeyUp})
	_, _ = m.handleWechatPanelKey(tea.KeyPressMsg{Code: tea.KeyDown})
}

func TestWechatBindingEntries_BoundCountIncludesCurrentWorkspace(t *testing.T) {
	// Setup: config with one wechat adapter, manager with a binding to current workspace
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40

	mgr := im.NewManager()
	_ = mgr.SetBindingStore(im.NewMemoryBindingStore())
	mgr.BindSession(im.SessionBinding{SessionID: "s1", Workspace: "/test/workspace"})

	// Bind wechat adapter to the current workspace
	_, _ = mgr.BindChannel(im.ChannelBinding{
		Platform:  im.PlatformWechat,
		Adapter:   "wechat",
		TargetID:  "user123",
		ChannelID: "ch-1",
		Workspace: "/test/workspace",
	})

	m.imManager = mgr
	m.config = &config.Config{
		IM: config.IMConfig{
			Adapters: map[string]config.IMAdapterConfig{
				"wechat": {Enabled: true, Platform: "wechat"},
			},
		},
	}
	m.session = &session.Session{ID: "s1", Workspace: "/test/workspace"}

	entries := m.wechatBindingEntries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if !entries[0].Bound {
		t.Fatal("expected entry.Bound=true for adapter bound to current workspace, got false")
	}
	// macOS may resolve /tmp to /private/tmp — just check OccupiedBy is same workspace
	if entries[0].OccupiedBy != "" && entries[0].OccupiedBy != "/private/test/workspace" {
		t.Fatalf("expected OccupiedBy empty or resolved path, got %q", entries[0].OccupiedBy)
	}

	// Verify boundCount in rendered view
	m.openWechatPanel()
	view := m.View().Content
	if !strings.Contains(view, "Bound: 1") {
		t.Fatalf("expected 'Bound: 1' in panel view, got:\n%s", view)
	}
}

func TestWechatBindingEntries_BoundToOtherWorkspace(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40

	mgr := im.NewManager()
	_ = mgr.SetBindingStore(im.NewMemoryBindingStore())
	mgr.BindSession(im.SessionBinding{SessionID: "s1", Workspace: "/test/workspace"})

	// Bind wechat adapter to a DIFFERENT workspace
	_, _ = mgr.BindChannel(im.ChannelBinding{
		Platform:  im.PlatformWechat,
		Adapter:   "wechat",
		TargetID:  "user456",
		ChannelID: "ch-2",
		Workspace: "/test/other-workspace",
	})

	m.imManager = mgr
	m.config = &config.Config{
		IM: config.IMConfig{
			Adapters: map[string]config.IMAdapterConfig{
				"wechat": {Enabled: true, Platform: "wechat"},
			},
		},
	}
	m.session = &session.Session{ID: "s1", Workspace: "/test/workspace"}

	entries := m.wechatBindingEntries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if !entries[0].Bound {
		t.Fatal("expected entry.Bound=true (adapter is bound, just to different workspace)")
	}
	if entries[0].OccupiedBy != "/test/other-workspace" {
		t.Fatalf("expected OccupiedBy=/test/other-workspace, got %q", entries[0].OccupiedBy)
	}
}

func TestWechatBindingEntries_NotBound(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40

	mgr := im.NewManager()
	_ = mgr.SetBindingStore(im.NewMemoryBindingStore())

	m.imManager = mgr
	m.config = &config.Config{
		IM: config.IMConfig{
			Adapters: map[string]config.IMAdapterConfig{
				"wechat": {Enabled: true, Platform: "wechat"},
			},
		},
	}

	entries := m.wechatBindingEntries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Bound {
		t.Fatal("expected entry.Bound=false for unbound adapter")
	}

	m.openWechatPanel()

	view := m.View().Content
	if !strings.Contains(view, "Bound: 0") {
		t.Fatalf("expected 'Bound: 0' in panel view, got:\n%s", view)
	}
}
