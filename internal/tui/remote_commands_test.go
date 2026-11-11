package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/lanchat"
)

func TestSetLanIdentityDefaultsTeamFromWorkspace(t *testing.T) {
	hub := lanchat.NewHub("node-t", "tui", "http://localhost:0", "", lanchat.NewStore(t.TempDir()), lanchat.WorkspaceMeta{Workspace: "/ws/alpha"})
	m := NewModel(nil, nil)
	m.lanChatHub = hub
	d := tuiSlashDeps{m: &m}

	// No team argument: team falls back to the workspace-derived default.
	msg, err := d.SetLanIdentity("carol", "devops", "")
	if err != nil {
		t.Fatalf("SetLanIdentity: %v", err)
	}
	if !strings.Contains(msg, "team: alpha") {
		t.Errorf("response %q should mention workspace default team 'alpha'", msg)
	}
	if got := hub.Team(); got != "alpha" {
		t.Errorf("hub team = %q, want 'alpha'", got)
	}

	// Explicit team is respected.
	if _, err := d.SetLanIdentity("carol", "devops", "sre"); err != nil {
		t.Fatalf("SetLanIdentity: %v", err)
	}
	if got := hub.Team(); got != "sre" {
		t.Errorf("hub team = %q, want 'sre'", got)
	}
}

func TestExecuteRemoteSlashCommandProviderSummary(t *testing.T) {
	m := NewModel(nil, nil)
	m.SetConfig(config.DefaultConfig())

	resp, handled := m.ExecuteRemoteSlashCommand("/provider")
	if !handled {
		t.Fatal("expected /provider to be handled")
	}
	if !strings.Contains(resp, "Available vendors") && !strings.Contains(resp, "可用供应商") {
		t.Fatalf("expected provider summary in response, got %q", resp)
	}
}

func TestExecuteRemoteSlashCommandModelSummary(t *testing.T) {
	m := NewModel(nil, nil)
	m.SetConfig(config.DefaultConfig())

	resp, handled := m.ExecuteRemoteSlashCommand("/model")
	if !handled {
		t.Fatal("expected /model to be handled")
	}
	if !strings.Contains(resp, "Available models") && !strings.Contains(resp, "可用模型") {
		t.Fatalf("expected model summary in response, got %q", resp)
	}
}

func TestExecuteRemoteSlashCommand_Unknown(t *testing.T) {
	m := NewModel(nil, nil)
	m.SetConfig(config.DefaultConfig())

	resp, handled := m.ExecuteRemoteSlashCommand("/foobar")
	if !handled {
		t.Fatal("expected unknown command to be handled (return error message)")
	}
	if !strings.Contains(resp, "Unknown command") && !strings.Contains(resp, "未知") {
		t.Fatalf("expected unknown command error, got %q", resp)
	}
	if !strings.Contains(resp, "/help") {
		t.Fatalf("expected /help hint, got %q", resp)
	}
}

func TestExecuteRemoteSlashCommand_NickWithoutHub(t *testing.T) {
	m := NewModel(nil, nil)
	m.SetConfig(config.DefaultConfig())

	resp, handled := m.ExecuteRemoteSlashCommand("/nick alice@dev@ggcode")
	if !handled {
		t.Fatal("expected /nick to be handled by the registry")
	}
	if strings.Contains(resp, "Unknown command") {
		t.Fatalf("/nick fell through to unknown: %q", resp)
	}
	if !strings.Contains(resp, "not available") {
		t.Fatalf("expected not-available hint without hub, got %q", resp)
	}
}
