package tui

import (
	"testing"

	"charm.land/bubbles/v2/textarea"
)

func TestDeleteFromCursorToEnd_StartOfLine(t *testing.T) {
	ta := textarea.New()
	ta.SetValue("hello world\nfoo bar")

	ta.CursorStart()

	deleteFromCursorToEnd(&ta)

	expected := "hello world\n"
	if ta.Value() != expected {
		t.Errorf("expected %q, got %q", expected, ta.Value())
	}

	if ta.Line() != 1 || ta.Column() != 0 {
		t.Errorf("cursor should be at (1,0), got (%d,%d)", ta.Line(), ta.Column())
	}
}

func TestDeleteFromLineStartToCursor_EndOfLine(t *testing.T) {
	ta := textarea.New()
	ta.SetValue("hello world\nfoo bar")

	ta.CursorEnd()

	deleteFromLineStartToCursor(&ta)

	expected := "hello world\n"
	if ta.Value() != expected {
		t.Errorf("expected %q, got %q", expected, ta.Value())
	}

	if ta.Line() != 1 || ta.Column() != 0 {
		t.Errorf("cursor should be at (1,0), got (%d,%d)", ta.Line(), ta.Column())
	}
}

func TestDeleteWordBeforeCursor(t *testing.T) {
	ta := textarea.New()
	ta.SetValue("hello world test")
	ta.CursorEnd()

	deleteWordBeforeCursor(&ta)

	expected := "hello world"
	if ta.Value() != expected {
		t.Errorf("expected %q, got %q", expected, ta.Value())
	}

	if ta.Line() != 0 || ta.Column() != 11 {
		t.Errorf("cursor should be at (0,11), got (%d,%d)", ta.Line(), ta.Column())
	}
}

func TestDeleteWordBeforeCursor_MultipleSpaces(t *testing.T) {
	ta := textarea.New()
	ta.SetValue("hello   world")
	ta.CursorEnd()

	deleteWordBeforeCursor(&ta)

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

	ta.CursorUp()

	deleteFromCursorToEnd(&ta)
	if ta.Value() != "hello\n\nworld" {
		t.Errorf("empty line should be unchanged, got %q", ta.Value())
	}

	if ta.Line() != 1 || ta.Column() != 0 {
		t.Errorf("cursor should be at (1,0), got (%d,%d)", ta.Line(), ta.Column())
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
