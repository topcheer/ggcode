package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
)

// #2185: remote /mode escalation gate + explicit mode-name validation on
// the IM-only SwitchMode path (the local TUI uses handleModeCommand).

func new2185Model(t *testing.T, gate bool) *Model {
	t.Helper()
	m := NewModel(nil, nil)
	cfg := config.DefaultConfig()
	cfg.IM.RemoteDangerousCommands = gate
	m.SetConfig(cfg)
	return &m
}

func TestIssue2185SwitchModeEscalationGated(t *testing.T) {
	m := new2185Model(t, false)
	deps := tuiSlashDeps{m: m}
	for _, esc := range []string{"bypass", "autopilot", "BYPASS"} {
		err := deps.SwitchMode(esc)
		if err == nil {
			t.Fatalf("SwitchMode(%q) must be refused without the opt-in gate", esc)
		}
		if !strings.Contains(err.Error(), "remote_dangerous_commands") {
			t.Fatalf("refusal must name the opt-in, got: %v", err)
		}
	}
	if m.mode != permission.SupervisedMode {
		t.Fatalf("refused escalation must not change mode, got %v", m.mode)
	}
}

func TestIssue2185SwitchModeEscalationAllowedWithOptIn(t *testing.T) {
	m := new2185Model(t, true)
	deps := tuiSlashDeps{m: m}
	if err := deps.SwitchMode("bypass"); err != nil {
		t.Fatalf("opt-in gate open: bypass must switch, got %v", err)
	}
	if m.mode != permission.BypassMode {
		t.Fatalf("expected bypass mode, got %v", m.mode)
	}
}

func TestIssue2185SwitchModeDowngradeAndLevelUngated(t *testing.T) {
	m := new2185Model(t, false)
	m.mode = permission.BypassMode
	deps := tuiSlashDeps{m: m}
	for _, name := range []string{"supervised", "plan", "auto"} {
		if err := deps.SwitchMode(name); err != nil {
			t.Fatalf("SwitchMode(%q) downgrade/level must stay ungated, got %v", name, err)
		}
	}
	if m.mode != permission.AutoMode {
		t.Fatalf("expected auto mode after ungated switch, got %v", m.mode)
	}
}

func TestIssue2185SwitchModeRejectsGarbage(t *testing.T) {
	m := new2185Model(t, false)
	deps := tuiSlashDeps{m: m}
	err := deps.SwitchMode("not-a-mode")
	if err == nil {
		t.Fatal("garbage mode name must error instead of silently switching to supervised")
	}
	if m.mode != permission.SupervisedMode {
		t.Fatalf("garbage input must not change mode, got %v", m.mode)
	}
}

func TestIssue2185ShellPassthroughGate(t *testing.T) {
	if new2185Model(t, false).remoteShellAllowed() {
		t.Fatal("gate closed: shell passthrough must be denied (fail closed, default off)")
	}
	if !new2185Model(t, true).remoteShellAllowed() {
		t.Fatal("gate open: shell passthrough must be allowed")
	}
	// nil config must fail closed
	var bare Model
	if bare.remoteShellAllowed() {
		t.Fatal("nil config must fail closed")
	}
}
