package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/topcheer/ggcode/internal/permission"
)

func newTestModel() Model {
	m := NewModel(nil, permission.NewConfigPolicy(nil, nil))
	return m
}

// --- Viewport / Resize tests ---

func TestResizeUpdatesViewport(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	if m.viewport.height != 35 { // 40 - 5
		t.Errorf("expected viewport height 35, got %d", m.viewport.height)
	}
	if m.viewport.width != 120 {
		t.Errorf("expected viewport width 120, got %d", m.viewport.width)
	}
	if m.input.Width != 120 {
		t.Errorf("expected input width 120, got %d", m.input.Width)
	}
}

func TestResizeSmallWindow(t *testing.T) {
	m := newTestModel()
	m.handleResize(40, 5)
	// viewportHeight = 5 - 5 = 0, but minimum is 3
	if m.viewport.height != 3 {
		t.Errorf("expected viewport height 3 (min), got %d", m.viewport.height)
	}
}

func TestResizeTinyWindow(t *testing.T) {
	m := newTestModel()
	m.handleResize(10, 2)
	if m.viewport.height != 3 {
		t.Errorf("expected viewport height 3 (min), got %d", m.viewport.height)
	}
}

// --- Viewport scroll / auto-follow tests ---

func TestViewportAutoFollow(t *testing.T) {
	vp := NewViewportModel(80, 20)
	if !vp.AutoFollow() {
		t.Error("expected autoFollow to be true initially")
	}
}

func TestViewportSetSize(t *testing.T) {
	vp := NewViewportModel(80, 20)
	vp.SetSize(120, 30)
	if vp.width != 120 || vp.height != 30 {
		t.Errorf("expected 120x30, got %dx%d", vp.width, vp.height)
	}
}

func TestViewportScrollUp(t *testing.T) {
	vp := NewViewportModel(80, 20)
	// Set multi-line content
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = "line content here"
	}
	vp.SetContent(strings.Join(lines, "\n"))
	vp.ScrollUp(5)
	// After scrolling up, auto-follow should be disabled
	if vp.AutoFollow() {
		t.Error("expected autoFollow to be false after manual scroll up")
	}
}

func TestViewportScrollDown(t *testing.T) {
	vp := NewViewportModel(80, 20)
	vp.ScrollUp(10)
	vp.ScrollDown(5)
	// ScrollDown with remaining offset may or may not re-enable auto-follow
	// depending on whether it reached the bottom; just verify no panic
}

func TestViewportGotoBottom(t *testing.T) {
	vp := NewViewportModel(80, 20)
	vp.ScrollUp(10)
	if vp.AutoFollow() {
		t.Error("expected autoFollow false after scroll up")
	}
	vp.GotoBottom()
	if !vp.AutoFollow() {
		t.Error("expected autoFollow true after GotoBottom")
	}
}

func TestViewportLongContentScroll(t *testing.T) {
	vp := NewViewportModel(80, 5)
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "content line"
	}
	vp.SetContent(strings.Join(lines, "\n"))
	// Should auto-follow to bottom
	if !vp.AutoFollow() {
		t.Error("expected autoFollow after SetContent with long content")
	}
}

// --- Empty content layout ---

func TestEmptyContentInputAtBottom(t *testing.T) {
	m := newTestModel()
	m.handleResize(80, 24)
	view := m.View()
	// The input prompt "❯ " should appear in the view even with empty content
	if !strings.Contains(view, "❯") {
		t.Error("expected input prompt in view")
	}
}

func TestViewContainsInputPlaceholder(t *testing.T) {
	m := newTestModel()
	m.handleResize(80, 24)
	view := m.View()
	if !strings.Contains(view, "Type a message") {
		t.Error("expected input placeholder in view")
	}
}

// --- Status bar rendering ---

func TestStatusBarWithCostInfo(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.statusActivity = "Thinking..."
	m.statusTokens = 1500
	m.statusCost = 0.0042
	bar := m.renderStatusBar()
	if !strings.Contains(bar, "Thinking...") {
		t.Error("expected activity in status bar")
	}
}

func TestStatusBarWithToolInfo(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.statusActivity = "Executing"
	m.statusToolName = "read_file"
	m.statusToolCount = 3
	bar := m.renderStatusBar()
	if !strings.Contains(bar, "read_file") {
		t.Error("expected tool name in status bar")
	}
	if !strings.Contains(bar, "3") {
		t.Error("expected tool count in status bar")
	}
}

func TestStatusBarEmptyWhenNotLoading(t *testing.T) {
	m := newTestModel()
	m.loading = false
	bar := m.renderStatusBar()
	// Should return empty string when not loading
	if strings.TrimSpace(bar) != "" {
		t.Errorf("expected empty status bar when not loading, got: %q", bar)
	}
}

// --- AutoComplete rendering ---

func TestAutoCompleteSlashCommands(t *testing.T) {
	m := newTestModel()
	m.autoCompleteActive = true
	m.autoCompleteKind = "slash"
	m.autoCompleteItems = []string{"/help", "/exit", "/clear"}
	m.autoCompleteIndex = 0
	result := m.renderAutoComplete()
	if !strings.Contains(result, "/help") {
		t.Error("expected /help in autocomplete")
	}
	if !strings.Contains(result, "/exit") {
		t.Error("expected /exit in autocomplete")
	}
}

func TestAutoCompleteEmpty(t *testing.T) {
	m := newTestModel()
	m.autoCompleteActive = false
	result := m.renderAutoComplete()
	if result != "" {
		t.Errorf("expected empty autocomplete when inactive, got: %q", result)
	}
}

// --- Update message handling ---

func TestUpdateWindowSizeMsg(t *testing.T) {
	m := newTestModel()
	msg := tea.WindowSizeMsg{Width: 100, Height: 30}
	model, cmd := m.Update(msg)
	m2 := model.(Model)
	if m2.width != 100 {
		t.Errorf("expected width 100, got %d", m2.width)
	}
	if m2.height != 30 {
		t.Errorf("expected height 30, got %d", m2.height)
	}
	if cmd != nil {
		t.Error("expected nil cmd for WindowSizeMsg")
	}
}

func TestUpdateKeyMsgQuit(t *testing.T) {
	m := newTestModel()
	msg := tea.KeyMsg{Type: tea.KeyCtrlC}
	_, cmd := m.Update(msg)
	if cmd == nil {
		t.Error("expected tea.Quit cmd")
	}
}

func TestUpdateKeyMsgEnterEmpty(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("")
	msg := tea.KeyMsg{Type: tea.KeyEnter}
	model, cmd := m.Update(msg)
	m2 := model.(Model)
	if m2.quitting {
		t.Error("should not quit on empty enter")
	}
	if cmd != nil {
		t.Error("expected nil cmd for empty enter")
	}
}

// --- renderOutput ---

func TestRenderOutputEmpty(t *testing.T) {
	m := newTestModel()
	result := m.renderOutput()
	if result != "" {
		t.Errorf("expected empty output, got: %q", result)
	}
}

func TestRenderOutputWithContent(t *testing.T) {
	m := newTestModel()
	m.output.WriteString("Hello world\n")
	result := m.renderOutput()
	if !strings.Contains(result, "Hello world") {
		t.Error("expected content in renderOutput")
	}
}
