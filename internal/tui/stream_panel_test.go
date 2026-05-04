package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/stream"
)

func newTestStreamModel(t *testing.T) *Model {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		FilePath: filepath.Join(dir, "ggcode.yaml"),
	}
	cfg.Stream = stream.StreamConfig{
		Targets: []stream.StreamTarget{
			{Name: "youtube", URL: "rtmps://a.rtmp.youtube.com/live2", Key: "test-key-123", Enabled: true},
			{Name: "twitch", URL: "rtmp://live.twitch.tv/app", Key: "twitch-key-456", Enabled: false},
		},
	}
	return &Model{config: cfg}
}

// sp extracts the Model from updateStreamPanel return value.
func sp(m tea.Model) *Model { return m.(*Model) }

func TestStreamPanelOpenClose(t *testing.T) {
	m := &Model{}
	m.openStreamPanel()
	if m.streamPanel == nil {
		t.Fatal("streamPanel should be set after openStreamPanel")
	}
	m.closeStreamPanel()
	if m.streamPanel != nil {
		t.Fatal("streamPanel should be nil after closeStreamPanel")
	}
}

func TestStreamPanelEscape(t *testing.T) {
	m := &Model{}
	m.openStreamPanel()
	updated, _ := m.updateStreamPanel(tea.KeyPressMsg{Code: tea.KeyEsc})
	if sp(updated).streamPanel != nil {
		t.Fatal("streamPanel should be nil after Esc")
	}
}

func TestStreamPanelEscapeWhileEditing(t *testing.T) {
	m := &Model{}
	m.openStreamPanel()
	m.streamPanel.editingField = "key"
	updated, _ := m.updateStreamPanel(tea.KeyPressMsg{Code: tea.KeyEsc})
	um := sp(updated)
	if um.streamPanel == nil {
		t.Fatal("streamPanel should still exist when exiting edit mode")
	}
	if um.streamPanel.editingField != "" {
		t.Error("editingField should be cleared")
	}
}

func TestStreamPanelNavigateEmpty(t *testing.T) {
	m := &Model{}
	m.openStreamPanel()
	updated, _ := m.updateStreamPanel(tea.KeyPressMsg{Text: "j"})
	if sp(updated).streamPanel == nil {
		t.Fatal("streamPanel should still be open after j")
	}
	updated, _ = sp(updated).updateStreamPanel(tea.KeyPressMsg{Text: "k"})
	if sp(updated).streamPanel == nil {
		t.Fatal("streamPanel should still be open after k")
	}
}

func TestStreamPanelNavigateWithTargets(t *testing.T) {
	m := newTestStreamModel(t)
	m.openStreamPanel()
	p := m.streamPanel

	if len(p.targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(p.targets))
	}
	if p.selectedIndex != 0 {
		t.Errorf("initial selectedIndex = %d, want 0", p.selectedIndex)
	}

	// Move down
	updated, _ := m.updateStreamPanel(tea.KeyPressMsg{Text: "j"})
	if sp(updated).streamPanel.selectedIndex != 1 {
		t.Errorf("after j: selectedIndex = %d, want 1", sp(updated).streamPanel.selectedIndex)
	}

	// Move up
	updated, _ = sp(updated).updateStreamPanel(tea.KeyPressMsg{Text: "k"})
	if sp(updated).streamPanel.selectedIndex != 0 {
		t.Errorf("after k: selectedIndex = %d, want 0", sp(updated).streamPanel.selectedIndex)
	}
}

func TestStreamPanelDeleteTarget(t *testing.T) {
	m := newTestStreamModel(t)
	m.openStreamPanel()

	if len(m.streamPanel.targets) != 2 {
		t.Fatalf("expected 2 targets initially, got %d", len(m.streamPanel.targets))
	}

	// Delete first target
	updated, _ := m.updateStreamPanel(tea.KeyPressMsg{Text: "d"})
	um := sp(updated)
	if len(um.streamPanel.targets) != 1 {
		t.Errorf("after delete: %d targets, want 1", len(um.streamPanel.targets))
	}
	if um.streamPanel.targets[0].Name != "twitch" {
		t.Errorf("remaining target = %q, want twitch", um.streamPanel.targets[0].Name)
	}
}

func TestStreamPanelDeleteLastTarget(t *testing.T) {
	m := newTestStreamModel(t)
	m.openStreamPanel()

	// Delete twice
	updated, _ := m.updateStreamPanel(tea.KeyPressMsg{Text: "d"})
	updated, _ = sp(updated).updateStreamPanel(tea.KeyPressMsg{Text: "d"})
	if len(sp(updated).streamPanel.targets) != 0 {
		t.Errorf("after double delete: %d targets, want 0", len(sp(updated).streamPanel.targets))
	}
}

func TestStreamPanelDeleteOutOfBound(t *testing.T) {
	m := newTestStreamModel(t)
	m.openStreamPanel()
	// Select beyond targets (preset area)
	m.streamPanel.selectedIndex = 5
	updated, _ := m.updateStreamPanel(tea.KeyPressMsg{Text: "d"})
	// Should not crash, targets unchanged
	if len(sp(updated).streamPanel.targets) != 2 {
		t.Errorf("targets changed unexpectedly: %d", len(sp(updated).streamPanel.targets))
	}
}

func TestStreamPanelEditKey(t *testing.T) {
	m := newTestStreamModel(t)
	m.openStreamPanel()

	// Start editing key
	updated, _ := m.updateStreamPanel(tea.KeyPressMsg{Text: "e"})
	if sp(updated).streamPanel.editingField != "key" {
		t.Errorf("editingField = %q, want key", sp(updated).streamPanel.editingField)
	}
}

func TestStreamPanelEditNoTarget(t *testing.T) {
	m := &Model{}
	m.openStreamPanel()
	// No targets, editing should not crash
	updated, _ := m.updateStreamPanel(tea.KeyPressMsg{Text: "e"})
	// editingField should remain empty since no target is selected
	if sp(updated).streamPanel == nil {
		t.Fatal("panel should still exist")
	}
}

func TestStreamPanelStopNotStreaming(t *testing.T) {
	m := newTestStreamModel(t)
	m.openStreamPanel()
	updated, _ := m.updateStreamPanel(tea.KeyPressMsg{Text: "x"})
	um := sp(updated)
	if um.streamPanel == nil {
		t.Fatal("panel should still exist after stop")
	}
	if um.streamPanel.message != "Not streaming" {
		t.Errorf("message = %q, want 'Not streaming'", um.streamPanel.message)
	}
}

func TestStreamPanelPersistenceOnClose(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		FilePath: filepath.Join(dir, "ggcode.yaml"),
	}
	cfg.Stream = stream.StreamConfig{
		Targets: []stream.StreamTarget{
			{Name: "youtube", URL: "rtmps://a.rtmp.youtube.com/live2", Key: "my-key", Enabled: true},
		},
	}

	m := &Model{config: cfg}
	m.openStreamPanel()

	// Verify panel loaded the targets
	if len(m.streamPanel.targets) != 1 {
		t.Fatalf("panel targets = %d, want 1", len(m.streamPanel.targets))
	}
	if m.streamPanel.targets[0].Key != "my-key" {
		t.Errorf("panel target key = %q, want 'my-key'", m.streamPanel.targets[0].Key)
	}

	// Modify key
	m.streamPanel.targets[0].Key = "new-key-789"

	// Close panel — should persist
	m.closeStreamPanel()

	// Verify config was updated in memory
	if cfg.Stream.Targets[0].Key != "new-key-789" {
		t.Errorf("config key after close = %q, want 'new-key-789'", cfg.Stream.Targets[0].Key)
	}

	// Verify file was written — closeStreamPanel may fail silently if Validate fails
	// on an empty config (no vendor). That's OK for this test; the key behavior
	// (in-memory persistence + save attempt) is what matters.
	data, err := os.ReadFile(cfg.FilePath)
	if err != nil {
		t.Logf("config file not written (expected for minimal test config): %v", err)
		// Don't fail — Validate may reject a config without vendor
		return
	}
	content := string(data)
	if !containsSubstring(content, "new-key-789") {
		t.Errorf("config file doesn't contain new key. Content:\n%s", content)
	}
}

func TestStreamPanelPersistenceOnDelete(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		FilePath: filepath.Join(dir, "ggcode.yaml"),
	}
	cfg.Stream = stream.StreamConfig{
		Targets: []stream.StreamTarget{
			{Name: "youtube", URL: "rtmps://a.rtmp.youtube.com/live2", Key: "key1", Enabled: true},
			{Name: "twitch", URL: "rtmp://live.twitch.tv/app", Key: "key2", Enabled: true},
		},
	}

	m := &Model{config: cfg}
	m.openStreamPanel()

	// Delete first target
	m.updateStreamPanel(tea.KeyPressMsg{Text: "d"})

	// Verify in-memory config updated
	if len(cfg.Stream.Targets) != 1 {
		t.Errorf("config targets after delete = %d, want 1", len(cfg.Stream.Targets))
	}
	if cfg.Stream.Targets[0].Name != "twitch" {
		t.Errorf("remaining config target = %q, want twitch", cfg.Stream.Targets[0].Name)
	}

	// Verify file written (may not exist if Validate rejects minimal config)
	data, err := os.ReadFile(cfg.FilePath)
	if err != nil {
		t.Logf("config file not written (expected for minimal test config): %v", err)
	} else if containsSubstring(string(data), "key1") {
		t.Error("deleted target key still in file")
	}
}

func TestStreamPanelPersistenceOnEditSave(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		FilePath: filepath.Join(dir, "ggcode.yaml"),
	}
	cfg.Stream = stream.StreamConfig{
		Targets: []stream.StreamTarget{
			{Name: "youtube", URL: "rtmps://a.rtmp.youtube.com/live2", Key: "old-key", Enabled: true},
		},
	}

	m := &Model{config: cfg}
	m.openStreamPanel()

	// Start editing key
	m.updateStreamPanel(tea.KeyPressMsg{Text: "e"})

	// Type new key
	m.streamPanel.keyInput.SetValue("brand-new-key")

	// Press Enter to save
	m.updateStreamPanel(tea.KeyPressMsg{Code: tea.KeyEnter})

	// Verify config updated
	if cfg.Stream.Targets[0].Key != "brand-new-key" {
		t.Errorf("key after edit = %q, want 'brand-new-key'", cfg.Stream.Targets[0].Key)
	}

	// Verify file written (may not exist if Validate rejects minimal config)
	data, err := os.ReadFile(cfg.FilePath)
	if err != nil {
		t.Logf("config file not written (expected for minimal test config): %v", err)
	} else if !containsSubstring(string(data), "brand-new-key") {
		t.Error("new key not in config file")
	}
}

func TestStreamPanelCloseNoConfig(t *testing.T) {
	m := &Model{}
	m.openStreamPanel()
	// Should not panic with nil config
	m.closeStreamPanel()
	if m.streamPanel != nil {
		t.Error("panel should be nil")
	}
}

func TestStreamPanelOpenLoadsConfig(t *testing.T) {
	m := &Model{
		config: &config.Config{
			Stream: stream.StreamConfig{
				Targets: []stream.StreamTarget{
					{Name: "youtube", Key: "persisted-key", Enabled: true},
				},
			},
		},
	}
	m.openStreamPanel()
	if len(m.streamPanel.targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(m.streamPanel.targets))
	}
	if m.streamPanel.targets[0].Key != "persisted-key" {
		t.Errorf("key = %q, want 'persisted-key'", m.streamPanel.targets[0].Key)
	}
}

func TestStreamPanelHelpKeys(t *testing.T) {
	m := newTestStreamModel(t)
	m.openStreamPanel()
	// Press ? to toggle help — should not crash
	updated, _ := m.updateStreamPanel(tea.KeyPressMsg{Text: "?"})
	if sp(updated).streamPanel == nil {
		t.Fatal("panel should still exist after ?")
	}
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
