package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestIMPanelOpenClose(t *testing.T) {
	m := Model{}
	m.openIMPanel()
	if m.imPanel == nil {
		t.Fatal("imPanel should be set after openIMPanel")
	}
	m.closeIMPanel()
	if m.imPanel != nil {
		t.Fatal("imPanel should be nil after closeIMPanel")
	}
}

func TestIMPanelEscape(t *testing.T) {
	m := Model{}
	m.openIMPanel()
	updated, _ := m.handleIMPanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if updated.imPanel != nil {
		t.Fatal("imPanel should be nil after Esc")
	}
}

func TestIMPanelNavigateEmpty(t *testing.T) {
	m := Model{}
	m.openIMPanel()

	// Navigate with no entries should not crash
	updated, _ := m.handleIMPanelKey(tea.KeyPressMsg{Text: "j"})
	if updated.imPanel == nil {
		t.Fatal("imPanel should still be open")
	}
	updated, _ = m.handleIMPanelKey(tea.KeyPressMsg{Text: "k"})
	if updated.imPanel == nil {
		t.Fatal("imPanel should still be open")
	}
}

func TestIMPanelDisableNoRuntime(t *testing.T) {
	m := Model{}
	m.openIMPanel()
	// Without imManager, disable should show error message
	updated, _ := m.handleIMPanelKey(tea.KeyPressMsg{Text: "d"})
	if updated.imPanel == nil {
		t.Fatal("imPanel should still be open")
	}
	// message should be set (no channels available)
	if updated.imPanel.message == "" {
		t.Fatal("message should be set when no channels")
	}
}

func TestIMPanelEnableNoRuntime(t *testing.T) {
	m := Model{}
	m.openIMPanel()
	updated, _ := m.handleIMPanelKey(tea.KeyPressMsg{Text: "e"})
	if updated.imPanel == nil {
		t.Fatal("imPanel should still be open")
	}
	if updated.imPanel.message == "" {
		t.Fatal("message should be set when no channels")
	}
}

func TestIMPanelMuteNoRuntime(t *testing.T) {
	m := Model{}
	m.openIMPanel()
	updated, _ := m.handleIMPanelKey(tea.KeyPressMsg{Text: "m"})
	if updated.imPanel == nil {
		t.Fatal("imPanel should still be open")
	}
	if updated.imPanel.message == "" {
		t.Fatal("message should be set when no channels")
	}
}

func TestIMPanelUnmuteNoRuntime(t *testing.T) {
	m := Model{}
	m.openIMPanel()
	updated, _ := m.handleIMPanelKey(tea.KeyPressMsg{Text: "u"})
	if updated.imPanel == nil {
		t.Fatal("imPanel should still be open")
	}
	if updated.imPanel.message == "" {
		t.Fatal("message should be set when no channels")
	}
}

func TestIMPanelMuteAllNoRuntime(t *testing.T) {
	m := Model{}
	m.openIMPanel()
	updated, cmd := m.handleIMPanelKey(tea.KeyPressMsg{Text: "M"})
	if updated.imPanel == nil {
		t.Fatal("imPanel should still be open")
	}
	// MuteAll returns a command even without runtime
	if cmd == nil {
		t.Fatal("expected a command for MuteAll")
	}
}

func TestIMPanelUnmuteAllNoRuntime(t *testing.T) {
	m := Model{}
	m.openIMPanel()
	updated, cmd := m.handleIMPanelKey(tea.KeyPressMsg{Text: "U"})
	if updated.imPanel == nil {
		t.Fatal("imPanel should still be open")
	}
	if cmd == nil {
		t.Fatal("expected a command for UnmuteAll")
	}
}

func TestClampIMSelection(t *testing.T) {
	tests := []struct {
		selected, total, want int
	}{
		{0, 0, 0},
		{-1, 5, 0},
		{3, 3, 2},
		{5, 3, 2},
		{1, 3, 1},
	}
	for _, tt := range tests {
		got := clampIMSelection(tt.selected, tt.total)
		if got != tt.want {
			t.Errorf("clampIMSelection(%d, %d) = %d, want %d", tt.selected, tt.total, got, tt.want)
		}
	}
}

func TestFirstNonEmptyIM(t *testing.T) {
	if got := firstNonEmptyIM("", "  ", "hello"); got != "hello" {
		t.Errorf("firstNonEmptyIM = %q, want %q", got, "hello")
	}
	if got := firstNonEmptyIM(""); got != "" {
		t.Errorf("firstNonEmptyIM = %q, want empty", got)
	}
	if got := firstNonEmptyIM("first", "second"); got != "first" {
		t.Errorf("firstNonEmptyIM = %q, want %q", got, "first")
	}
}
