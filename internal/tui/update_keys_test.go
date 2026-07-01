package tui

import (
	"testing"

	"charm.land/bubbles/v2/textarea"
)

func TestDeleteFromCursorToEnd_StartOfLine(t *testing.T) {
	ta := textarea.New()
	ta.SetValue("hello world\nfoo bar")
	// CursorStart sets col=0 on the current row (row=1 after SetValue)
	ta.CursorStart()

	deleteFromCursorToEnd(&ta)
	// Row 1 ("foo bar"), col 0 → delete entire line 1 content
	expected := "hello world\n"
	if ta.Value() != expected {
		t.Errorf("expected %q, got %q", expected, ta.Value())
	}
}

func TestDeleteFromLineStartToCursor_EndOfLine(t *testing.T) {
	ta := textarea.New()
	ta.SetValue("hello world\nfoo bar")
	// CursorEnd sets col to end of current row (row=1 after SetValue)
	ta.CursorEnd()

	deleteFromLineStartToCursor(&ta)
	// Row 1, col at end → delete entire line 1 content
	expected := "hello world\n"
	if ta.Value() != expected {
		t.Errorf("expected %q, got %q", expected, ta.Value())
	}
}

func TestDeleteWordBeforeCursor(t *testing.T) {
	ta := textarea.New()
	ta.SetValue("hello world test")
	ta.CursorEnd()

	deleteWordBeforeCursor(&ta)
	// Should delete " test" (whitespace + word)
	expected := "hello world"
	if ta.Value() != expected {
		t.Errorf("expected %q, got %q", expected, ta.Value())
	}
}

func TestDeleteWordBeforeCursor_MultipleSpaces(t *testing.T) {
	ta := textarea.New()
	ta.SetValue("hello   world")
	ta.CursorEnd()

	deleteWordBeforeCursor(&ta)
	// Should delete "   world" (all spaces + word, matching bash Ctrl+W)
	expected := "hello"
	if ta.Value() != expected {
		t.Errorf("expected %q, got %q", expected, ta.Value())
	}
}

func TestDeleteWordBeforeCursor_StartOfLine(t *testing.T) {
	ta := textarea.New()
	ta.SetValue("hello")
	ta.CursorStart()

	deleteWordBeforeCursor(&ta)
	if ta.Value() != "hello" {
		t.Errorf("expected 'hello', got %q", ta.Value())
	}
}

func TestDeleteFromCursorToEnd_EmptyLine(t *testing.T) {
	ta := textarea.New()
	ta.SetValue("hello\n\nworld")
	// Navigate to row 1 (the empty line)
	ta.CursorUp() // row 1 from row 2

	deleteFromCursorToEnd(&ta)
	if ta.Value() != "hello\n\nworld" {
		t.Errorf("empty line should be unchanged, got %q", ta.Value())
	}
}

func TestDeleteWordBeforeCursor_SingleWord(t *testing.T) {
	ta := textarea.New()
	ta.SetValue("hello")
	ta.CursorEnd()

	deleteWordBeforeCursor(&ta)
	if ta.Value() != "" {
		t.Errorf("expected empty string, got %q", ta.Value())
	}
}
