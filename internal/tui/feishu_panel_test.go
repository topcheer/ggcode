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

func TestFeishuPanelEscClosesPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.openFeishuPanel()
	updated, cmd := m.handleFeishuPanelKey(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected esc panel close without command")
	}
	m = updated
	if m.feishuPanel != nil {
		t.Fatal("expected esc to close the feishu panel")
	}
}

func TestFeishuPanelCtrlCClosesPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.openFeishuPanel()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil {
		t.Fatal("expected ctrl-c feishu panel close to be synchronous")
	}
	m2 := next.(Model)
	if m2.feishuPanel != nil {
		t.Fatal("expected feishu panel to close on ctrl-c")
	}
}

func TestFeishuPanelRenderShowsBotList(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.SetConfig(&config.Config{
		IM: config.IMConfig{
			Adapters: map[string]config.IMAdapterConfig{
				"fs-a": {Enabled: true, Platform: "feishu"},
				"fs-b": {Enabled: true, Platform: "feishu"},
			},
		},
	})
	m.openFeishuPanel()
	rendered := m.renderFeishuPanel()
	if !strings.Contains(rendered, "Created: 2") || !strings.Contains(rendered, "Available: 2") {
		t.Fatalf("expected bot counts in feishu panel, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "j/k") {
		t.Fatalf("expected actions hint in feishu panel, got:\n%s", rendered)
	}
}

func TestFeishuPanelRenderLocalizesToChinese(t *testing.T) {
	m := NewModel(nil, nil)
	m.SetConfig(&config.Config{
		Language: "zh-CN",
		IM: config.IMConfig{
			Adapters: map[string]config.IMAdapterConfig{
				"fs-a": {Enabled: true, Platform: "feishu"},
			},
		},
	})
	m.openFeishuPanel()
	rendered := m.renderFeishuPanel()
	if !strings.Contains(rendered, "飞书") {
		t.Fatalf("expected localized feishu panel, got:\n%s", rendered)
	}
}

func TestFeishuPanelCreateModeInput(t *testing.T) {
	m := NewModel(nil, nil)
	m.openFeishuPanel()
	updated, _ := m.handleFeishuPanelKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = updated
	if !m.feishuPanel.createMode {
		t.Fatal("expected create mode to be active")
	}
	m.handleFeishuPanelKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("fs-test")})
	updated, _ = m.handleFeishuPanelKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated
	if m.feishuPanel.createMode {
		t.Fatal("expected create mode to be cancelled")
	}
}

func TestFeishuPanelBindSetsTargetFromWorkspace(t *testing.T) {
	m := NewModel(nil, nil)
	cfg := config.DefaultConfig()
	cfg.IM = config.IMConfig{
		Enabled: true,
		Adapters: map[string]config.IMAdapterConfig{
			"fs-b": {Enabled: true, Platform: "feishu"},
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
	imMgr.PublishAdapterState(im.AdapterState{Name: "fs-b", Platform: im.PlatformFeishu, Healthy: true, Status: "connected"})
	m.SetIMManager(imMgr)

	msg := m.bindFeishuEntry(feishuBindingEntry{Adapter: "fs-b"})()
	result, ok := msg.(feishuBindResultMsg)
	if !ok {
		t.Fatalf("expected feishuBindResultMsg, got %#v", msg)
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

func TestFeishuPanelUnbindRemovesChannel(t *testing.T) {
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
		Platform:  im.PlatformFeishu,
		Adapter:   "fs-test",
		TargetID:  "ops",
		ChannelID: "chat-1",
	}); err != nil {
		t.Fatalf("BindChannel: %v", err)
	}
	m.SetIMManager(imMgr)
	m.openFeishuPanel()
	_, cmd := m.handleFeishuPanelKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if cmd == nil {
		t.Fatal("expected unbind command")
	}
	msg := cmd()
	result, ok := msg.(feishuBindResultMsg)
	if !ok {
		t.Fatalf("expected feishuBindResultMsg, got %#v", msg)
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
}

func TestFeishuPanelNoEntriesShowsMessage(t *testing.T) {
	m := NewModel(nil, nil)
	m.openFeishuPanel()
	updated, _ := m.handleFeishuPanelKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	if m.feishuPanel == nil {
		t.Fatal("panel should still be open")
	}
	if m.feishuPanel.message == "" {
		t.Fatal("expected error message for no bot")
	}
}
