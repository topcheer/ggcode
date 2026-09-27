package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/stream"
)

// #2800: Esc during a custom add must reset customMode and clear stale
// inputs. Otherwise a later `e` (edit key of an existing target) hits the
// custom-save branch: with stale name/url it appends a bogus target, with
// empty ones it reports "All fields required" and editing is dead.
func TestStreamPanelEscResetsCustomMode2800(t *testing.T) {
	m := newTestStreamModel(t)
	m.config.FirstRun = true // skip full vendor/endpoint validation on save
	m.openStreamPanel()
	p := m.streamPanel

	// Simulate a partially-filled custom add that the user aborts with Esc.
	p.customMode = true
	p.editingField = "name"
	p.nameInput.SetValue("mycustom")
	p.urlInput.SetValue("rtmp://example.com/live")

	updated, _ := m.updateStreamPanel(tea.KeyPressMsg{Text: "esc"})
	um := sp(updated)
	p = um.streamPanel
	if p.customMode {
		t.Error("customMode not reset after Esc")
	}
	if p.editingField != "" {
		t.Errorf("editingField = %q, want empty", p.editingField)
	}
	if got := p.nameInput.Value(); got != "" {
		t.Errorf("nameInput residual = %q, want empty", got)
	}
	if got := p.urlInput.Value(); got != "" {
		t.Errorf("urlInput residual = %q, want empty", got)
	}

	// Select an existing target and press `e`: plain key edit, not custom add.
	p.selectedIndex = 0
	updated, _ = um.updateStreamPanel(tea.KeyPressMsg{Text: "e"})
	um = sp(updated)
	p = um.streamPanel
	if p.editingField != "key" {
		t.Fatalf("editingField after e = %q, want key", p.editingField)
	}
	if p.customMode {
		t.Error("customMode reactivated by e")
	}

	// Save a new key: target updated in place, no bogus append, no
	// "All fields required".
	p.keyInput.SetValue("new-key-9")
	before := len(p.targets)
	updated, _ = um.updateStreamPanel(tea.KeyPressMsg{Text: "enter"})
	p = sp(updated).streamPanel
	if len(p.targets) != before {
		t.Errorf("targets grew from %d to %d - bogus custom append", before, len(p.targets))
	}
	if p.targets[p.selectedIndex].Key != "new-key-9" {
		t.Errorf("target key = %q, want new-key-9 (edit not saved)", p.targets[p.selectedIndex].Key)
	}
	if p.message != "Saved" {
		t.Errorf("message = %q, want Saved", p.message)
	}
}

// Regression companion: the custom-add flow itself must still work after the
// Esc-reset change (name → url → key → appended).
func TestStreamPanelCustomAddStillWorks2800(t *testing.T) {
	m := &Model{config: &config.Config{Stream: stream.StreamConfig{}}}
	m.openStreamPanel()
	p := m.streamPanel

	p.customMode = true
	p.editingField = "name"
	p.nameInput.SetValue("custom1")
	p.urlInput.SetValue("rtmp://c.example/live")

	// name → url transition
	updated, _ := m.updateStreamPanel(tea.KeyPressMsg{Text: "enter"})
	p = sp(updated).streamPanel
	if p.editingField != "url" {
		t.Fatalf("editingField after name-enter = %q, want url", p.editingField)
	}
	// url → key transition
	p.keyInput.SetValue("k")
	updated, _ = m.updateStreamPanel(tea.KeyPressMsg{Text: "enter"})
	p = sp(updated).streamPanel
	if p.editingField != "key" {
		t.Fatalf("editingField after url-enter = %q, want key", p.editingField)
	}
	// key → saved
	p.keyInput.SetValue("ckey")
	updated, _ = m.updateStreamPanel(tea.KeyPressMsg{Text: "enter"})
	p = sp(updated).streamPanel
	if len(p.targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(p.targets))
	}
	got := p.targets[0]
	if got.Name != "custom1" || got.URL != "rtmp://c.example/live" || got.Key != "ckey" {
		t.Errorf("appended target = %+v", got)
	}
	if !p.customMode == false || p.customMode {
		t.Error("customMode not cleared after successful custom add")
	}
}
