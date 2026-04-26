package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/session"
)

func TestTGPanelEscClosesPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.openTGPanel()
	updated, cmd := m.handleTGPanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected esc panel close without command")
	}
	m = updated
	if m.tgPanel != nil {
		t.Fatal("expected esc to close the tg panel")
	}
}

func TestTGPanelCtrlCClosesPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.openTGPanel()
	next, cmd := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	if cmd != nil {
		t.Fatal("expected ctrl-c tg panel close to be synchronous")
	}
	m2 := next.(Model)
	if m2.tgPanel != nil {
		t.Fatal("expected tg panel to close on ctrl-c")
	}
}

func TestTGPanelRenderShowsBotList(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.SetConfig(&config.Config{
		IM: config.IMConfig{
			Adapters: map[string]config.IMAdapterConfig{
				"tg-a": {Enabled: true, Platform: "telegram"},
				"tg-b": {Enabled: true, Platform: "telegram"},
			},
		},
	})
	m.openTGPanel()
	rendered := m.renderTGPanel()
	if !strings.Contains(rendered, "Created: 2") || !strings.Contains(rendered, "Available: 2") {
		t.Fatalf("expected bot counts in tg panel, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "j/k") {
		t.Fatalf("expected actions hint in tg panel, got:\n%s", rendered)
	}
}

func TestTGPanelRenderLocalizesToChinese(t *testing.T) {
	m := NewModel(nil, nil)
	m.SetConfig(&config.Config{
		Language: "zh-CN",
		IM: config.IMConfig{
			Adapters: map[string]config.IMAdapterConfig{
				"tg-a": {Enabled: true, Platform: "telegram"},
			},
		},
	})
	m.openTGPanel()
	rendered := m.renderTGPanel()
	if !strings.Contains(rendered, "目录") || !strings.Contains(rendered, "Telegram") {
		t.Fatalf("expected localized tg panel, got:\n%s", rendered)
	}
}

func TestTGPanelCreateModeInput(t *testing.T) {
	m := NewModel(nil, nil)
	m.openTGPanel()
	updated, _ := m.handleTGPanelKey(tea.KeyPressMsg{Text: "i"})
	m = updated
	if !m.tgPanel.createMode {
		t.Fatal("expected create mode to be active")
	}
	// Type some text
	m.handleTGPanelKey(tea.KeyPressMsg{Text: "tg-test"})
	// Cancel with Esc
	updated, _ = m.handleTGPanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated
	if m.tgPanel.createMode {
		t.Fatal("expected create mode to be cancelled")
	}
}

func TestTGPanelBindSetsTargetFromWorkspace(t *testing.T) {
	m := NewModel(nil, nil)
	cfg := config.DefaultConfig()
	cfg.IM = config.IMConfig{
		Enabled: true,
		Adapters: map[string]config.IMAdapterConfig{
			"tg-b": {Enabled: true, Platform: "telegram"},
		},
	}
	m.SetConfig(cfg)
	m.session = &session.Session{Workspace: filepath.Join(t.TempDir(), "workspace-alpha")}
	imMgr := im.NewManager()
	store := im.NewMemoryBindingStore()
	if err := imMgr.SetBindingStore(store); err != nil {
		t.Fatalf("SetBindingStore: %v", err)
	}
	imMgr.BindSession(im.SessionBinding{SessionID: "s1", Workspace: m.currentWorkspacePath()})
	imMgr.PublishAdapterState(im.AdapterState{Name: "tg-b", Platform: im.PlatformTelegram, Healthy: true, Status: "connected"})
	m.SetIMManager(imMgr)

	msg := m.bindTGEntry(tgBindingEntry{Adapter: "tg-b"})()
	result, ok := msg.(tgBindResultMsg)
	if !ok {
		t.Fatalf("expected tgBindResultMsg, got %#v", msg)
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
	binding := imMgr.CurrentBinding()
	if binding == nil {
		t.Fatal("expected current binding")
	}
	if got := binding.TargetID; got != filepath.Base(m.currentWorkspacePath()) {
		t.Fatalf("unexpected target id: %q", got)
	}
}

func TestTGPanelUnbindRemovesChannel(t *testing.T) {
	m := NewModel(nil, nil)
	m.SetConfig(config.DefaultConfig())
	m.session = &session.Session{Workspace: "/tmp/project"}
	m.config.IM.Enabled = true
	if m.config.IM.Adapters == nil {
		m.config.IM.Adapters = make(map[string]config.IMAdapterConfig)
	}
	m.config.IM.Adapters["tg-test"] = config.IMAdapterConfig{Enabled: true, Platform: "telegram"}
	imMgr := im.NewManager()
	store := im.NewMemoryBindingStore()
	if err := imMgr.SetBindingStore(store); err != nil {
		t.Fatalf("SetBindingStore: %v", err)
	}
	imMgr.BindSession(im.SessionBinding{SessionID: "s1", Workspace: "/tmp/project"})
	if _, err := imMgr.BindChannel(im.ChannelBinding{
		Platform:  im.PlatformTelegram,
		Adapter:   "tg-test",
		TargetID:  "ops",
		ChannelID: "chat-1",
	}); err != nil {
		t.Fatalf("BindChannel: %v", err)
	}
	m.SetIMManager(imMgr)
	m.openTGPanel()
	_, cmd := m.handleTGPanelKey(tea.KeyPressMsg{Text: "u"})
	if cmd == nil {
		t.Fatal("expected unbind command")
	}
	msg := cmd()
	result, ok := msg.(tgBindResultMsg)
	if !ok {
		t.Fatalf("expected tgBindResultMsg, got %#v", msg)
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
}

func TestTGPanelNoEntriesShowsMessage(t *testing.T) {
	m := NewModel(nil, nil)
	m.openTGPanel()
	updated, _ := m.handleTGPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated
	if m.tgPanel == nil {
		t.Fatal("panel should still be open")
	}
	if m.tgPanel.message == "" {
		t.Fatal("expected error message for no bot")
	}
}

func TestTGBindingLabelsMutedActiveAvailable(t *testing.T) {
	m := NewModel(nil, nil)
	entries := []tgBindingEntry{
		{Adapter: "a", Muted: true},
		{Adapter: "b", OccupiedBy: "/ws", Muted: false},
		{Adapter: "c", OccupiedBy: "/other", Muted: false},
		{Adapter: "d", Muted: false},
	}
	m.session = &session.Session{Workspace: "/ws"}
	labels := m.tgBindingLabels(entries)
	if len(labels) != 4 {
		t.Fatalf("expected 4 labels, got %d", len(labels))
	}
	if !strings.Contains(labels[0], "Muted") {
		t.Fatalf("expected muted, got %s", labels[0])
	}
	if !strings.Contains(labels[1], "Active") {
		t.Fatalf("expected active, got %s", labels[1])
	}
	if !strings.Contains(labels[2], "Bound: /other") {
		t.Fatalf("expected bound_other, got %s", labels[2])
	}
	if !strings.Contains(labels[3], "Available") {
		t.Fatalf("expected available, got %s", labels[3])
	}
}
