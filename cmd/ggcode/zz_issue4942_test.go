package main

import (
	"testing"
	"unicode/utf8"
)

// fakeByteSource feeds a fixed byte script (the "tty").
type fakeByteSource struct {
	script []byte
	pos    int
	reads  int // instrumentation: a spin would blow this up
}

func (f *fakeByteSource) readOne() (byte, error) {
	f.reads++
	if f.pos >= len(f.script) {
		return 0, errExhausted{}
	}
	b := f.script[f.pos]
	f.pos++
	return b, nil
}

type errExhausted struct{}

func (errExhausted) Error() string { return "script exhausted" }

// #4942 regression pin: draining an incomplete CJK leader must TERMINATE -
// the #4939 shape (leader pushed back into the outer loop whose refill is
// len(pending)==0) spun forever at 100% CPU. Here the drain consumes its
// continuation bytes directly, so a bounded script yields a bounded number
// of reads.
func TestDrainContinuation_TerminatesOnCompleteRune(t *testing.T) {
	const s = "语" // 3-byte CJK rune
	src := &fakeByteSource{script: []byte(s[1:])}
	r, size, consumed, rest, err := drainContinuation(src, nil, s[0])
	if err != nil {
		t.Fatalf("drain error: %v", err)
	}
	if r != '语' || size != 3 || consumed != 2 {
		t.Fatalf("drain = %q/%d/%d, want 语/3/2", r, size, consumed)
	}
	if src.reads != 2 {
		t.Fatalf("reads = %d, want exactly 2 - a spin would exceed", src.reads)
	}
	// The drain returns the UNCONSUMED tail; the caller slices consumed
	// off (mirroring the pre-fix loop shape). For a complete rune the tail
	// is exactly the continuation pair.
	if len(rest) != 2 {
		t.Fatalf("rest = %v, want the 2 continuation bytes (caller consumes)", rest)
	}
}

// #4940 regression pin: the leader must NEVER be silently dropped - the
// assembled rune reaches the editor whole.
func TestDrainContinuation_LeaderNeverDropped(t *testing.T) {
	const s = "漢" // another 3-byte rune
	src := &fakeByteSource{script: []byte(s[1:])}
	r, _, _, _, err := drainContinuation(src, nil, s[0])
	if err != nil {
		t.Fatalf("drain error: %v", err)
	}
	if r != '漢' {
		t.Fatalf("rune = %q, want 漢 (leader must survive)", r)
	}
}

// Garbage guard: a leader whose "continuations" never complete a valid
// rune must stop draining (malformed -> size 1 / consumed 0, the
// "drop just the leader" signal), bounded reads, never spin.
func TestDrainContinuation_GarbageGuardTerminates(t *testing.T) {
	// 0xE0 announces 2 continuation bytes; feed non-continuation bytes.
	src := &fakeByteSource{script: []byte{0x41, 0x41, 0x41, 0x41, 0x41, 0x41}}
	r, size, consumed, _, err := drainContinuation(src, nil, 0xE0)
	if err != nil {
		t.Fatalf("drain error: %v", err)
	}
	if r != utf8.RuneError || size != 1 || consumed != 0 {
		t.Fatalf("drain = %q/%d/%d, want RuneError/1/0 (drop just the leader)", r, size, consumed)
	}
	if src.reads > 3 {
		t.Fatalf("guard did not bound the drain: reads=%d", src.reads)
	}
}

// Partial-pending path: a continuation byte already in pending from the
// same read burst is consumed before hitting the source again.
func TestDrainContinuation_UsesPendingFirst(t *testing.T) {
	const s = "语"
	src := &fakeByteSource{script: []byte{s[2]}} // only the LAST byte
	r, size, _, _, err := drainContinuation(src, []byte{s[1]}, s[0])
	if err != nil {
		t.Fatalf("drain error: %v", err)
	}
	if r != '语' || size != 3 {
		t.Fatalf("drain = %q/%d, want 语/3", r, size)
	}
	if src.reads != 1 {
		t.Fatalf("reads = %d, want 1 (pending byte must be used first)", src.reads)
	}
}

// assembleRune sanity pin (pre-existing behavior, guards the seam).
func TestDrainContinuation_AssembleSanity(t *testing.T) {
	if _, size, _ := assembleRune(nil, 0xE8); size != 0 {
		t.Fatalf("incomplete leader size = %d, want 0", size)
	}
	if !utf8.ValidRune('语') {
		t.Fatal("fixture")
	}
}
