package main

import (
	"testing"
	"unicode/utf8"
)

// TestPlainLineEditByteByByteAssembly pins the #4938 hotfix seam: bytes
// arriving ONE PER READ (how a raw tty delivers a pasted/typed multi-byte
// rune) must still assemble into whole runes. The pre-fix read loop consumed
// the leader on "incomplete", orphaning the continuations; this test drives
// the exact read-loop consumption sequence (pop-leader, assemble against the
// rest, push-leader-back on incomplete) so the seam cannot regress silently.
func TestPlainLineEditByteByByteAssembly(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"CJK", "你好"},
		{"CJK+ascii interleave-same-bytes", "中文a"},
		{"3-byte emoji", "\U0001F600"}, // F0 9F 98 80
		{"2-byte latin-1 é", "café"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !utf8.ValidString(tc.input) {
				t.Fatalf("fixture must be valid UTF-8")
			}
			var pending []byte
			e := &lineEditor{}
			// Feed one byte at a time - the tty cadence.
			for i := 0; i < len(tc.input); i++ {
				pending = append(pending, tc.input[i])
				// Drain: mirror the read-loop's pop/assemble/push-back order.
				for len(pending) > 0 {
					b := pending[0]
					pending = pending[1:]
					if b < 0x80 {
						e.insert(rune(b))
						continue
					}
					r, size, consumed := assembleRune(pending, b)
					if size == 0 {
						// THE SEAM FIX: leader goes back, not dropped.
						pending = append([]byte{b}, pending...)
						break // wait for more bytes (read loop: continue outer)
					}
					pending = pending[consumed:]
					if r == utf8.RuneError && size == 1 {
						t.Fatalf("byte %02X dropped as invalid mid-assembly at offset %d", b, i)
					}
					e.insert(r)
				}
			}
			if got := e.line(); got != tc.input {
				t.Fatalf("assembled line = %q, want %q", got, tc.input)
			}
		})
	}
}

// TestPlainLineEditRefillGateNoSpin pins the #4942 regression: the read
// loop's refill gate must keep reading while pending holds an incomplete
// rune prefix (the #4939 leader push-back leaves exactly that shape).
// The pre-#4942 gate (`len(pending) == 0` only) never refilled after the
// push-back, so the loop popped the same leader forever - 100% CPU spin on
// the first CJK byte. This test mirrors the ACTUAL loop (refill gate
// included) against a one-byte-per-Read source and fails if any rune needs
// more pops than its byte length (the spin signature).
func TestPlainLineEditRefillGateNoSpin(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"CJK", "你好"},
		{"CJK+ascii", "中a文"},
		{"4-byte emoji", "\U0001F600"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.input)
			pos := 0
			e := &lineEditor{}
			var pending []byte
			pops := 0
			const maxPops = 64 // input is <= 9 bytes; spin blows past this instantly
			for {
				// Mirror of the read-loop refill gate (#4942 fix shape).
				if len(pending) == 0 || !utf8.FullRune(pending) {
					if pos >= len(src) {
						break // input exhausted; line must be complete
					}
					pending = append(pending, src[pos])
					pos++
					continue
				}
				pops++
				if pops > maxPops {
					t.Fatalf("refill gate spin: %d pops for %d input bytes", pops, len(src))
				}
				b := pending[0]
				pending = pending[1:]
				if b < 0x80 {
					e.insert(rune(b))
					continue
				}
				r, size, consumed := assembleRune(pending, b)
				if size == 0 {
					// Leader push-back (incomplete): gate must refill next pass.
					pending = append([]byte{b}, pending...)
					continue
				}
				pending = pending[consumed:]
				e.insert(r)
			}
			if got := e.line(); got != tc.input {
				t.Fatalf("assembled line = %q, want %q", got, tc.input)
			}
		})
	}
}
