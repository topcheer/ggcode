package commands

import (
	"testing"
)

func TestNewManager(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	if m == nil {
		t.Fatal("expected non-nil manager")
	}
}

func TestManager_Reload(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	// Second reload should work (may return false if no change)
	result := m.Reload()
	_ = result
}

func TestManager_Commands(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	cmds := m.Commands()
	if cmds == nil {
		t.Error("expected non-nil commands")
	}
}

func TestManager_List(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	list := m.List()
	if list == nil {
		t.Error("expected non-nil list")
	}
}

func TestManager_Get(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	_, ok := m.Get("nonexistent")
	if ok {
		t.Error("expected false for nonexistent command")
	}
}

func TestManager_Get_Nil(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	_, ok := m.Get("")
	_ = ok
}

func TestManager_SetExtraProviders(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	called := false
	m.SetExtraProviders(func() []*Command {
		called = true
		return nil
	})
	cmds := m.Commands()
	_ = cmds
	_ = called
}

func TestManager_SetEnabled(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	// Should not panic
	m.SetEnabled("nonexistent", true)
	m.SetEnabled("nonexistent", false)
}

func TestCommandSetSignature(t *testing.T) {
	cmds := map[string]*Command{
		"test": {Name: "test", Description: "desc"},
	}
	sig := commandSetSignature(cmds)
	if sig == "" {
		t.Error("expected non-empty signature")
	}
}

func TestCommandSetSignature_Empty(t *testing.T) {
	sig := commandSetSignature(nil)
	if sig != "" {
		t.Errorf("expected empty for nil, got %q", sig)
	}
}

func TestManager_RecordUsage(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.RecordUsage("test-skill")
}

func TestPowHalf(t *testing.T) {
	if powHalf(0) != 1.0 {
		t.Errorf("powHalf(0) = %f, want 1.0", powHalf(0))
	}
	if powHalf(1) != 0.5 {
		t.Errorf("powHalf(1) = %f, want 0.5", powHalf(1))
	}
	if powHalf(10) <= 0 {
		t.Errorf("powHalf(10) = %f, want positive", powHalf(10))
	}
}

func TestManager_UserSlashCommands(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	cmds := m.UserSlashCommands()
	if cmds == nil {
		t.Error("expected non-nil")
	}
}
