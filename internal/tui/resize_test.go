package tui

import (
	"testing"

	"charm.land/bubbles/v2/textarea"
)

func TestComposerHeight(t *testing.T) {
	cases := []struct {
		value string
		want  int
	}{
		{"", 1},
		{"hello", 1},
		{"hello\nworld", 2},
		{"a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl", 10}, // capped at 10
		{"a\nb\nc\nd\ne\nf\ng\nh\ni\nj", 10},       // exactly 10
		{"a\nb\nc\nd\ne", 5},
	}
	for _, tc := range cases {
		got := composerHeight(tc.value)
		if got != tc.want {
			t.Errorf("composerHeight(%q) = %d, want %d", tc.value, got, tc.want)
		}
	}
}

func TestInputCursor(t *testing.T) {
	ta := textarea.New()
	ta.Focus()
	ta.SetHeight(5)
	ta.SetValue("hello world")
	// Cursor should be at end after SetValue
	got := inputCursor(&ta)
	if got != 11 {
		t.Errorf("inputCursor after SetValue(hello world) = %d, want 11", got)
	}

	ta.SetValue("line1\nline2\nline3")
	got = inputCursor(&ta)
	if got != 17 {
		t.Errorf("inputCursor after 3 lines = %d, want 17", got)
	}

	ta.SetValue("你好 @internal/")
	got = inputCursor(&ta)
	if got != len("你好 @internal/") {
		t.Errorf("inputCursor with multibyte chars = %d, want %d", got, len("你好 @internal/"))
	}

	ta.SetValue("first\n你好 /help")
	got = inputCursor(&ta)
	if got != len("first\n你好 /help") {
		t.Errorf("inputCursor with multibyte multiline input = %d, want %d", got, len("first\n你好 /help"))
	}

	ta.SetValue("")
	got = inputCursor(&ta)
	if got != 0 {
		t.Errorf("inputCursor on empty = %d, want 0", got)
	}
}

func TestComposerCursorEnd(t *testing.T) {
	ta := textarea.New()
	ta.Focus()
	ta.SetHeight(5)
	ta.SetValue("line1\nline2")
	composerCursorEnd(&ta)
	// After composerCursorEnd, cursor should be at the end
	got := inputCursor(&ta)
	if got != 11 {
		t.Errorf("cursor after composerCursorEnd = %d, want 11", got)
	}
}

func TestComposerCursorEndCJKMultiline(t *testing.T) {
	// CJK multiline: cursor must land at the byte end of the value and the
	// scroll must follow it (tail visible). Guards the composerCursorEnd
	// View()+MoveToEnd() rework - see cjk_repro_test.go for the full story.
	ta := textarea.New()
	ta.Focus()
	ta.SetHeight(5)
	val := "第一行\n第二行\n结尾"
	ta.SetValue(val)
	composerCursorEnd(&ta)
	if got := inputCursor(&ta); got != len(val) {
		t.Errorf("cursor after composerCursorEnd = %d, want %d", got, len(val))
	}
	if row := ta.Line(); row != ta.LineCount()-1 {
		t.Errorf("cursor row = %d, want last line %d", row, ta.LineCount()-1)
	}
}

// TestRelayoutAfterSidebarChangeNoPanic pins #1390: the sidebar-toggle
// relayout must run the full sync set (chatList caches items by width).
// chat.List's width is unexported, so the direct width assertion lives in
// code review; here we pin the observable contract - mainColumnWidth
// shifts with the toggle and relayout completes without panic on a model
// wired like production (chatList, questionnaire and stats present).
func TestRelayoutAfterSidebarChangeNoPanic(t *testing.T) {
	m := newTestModel()
	m.handleResize(140, 40)

	wide := m.mainColumnWidth()
	m.sidebarVisible = !m.sidebarVisible
	m.relayoutAfterSidebarChange()
	narrow := m.mainColumnWidth()

	if wide == narrow {
		t.Fatalf("mainColumnWidth should differ across sidebar states: %d == %d", wide, narrow)
	}
}
