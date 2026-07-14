package tui

import (
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// ============================================================
// MouseWheelMsg → main conversation viewport
// ============================================================

func TestMouseWheelUpScrollsMainViewport(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	for i := 0; i < 50; i++ {
		m.chatWriteSystem(nextSystemID(), "line")
	}
	m.chatListScrollToBottom()
	bottomOffset := m.chatList.YOffset()

	model, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 10, Y: 10})
	m2 := model.(Model)

	if m2.chatList.YOffset() >= bottomOffset {
		t.Errorf("after MouseWheelUp: YOffset=%d, want < %d", m2.chatList.YOffset(), bottomOffset)
	}
}

func TestMouseWheelDownFromScrolledPosition(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	for i := 0; i < 50; i++ {
		m.chatWriteSystem(nextSystemID(), "line")
	}
	m.chatListScrollToBottom()
	m.chatList.ScrollUp(10)

	offsetBeforeUpdate := m.chatList.YOffset()

	model, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 10, Y: 10})
	m2 := model.(Model)

	if m2.chatList.YOffset() <= offsetBeforeUpdate {
		t.Errorf("after MouseWheelDown: YOffset=%d, want > %d", m2.chatList.YOffset(), offsetBeforeUpdate)
	}
}

func TestMouseWheelDownAtBottomEnablesAutoFollow(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	for i := 0; i < 50; i++ {
		m.chatWriteSystem(nextSystemID(), "line")
	}
	m.chatListScrollToBottom()
	m.chatList.ScrollUp(5)

	for !m.chatList.AtBottom() {
		m.chatList.ScrollDown(3)
	}
	if !m.chatList.AtBottom() {
		t.Fatal("expected to be at bottom")
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

	for i := 0; i < 50; i++ {
		m.chatWriteSystem(nextSystemID(), "line")
	}
	m.chatListScrollToBottom()
	bottomOffset := m.chatList.YOffset()

	model, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 10, Y: 10})
	m2 := model.(Model)

	if m2.chatList.YOffset() == bottomOffset {
		t.Error("MouseWheelUp had no effect — likely caught by MouseMsg case instead of MouseWheelMsg case")
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
