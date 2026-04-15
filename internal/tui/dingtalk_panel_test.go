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

func TestDingtalkPanelEscClosesPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.openDingtalkPanel()
	updated, cmd := m.handleDingtalkPanelKey(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected esc panel close without command")
	}
	m = updated
	if m.dingtalkPanel != nil {
		t.Fatal("expected esc to close the dingtalk panel")
	}
}

func TestDingtalkPanelCtrlCClosesPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.openDingtalkPanel()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil {
		t.Fatal("expected ctrl-c dingtalk panel close to be synchronous")
	}
	m2 := next.(Model)
	if m2.dingtalkPanel != nil {
		t.Fatal("expected dingtalk panel to close on ctrl-c")
	}
}

func TestDingtalkPanelRenderShowsBotList(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.SetConfig(&config.Config{
		IM: config.IMConfig{
			Adapters: map[string]config.IMAdapterConfig{
				"dt-a": {Enabled: true, Platform: "dingtalk"},
				"dt-b": {Enabled: true, Platform: "dingtalk"},
			},
		},
	})
	m.openDingtalkPanel()
	rendered := m.renderDingtalkPanel()
	if !strings.Contains(rendered, "Created: 2") || !strings.Contains(rendered, "Available: 2") {
		t.Fatalf("expected bot counts in dingtalk panel, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "j/k") {
		t.Fatalf("expected actions hint in dingtalk panel, got:\n%s", rendered)
	}
}

func TestDingtalkPanelRenderLocalizesToChinese(t *testing.T) {
	m := NewModel(nil, nil)
	m.SetConfig(&config.Config{
		Language: "zh-CN",
		IM: config.IMConfig{
			Adapters: map[string]config.IMAdapterConfig{
				"dt-a": {Enabled: true, Platform: "dingtalk"},
			},
		},
	})
	m.openDingtalkPanel()
	rendered := m.renderDingtalkPanel()
	if !strings.Contains(rendered, "钉钉") {
		t.Fatalf("expected localized dingtalk panel, got:\n%s", rendered)
	}
}

func TestDingtalkPanelCreateModeInput(t *testing.T) {
	m := NewModel(nil, nil)
	m.openDingtalkPanel()
	updated, _ := m.handleDingtalkPanelKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = updated
	if !m.dingtalkPanel.createMode {
		t.Fatal("expected create mode to be active")
	}
	m.handleDingtalkPanelKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("dt-test")})
	updated, _ = m.handleDingtalkPanelKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated
	if m.dingtalkPanel.createMode {
		t.Fatal("expected create mode to be cancelled")
	}
}

func TestDingtalkPanelBindSetsTargetFromWorkspace(t *testing.T) {
	m := NewModel(nil, nil)
	cfg := config.DefaultConfig()
	cfg.IM = config.IMConfig{
		Enabled: true,
		Adapters: map[string]config.IMAdapterConfig{
			"dt-b": {Enabled: true, Platform: "dingtalk"},
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
	imMgr.PublishAdapterState(im.AdapterState{Name: "dt-b", Platform: im.PlatformDingTalk, Healthy: true, Status: "connected"})
	m.SetIMManager(imMgr)

	msg := m.bindDingtalkEntry(dingtalkBindingEntry{Adapter: "dt-b"})()
	result, ok := msg.(dingtalkBindResultMsg)
	if !ok {
		t.Fatalf("expected dingtalkBindResultMsg, got %#v", msg)
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

func TestDingtalkPanelUnbindRemovesChannel(t *testing.T) {
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
		Platform:  im.PlatformDingTalk,
		Adapter:   "dt-test",
		TargetID:  "ops",
		ChannelID: "chat-1",
	}); err != nil {
		t.Fatalf("BindChannel: %v", err)
	}
	m.SetIMManager(imMgr)
	m.openDingtalkPanel()
	_, cmd := m.handleDingtalkPanelKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if cmd == nil {
		t.Fatal("expected unbind command")
	}
	msg := cmd()
	result, ok := msg.(dingtalkBindResultMsg)
	if !ok {
		t.Fatalf("expected dingtalkBindResultMsg, got %#v", msg)
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
}

func TestDingtalkPanelNoEntriesShowsMessage(t *testing.T) {
	m := NewModel(nil, nil)
	m.openDingtalkPanel()
	updated, _ := m.handleDingtalkPanelKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	if m.dingtalkPanel == nil {
		t.Fatal("panel should still be open")
	}
	if m.dingtalkPanel.message == "" {
		t.Fatal("expected error message for no bot")
	}
}
