package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/session"
)

func TestTGPanelEscClosesPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.openTGPanel()
	updated, cmd := m.handleTGPanelKey(tea.KeyMsg{Type: tea.KeyEsc})
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
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
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
	updated, _ := m.handleTGPanelKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = updated
	if !m.tgPanel.createMode {
		t.Fatal("expected create mode to be active")
	}
	// Type some text
	m.handleTGPanelKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("tg-test")})
	// Cancel with Esc
	updated, _ = m.handleTGPanelKey(tea.KeyMsg{Type: tea.KeyEsc})
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
	_, cmd := m.handleTGPanelKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
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
	updated, _ := m.handleTGPanelKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	if m.tgPanel == nil {
		t.Fatal("panel should still be open")
	}
	if m.tgPanel.message == "" {
		t.Fatal("expected error message for no bot")
	}
}
