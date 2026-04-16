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

func TestSlackPanelEscClosesPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.openSlackPanel()
	updated, cmd := m.handleSlackPanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected esc panel close without command")
	}
	m = updated
	if m.slackPanel != nil {
		t.Fatal("expected esc to close the slack panel")
	}
}

func TestSlackPanelCtrlCClosesPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.openSlackPanel()
	next, cmd := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	if cmd != nil {
		t.Fatal("expected ctrl-c slack panel close to be synchronous")
	}
	m2 := next.(Model)
	if m2.slackPanel != nil {
		t.Fatal("expected slack panel to close on ctrl-c")
	}
}

func TestSlackPanelRenderShowsBotList(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.SetConfig(&config.Config{
		IM: config.IMConfig{
			Adapters: map[string]config.IMAdapterConfig{
				"sl-a": {Enabled: true, Platform: "slack"},
				"sl-b": {Enabled: true, Platform: "slack"},
			},
		},
	})
	m.openSlackPanel()
	rendered := m.renderSlackPanel()
	if !strings.Contains(rendered, "Created: 2") || !strings.Contains(rendered, "Available: 2") {
		t.Fatalf("expected bot counts in slack panel, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "j/k") {
		t.Fatalf("expected actions hint in slack panel, got:\n%s", rendered)
	}
}

func TestSlackPanelRenderLocalizesToChinese(t *testing.T) {
	m := NewModel(nil, nil)
	m.SetConfig(&config.Config{
		Language: "zh-CN",
		IM: config.IMConfig{
			Adapters: map[string]config.IMAdapterConfig{
				"sl-a": {Enabled: true, Platform: "slack"},
			},
		},
	})
	m.openSlackPanel()
	rendered := m.renderSlackPanel()
	if !strings.Contains(rendered, "Slack") {
		t.Fatalf("expected Slack in slack panel, got:\n%s", rendered)
	}
}

func TestSlackPanelCreateModeInput(t *testing.T) {
	m := NewModel(nil, nil)
	m.openSlackPanel()
	updated, _ := m.handleSlackPanelKey(tea.KeyPressMsg{Text: "i"})
	m = updated
	if !m.slackPanel.createMode {
		t.Fatal("expected create mode to be active")
	}
	m.handleSlackPanelKey(tea.KeyPressMsg{Text: "sl-test"})
	updated, _ = m.handleSlackPanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated
	if m.slackPanel.createMode {
		t.Fatal("expected create mode to be cancelled")
	}
}

func TestSlackPanelBindSetsTargetFromWorkspace(t *testing.T) {
	m := NewModel(nil, nil)
	cfg := config.DefaultConfig()
	cfg.IM = config.IMConfig{
		Enabled: true,
		Adapters: map[string]config.IMAdapterConfig{
			"sl-b": {Enabled: true, Platform: "slack"},
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
	imMgr.PublishAdapterState(im.AdapterState{Name: "sl-b", Platform: im.PlatformSlack, Healthy: true, Status: "connected"})
	m.SetIMManager(imMgr)

	msg := m.bindSlackEntry(slackBindingEntry{Adapter: "sl-b"})()
	result, ok := msg.(slackBindResultMsg)
	if !ok {
		t.Fatalf("expected slackBindResultMsg, got %#v", msg)
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

func TestSlackPanelUnbindRemovesChannel(t *testing.T) {
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
		Platform:  im.PlatformSlack,
		Adapter:   "sl-test",
		TargetID:  "ops",
		ChannelID: "chat-1",
	}); err != nil {
		t.Fatalf("BindChannel: %v", err)
	}
	m.SetIMManager(imMgr)
	m.openSlackPanel()
	_, cmd := m.handleSlackPanelKey(tea.KeyPressMsg{Text: "u"})
	if cmd == nil {
		t.Fatal("expected unbind command")
	}
	msg := cmd()
	result, ok := msg.(slackBindResultMsg)
	if !ok {
		t.Fatalf("expected slackBindResultMsg, got %#v", msg)
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
}

func TestSlackPanelNoEntriesShowsMessage(t *testing.T) {
	m := NewModel(nil, nil)
	m.openSlackPanel()
	updated, _ := m.handleSlackPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated
	if m.slackPanel == nil {
		t.Fatal("panel should still be open")
	}
	if m.slackPanel.message == "" {
		t.Fatal("expected error message for no bot")
	}
}
