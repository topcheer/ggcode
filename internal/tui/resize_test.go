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
