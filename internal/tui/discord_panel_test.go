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

func TestDiscordPanelEscClosesPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.openDiscordPanel()
	updated, cmd := m.handleDiscordPanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected esc panel close without command")
	}
	m = updated
	if m.discordPanel != nil {
		t.Fatal("expected esc to close the discord panel")
	}
}

func TestDiscordPanelCtrlCClosesPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.openDiscordPanel()
	next, cmd := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	if cmd != nil {
		t.Fatal("expected ctrl-c discord panel close to be synchronous")
	}
	m2 := next.(Model)
	if m2.discordPanel != nil {
		t.Fatal("expected discord panel to close on ctrl-c")
	}
}

func TestDiscordPanelRenderShowsBotList(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.SetConfig(&config.Config{
		IM: config.IMConfig{
			Adapters: map[string]config.IMAdapterConfig{
				"dc-a": {Enabled: true, Platform: "discord"},
				"dc-b": {Enabled: true, Platform: "discord"},
			},
		},
	})
	m.openDiscordPanel()
	rendered := m.renderDiscordPanel()
	if !strings.Contains(rendered, "Created: 2") || !strings.Contains(rendered, "Available: 2") {
		t.Fatalf("expected bot counts in discord panel, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "j/k") {
		t.Fatalf("expected actions hint in discord panel, got:\n%s", rendered)
	}
}

func TestDiscordPanelRenderLocalizesToChinese(t *testing.T) {
	m := NewModel(nil, nil)
	m.SetConfig(&config.Config{
		Language: "zh-CN",
		IM: config.IMConfig{
			Adapters: map[string]config.IMAdapterConfig{
				"dc-a": {Enabled: true, Platform: "discord"},
			},
		},
	})
	m.openDiscordPanel()
	rendered := m.renderDiscordPanel()
	if !strings.Contains(rendered, "Discord") {
		t.Fatalf("expected Discord in discord panel, got:\n%s", rendered)
	}
}

func TestDiscordPanelCreateModeInput(t *testing.T) {
	m := NewModel(nil, nil)
	m.openDiscordPanel()
	updated, _ := m.handleDiscordPanelKey(tea.KeyPressMsg{Text: "i"})
	m = updated
	if !m.discordPanel.createMode {
		t.Fatal("expected create mode to be active")
	}
	m.handleDiscordPanelKey(tea.KeyPressMsg{Text: "dc-test"})
	updated, _ = m.handleDiscordPanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated
	if m.discordPanel.createMode {
		t.Fatal("expected create mode to be cancelled")
	}
}

func TestDiscordPanelBindSetsTargetFromWorkspace(t *testing.T) {
	m := NewModel(nil, nil)
	cfg := config.DefaultConfig()
	cfg.IM = config.IMConfig{
		Enabled: true,
		Adapters: map[string]config.IMAdapterConfig{
			"dc-b": {Enabled: true, Platform: "discord"},
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
	imMgr.PublishAdapterState(im.AdapterState{Name: "dc-b", Platform: im.PlatformDiscord, Healthy: true, Status: "connected"})
	m.SetIMManager(imMgr)

	msg := m.bindDiscordEntry(discordBindingEntry{Adapter: "dc-b"})()
	result, ok := msg.(discordBindResultMsg)
	if !ok {
		t.Fatalf("expected discordBindResultMsg, got %#v", msg)
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

func TestDiscordPanelUnbindRemovesChannel(t *testing.T) {
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
		Platform:  im.PlatformDiscord,
		Adapter:   "dc-test",
		TargetID:  "ops",
		ChannelID: "chat-1",
	}); err != nil {
		t.Fatalf("BindChannel: %v", err)
	}
	m.SetIMManager(imMgr)
	m.openDiscordPanel()
	_, cmd := m.handleDiscordPanelKey(tea.KeyPressMsg{Text: "u"})
	if cmd == nil {
		t.Fatal("expected unbind command")
	}
	msg := cmd()
	result, ok := msg.(discordBindResultMsg)
	if !ok {
		t.Fatalf("expected discordBindResultMsg, got %#v", msg)
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
}

func TestDiscordPanelNoEntriesShowsMessage(t *testing.T) {
	m := NewModel(nil, nil)
	m.openDiscordPanel()
	updated, _ := m.handleDiscordPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated
	if m.discordPanel == nil {
		t.Fatal("panel should still be open")
	}
	if m.discordPanel.message == "" {
		t.Fatal("expected error message for no bot")
	}
}
