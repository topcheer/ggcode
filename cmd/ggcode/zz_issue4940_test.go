package main

import (
	"bytes"
	"io"
	"testing"
	"testing/iotest"
	"unicode/utf8"
)

// zz_issue4940_test.go pins the UTF-8 leader accumulation fix: the 1-byte
// Read refill loop used to discard a multi-byte leader because pending was
// always empty when assembly was attempted (#4940, regression from #4938).

// TestIssue4940ReadFullRuneCJK drives the assembly through a OneByteReader
// (mirroring the tty 1-byte refill semantics) for a 3-byte CJK rune.
func TestIssue4940ReadFullRuneCJK(t *testing.T) {
	rd := iotest.OneByteReader(bytes.NewReader([]byte{0xB8, 0xAD})) // continuations for 0xE4
	var pending []byte
	r, ok, err := readFullRune(rd, 0xE4, &pending)
	if err != nil || !ok {
		t.Fatalf("readFullRune = %q/%v/%v, want ok with no error", r, ok, err)
	}
	if r != '中' {
		t.Fatalf("readFullRune rune = %q, want 中", r)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %v, want fully drained", pending)
	}
}

// TestIssue4940ReadFullRuneEmoji covers the 4-byte sequence path.
func TestIssue4940ReadFullRuneEmoji(t *testing.T) {
	emo := "😀"
	rd := iotest.OneByteReader(bytes.NewReader([]byte(emo[1:])))
	var pending []byte
	r, ok, err := readFullRune(rd, emo[0], &pending)
	if err != nil || !ok || r != '😀' {
		t.Fatalf("readFullRune = %q/%v/%v, want 😀/ok/nil", r, ok, err)
	}
}

// TestIssue4940ReadFullRuneDrainsPendingFirst pins that bytes already in the
// pending slice (delivered by an earlier multi-byte read) are consumed
// before hitting the reader again.
func TestIssue4940ReadFullRuneDrainsPendingFirst(t *testing.T) {
	rd := iotest.OneByteReader(bytes.NewReader([]byte{0xAD}))
	pending := []byte{0xB8}
	r, ok, err := readFullRune(rd, 0xE4, &pending)
	if err != nil || !ok || r != '中' {
		t.Fatalf("readFullRune = %q/%v/%v, want 中/ok/nil", r, ok, err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %v, want empty", pending)
	}
}

// TestIssue4940ReadFullRuneMalformedDrops: a leader whose continuation
// bytes are not valid UTF-8 must be dropped silently (ok=false), not
// inserted as RuneError.
func TestIssue4940ReadFullRuneMalformedDrops(t *testing.T) {
	rd := iotest.OneByteReader(bytes.NewReader([]byte{0x0D, 0x8D})) // 0x0D is not a continuation byte
	var pending []byte
	_, ok, err := readFullRune(rd, 0xE4, &pending)
	if err != nil {
		t.Fatalf("readFullRune err = %v, want nil", err)
	}
	if ok {
		t.Fatalf("readFullRune ok=true for malformed sequence, want drop")
	}
}

// TestIssue4940ReadFullRuneStrayContinuation: a bare continuation byte with
// no leader must be dropped without touching the reader.
func TestIssue4940ReadFullRuneStrayContinuation(t *testing.T) {
	rd := iotest.OneByteReader(bytes.NewReader([]byte("unused")))
	pending := []byte("x")
	_, ok, err := readFullRune(rd, 0x80, &pending)
	if err != nil || ok {
		t.Fatalf("readFullRune = %v/%v for stray continuation, want drop", ok, err)
	}
	if string(pending) != "x" {
		t.Fatalf("pending mutated on stray drop: %q", pending)
	}
}

// TestIssue4940ReadFullRuneReaderError: read errors propagate (EOF mid-
// sequence must not loop forever or fabricate a rune).
func TestIssue4940ReadFullRuneReaderError(t *testing.T) {
	var pending []byte
	_, _, err := readFullRune(iotest.ErrReader(io.ErrUnexpectedEOF), 0xE4, &pending)
	if err == nil {
		t.Fatalf("readFullRune err = nil, want propagated reader error")
	}
}

// TestIssue4940AssembleRuneStillPinned: assembleRune keeps its historical
// contract (direct unit pins from #4938 stay valid after the utf8Need
// extraction).
func TestIssue4940AssembleRuneStillPinned(t *testing.T) {
	s := "语"
	r, size, consumed := assembleRune([]byte(s[1:]), s[0])
	if r != '语' || size != 3 || consumed != 2 {
		t.Fatalf("assembleRune = %q/%d/%d, want 语/3/2", r, size, consumed)
	}
	if _, size, _ := assembleRune([]byte{0x80}, 0xE8); size != 0 {
		t.Fatalf("assembleRune incomplete size = %d, want 0", size)
	}
	if r, _, _ := assembleRune(nil, 0x80); r != utf8.RuneError {
		t.Fatalf("assembleRune stray = %q, want RuneError", r)
	}
}
