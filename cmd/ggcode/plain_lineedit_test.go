package main

import (
	"bytes"
	"testing"
)

// TestPlainLineEditCJKBackspace pins the #plain-cjk fix: backspace must
// remove one RUNE and erase its full display width (2 cells for CJK, 1 for
// ASCII), never one byte.
func TestPlainLineEditCJKBackspace(t *testing.T) {
	e := &lineEditor{}
	e.insert('中') // 3 bytes, 2 cells
	e.insert('文') // 3 bytes, 2 cells
	e.insert('a') // 1 byte, 1 cell
	if got := e.line(); got != "中文a" {
		t.Fatalf("line = %q, want %q", got, "中文a")
	}
	if e.cells != 5 {
		t.Fatalf("cells = %d, want 5 (2+2+1)", e.cells)
	}

	seq := e.backspace() // drop 'a'
	if !bytes.Equal(seq, []byte{'\b', ' ', '\b'}) {
		t.Fatalf("ascii backspace seq = %q, want \\b \\b space", seq)
	}
	if got := e.line(); got != "中文" {
		t.Fatalf("after 'a' backspace line = %q", got)
	}

	seq = e.backspace() // drop '文' -> must clear 2 cells
	if len(seq) != 6 {  // 2x(\b ' ') + 2x\b
		t.Fatalf("cjk backspace seq len = %d, want 6: %q", len(seq), seq)
	}
	want := []byte{'\b', ' ', '\b', ' ', '\b', '\b'}
	if !bytes.Equal(seq, want) {
		t.Fatalf("cjk backspace seq = %q, want %q", seq, want)
	}
	if got := e.line(); got != "中" {
		t.Fatalf("after '文' backspace line = %q, got %q", "中", got)
	}
	if e.cells != 2 {
		t.Fatalf("cells after one CJK left = %d, want 2", e.cells)
	}

	// Empty-buffer backspace MUST be a no-op (nil) - emitting an erase there
	// would eat the prompt.
	if seq := e.backspace(); seq == nil || e.cells != 0 {
		t.Fatal("expected nil backspace on empty buffer")
	}
}

// TestPlainLineEditClearLine pins Ctrl-U: full-width wipe including the
// prompt-side safety (cells only, never more).
func TestPlainLineEditClearLine(t *testing.T) {
	e := &lineEditor{}
	e.insert('你')
	e.insert('好')
	seq := e.clearLine()
	if len(seq) != 12 { // 4 cells x(\b ' ') + 4x\b
		t.Fatalf("clear seq len = %d, want 12", len(seq))
	}
	if e.cells != 0 || len(e.buf) != 0 {
		t.Fatalf("clearLine left cells=%d buf=%d", e.cells, len(e.buf))
	}
}

// TestPlainLineEditZeroWidth ensures combining marks / zero-width runes
// insert without consuming a display cell (their backspace erases nothing).
func TestPlainLineEditZeroWidth(t *testing.T) {
	e := &lineEditor{}
	e.insert('e')
	e.insert(0x0301) // combining acute accent: zero width
	if e.cells != 1 {
		t.Fatalf("cells = %d, want 1 (combining mark is free)", e.cells)
	}
	if seq := e.backspace(); len(seq) != 0 {
		t.Fatalf("zero-width backspace seq = %q, want empty", seq)
	}
	if got := e.line(); got != "e" {
		t.Fatalf("line = %q, want e", got)
	}
}
