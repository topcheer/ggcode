package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// ============================================================
// MouseWheelMsg → main conversation viewport
// ============================================================

func TestMouseWheelUpScrollsMainViewport(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	content := strings.Repeat("line\n", 50)
	m.output.WriteString(content)
	m.syncConversationViewport()

	totalLines := m.viewport.TotalLineCount()
	visibleLines := m.viewport.VisibleLineCount()
	if totalLines <= visibleLines {
		t.Fatalf("need scrollable content, got total=%d visible=%d", totalLines, visibleLines)
	}

	m.viewport.GotoBottom()
	bottomOffset := m.viewport.YOffset()
	t.Logf("bottom: total=%d visible=%d YOffset=%d autoFollow=%v",
		totalLines, visibleLines, bottomOffset, m.viewport.AutoFollow())

	// MouseWheelMsg must match before MouseMsg in the type switch because
	// MouseWheelMsg implements the MouseMsg interface.
	model, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 10, Y: 10})
	m2 := model.(Model)

	got := m2.viewport.YOffset()
	want := bottomOffset - 3
	if got != want {
		t.Errorf("after MouseWheelUp: YOffset=%d, want %d", got, want)
	}
	if m2.viewport.AutoFollow() {
		t.Error("after MouseWheelUp: autoFollow should be false")
	}
}

func TestMouseWheelDownFromScrolledPosition(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	content := strings.Repeat("line\n", 50)
	m.output.WriteString(content)
	m.syncConversationViewport()

	m.viewport.GotoBottom()
	m.viewport.ScrollUp(10) // scroll up, autoFollow becomes false
	if m.viewport.AutoFollow() {
		t.Fatal("autoFollow should be false after ScrollUp")
	}

	offsetBeforeUpdate := m.viewport.YOffset()
	t.Logf("scrolled up by 10: YOffset=%d autoFollow=%v", offsetBeforeUpdate, m.viewport.AutoFollow())

	model, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 10, Y: 10})
	m2 := model.(Model)

	got := m2.viewport.YOffset()
	want := offsetBeforeUpdate + 3
	if got != want {
		t.Errorf("after MouseWheelDown: YOffset=%d, want %d (offsetBeforeUpdate=%d)", got, want, offsetBeforeUpdate)
	}
}

func TestMouseWheelDownAtBottomEnablesAutoFollow(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	content := strings.Repeat("line\n", 50)
	m.output.WriteString(content)
	m.syncConversationViewport()

	m.viewport.ScrollUp(5)
	if m.viewport.AutoFollow() {
		t.Fatal("expected autoFollow=false after ScrollUp")
	}

	for !m.viewport.AtBottom() {
		m.viewport.ScrollDown(3)
	}
	if !m.viewport.AtBottom() {
		t.Fatal("expected to be at bottom")
	}
	if !m.viewport.AutoFollow() {
		t.Error("expected autoFollow=true when scrolled to bottom via ScrollDown")
	}
}

func TestMouseWheelIgnoredDuringStartupGate(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	m.startedAt = time.Now() // activate startup gate

	content := strings.Repeat("line\n", 50)
	m.output.WriteString(content)
	m.syncConversationViewport()
	m.viewport.GotoBottom()
	initialY := m.viewport.YOffset()

	model, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 10, Y: 10})
	m2 := model.(Model)

	if m2.viewport.YOffset() != initialY {
		t.Errorf("mouse wheel should be suppressed during startup gate, YOffset changed from %d to %d", initialY, m2.viewport.YOffset())
	}
}

func TestMouseWheelOnEmptyOutputDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked: %v", r)
		}
	}()
	m := newTestModel()
	m.handleResize(120, 40)

	model, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 0, Y: 0})
	_ = model.(Model)

	model, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 0, Y: 0})
	_ = model.(Model)
}

// MouseWheelMsg must be dispatched before MouseMsg in the type switch,
// because MouseWheelMsg implements the MouseMsg interface. This test
// verifies that MouseWheelMsg reaches the scroll handler, not the click handler.
func TestMouseWheelMsgNotCaughtByMouseMsgCase(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	content := strings.Repeat("line\n", 50)
	m.output.WriteString(content)
	m.syncConversationViewport()
	m.viewport.GotoBottom()
	bottomOffset := m.viewport.YOffset()

	// If MouseWheelMsg falls through to MouseMsg case, it would be ignored
	// (no file browser/preview panel open) and YOffset would stay the same.
	model, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 10, Y: 10})
	m2 := model.(Model)

	if m2.viewport.YOffset() == bottomOffset {
		t.Error("MouseWheelUp had no effect — likely caught by MouseMsg case instead of MouseWheelMsg case")
	}
}

// ============================================================
// MouseMsg → file browser preview viewport (wheel via MouseClickMsg)
// ============================================================

func TestFileBrowserMouseWheelScrollsPreview(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	dir := t.TempDir()
	longFile := createTestFile(t, dir, "test.txt", strings.Repeat("line content\n", 50))

	m.toggleFileBrowser()
	if m.fileBrowser == nil {
		t.Skip("file browser not available")
	}
	m.fileBrowser.rootPath = dir
	m.fileBrowser.selectedPath = longFile
	m.syncFileBrowser(true)

	if m.fileBrowser.preview == nil {
		t.Fatal("expected preview panel")
	}
	m.fileBrowser.preview.viewport.GotoBottom()
	initialY := m.fileBrowser.preview.viewport.YOffset()
	if initialY == 0 {
		t.Skip("preview not scrollable with this content/size")
	}

	clickUp := tea.MouseClickMsg{Button: tea.MouseWheelUp, X: 10, Y: 10}
	model, _ := m.Update(clickUp)
	m2 := model.(Model)

	if m2.fileBrowser == nil || m2.fileBrowser.preview == nil {
		t.Fatal("file browser or preview lost after mouse event")
	}
	got := m2.fileBrowser.preview.viewport.YOffset()
	want := initialY - 3
	if got != want {
		t.Errorf("preview YOffset = %d, want %d (initial was %d)", got, want, initialY)
	}
}

func TestFileBrowserMouseWheelDownScrollsPreview(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	dir := t.TempDir()
	longFile := createTestFile(t, dir, "test.txt", strings.Repeat("line content\n", 50))

	m.toggleFileBrowser()
	if m.fileBrowser == nil {
		t.Skip("file browser not available")
	}
	m.fileBrowser.rootPath = dir
	m.fileBrowser.selectedPath = longFile
	m.syncFileBrowser(true)

	if m.fileBrowser.preview == nil {
		t.Fatal("expected preview panel")
	}
	m.fileBrowser.preview.viewport.ScrollUp(10)
	initialY := m.fileBrowser.preview.viewport.YOffset()

	clickDown := tea.MouseClickMsg{Button: tea.MouseWheelDown, X: 10, Y: 10}
	model, _ := m.Update(clickDown)
	m2 := model.(Model)

	got := m2.fileBrowser.preview.viewport.YOffset()
	want := initialY + 3
	if got != want {
		t.Errorf("preview YOffset = %d, want %d (initial was %d)", got, want, initialY)
	}
}

func TestFileBrowserMouseAltModifierIgnored(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	dir := t.TempDir()
	longFile := createTestFile(t, dir, "test.txt", strings.Repeat("line\n", 50))
	m.toggleFileBrowser()
	if m.fileBrowser == nil {
		t.Skip("file browser not available")
	}
	m.fileBrowser.rootPath = dir
	m.fileBrowser.selectedPath = longFile
	m.syncFileBrowser(true)

	if m.fileBrowser.preview == nil {
		t.Fatal("expected preview")
	}
	m.fileBrowser.preview.viewport.GotoBottom()
	initialY := m.fileBrowser.preview.viewport.YOffset()

	// Alt+wheel should be ignored by handleFileBrowserMouse.
	click := tea.MouseClickMsg{Button: tea.MouseWheelUp, X: 10, Y: 10, Mod: tea.ModAlt}
	model, _ := m.Update(click)
	m2 := model.(Model)

	if m2.fileBrowser != nil && m2.fileBrowser.preview != nil {
		if m2.fileBrowser.preview.viewport.YOffset() != initialY {
			t.Errorf("Alt+mouse should be ignored, but YOffset changed from %d to %d",
				initialY, m2.fileBrowser.preview.viewport.YOffset())
		}
	}
}

// ============================================================
// MouseMsg → standalone preview panel viewport
// ============================================================

func TestPreviewPanelMouseWheelScrollsViewport(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	dir := t.TempDir()
	longFile := createTestFile(t, dir, "preview.txt", strings.Repeat("content line here\n", 50))

	m.previewPanel = buildPreviewPanelStateForPath(longFile, 0)
	if m.previewPanel == nil {
		t.Fatal("expected preview panel to be created")
	}
	m.syncPreviewViewport(true)

	m.previewPanel.viewport.GotoBottom()
	initialY := m.previewPanel.viewport.YOffset()
	if initialY == 0 {
		t.Skip("preview not scrollable")
	}

	clickUp := tea.MouseClickMsg{Button: tea.MouseWheelUp, X: 10, Y: 10}
	model, _ := m.Update(clickUp)
	m2 := model.(Model)

	if m2.previewPanel == nil {
		t.Fatal("preview panel lost after mouse event")
	}
	got := m2.previewPanel.viewport.YOffset()
	want := initialY - 3
	if got != want {
		t.Errorf("preview YOffset = %d, want %d (initial was %d)", got, want, initialY)
	}
}

func TestPreviewPanelMouseWheelDown(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	dir := t.TempDir()
	longFile := createTestFile(t, dir, "preview.txt", strings.Repeat("content line here\n", 50))

	m.previewPanel = buildPreviewPanelStateForPath(longFile, 0)
	m.syncPreviewViewport(true)

	m.previewPanel.viewport.ScrollUp(10)
	initialY := m.previewPanel.viewport.YOffset()

	clickDown := tea.MouseClickMsg{Button: tea.MouseWheelDown, X: 10, Y: 10}
	model, _ := m.Update(clickDown)
	m2 := model.(Model)

	got := m2.previewPanel.viewport.YOffset()
	want := initialY + 3
	if got != want {
		t.Errorf("preview YOffset = %d, want %d (initial was %d)", got, want, initialY)
	}
}

// ============================================================
// MouseMsg: Alt modifier pass-through / startup gate
// ============================================================

func TestMouseMsgWithAltIsIgnored(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	click := tea.MouseClickMsg{Button: tea.MouseLeft, X: 10, Y: 10, Mod: tea.ModAlt}
	model, _ := m.Update(click)
	m2 := model.(Model)
	_ = m2.View() // should not panic
}

func TestMouseMsgDuringStartupGateIsDropped(t *testing.T) {
	m := newTestModel()
	m.startedAt = time.Now() // activate startup gate

	dir := t.TempDir()
	m.toggleFileBrowser()
	if m.fileBrowser == nil {
		t.Skip("file browser not available")
	}
	m.fileBrowser.rootPath = dir
	longFile := createTestFile(t, dir, "test.txt", strings.Repeat("line\n", 50))
	m.fileBrowser.selectedPath = longFile
	m.syncFileBrowser(true)

	if m.fileBrowser.preview == nil {
		t.Fatal("expected preview")
	}
	m.fileBrowser.preview.viewport.GotoBottom()
	initialY := m.fileBrowser.preview.viewport.YOffset()

	clickUp := tea.MouseClickMsg{Button: tea.MouseWheelUp, X: 10, Y: 10}
	model, _ := m.Update(clickUp)
	m2 := model.(Model)

	if m2.fileBrowser != nil && m2.fileBrowser.preview != nil {
		if m2.fileBrowser.preview.viewport.YOffset() != initialY {
			t.Errorf("mouse during startup gate should be dropped, YOffset changed from %d to %d",
				initialY, m2.fileBrowser.preview.viewport.YOffset())
		}
	}
}

// ============================================================
// View() configuration
// ============================================================

func TestViewSetsMouseModeCellMotion(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	v := m.View()
	if v.MouseMode != tea.MouseModeCellMotion {
		t.Errorf("View().MouseMode = %v, want MouseModeCellMotion", v.MouseMode)
	}
}

func TestViewSetsAltScreen(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	v := m.View()
	if !v.AltScreen {
		t.Error("View().AltScreen = false, want true")
	}
}

// ============================================================
// Edge cases
// ============================================================

func TestMouseMsgWithNoPanelsDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked: %v", r)
		}
	}()
	m := newTestModel()
	m.handleResize(120, 40)

	click := tea.MouseClickMsg{Button: tea.MouseLeft, X: 10, Y: 10}
	model, _ := m.Update(click)
	_ = model.(Model)
}

// ============================================================
// helpers
// ============================================================

func createTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := dir + "/" + name
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("create test file: %v", err)
	}
	return path
}
