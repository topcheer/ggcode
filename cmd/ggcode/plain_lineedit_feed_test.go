package main

import (
	"testing"
	"unicode/utf8"
)

// feedAssembler is the REAL control-flow function plainReadLine calls per
// byte; these tests drive it directly in the read loop's exact cadence (one
// byte per call), so an assembly regression here fails the same path the
// loop takes - no mirror tests (the #4939/#4942 lesson).

// TestFeedAssemblerByteByByte pins the #4938/#4942 fix end-state: bytes fed
// ONE PER CALL (tty cadence) must assemble into whole runes across every
// UTF-8 width class.
func TestFeedAssemblerByteByByte(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"CJK", "你好"},
		{"CJK+ascii", "中文a"},
		{"4-byte emoji", "\U0001F600"},
		{"2-byte latin-1", "café"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var acc []byte
			var got []rune
			for i := 0; i < len(tc.input); i++ {
				b := tc.input[i]
				if b < 0x80 {
					// Mirror the read loop: ASCII takes the fast path and
					// never enters the assembler.
					got = append(got, rune(b))
					continue
				}
				var r rune
				var emit bool
				acc, r, emit = feedAssembler(acc, b)
				if emit {
					got = append(got, r)
				}
			}
			if string(got) != tc.input {
				t.Fatalf("assembled %q, want %q", string(got), tc.input)
			}
		})
	}
}

// TestFeedAssemblerNoDeadlockAfterLeader is the #4942 regression pin: after
// a leader byte is buffered, the NEXT call must still make progress (the
// loop's refill guard stays reachable because partial state never parks on
// the pending buffer). If a future change reintroduces park-on-pending, this
// documents the invariant the read loop depends on: feedAssembler always
// returns without needing the caller to re-supply bytes it already consumed.
func TestFeedAssemblerNoDeadlockAfterLeader(t *testing.T) {
	acc, _, emit := feedAssembler(nil, 0xE4) // leader of 中
	if emit || len(acc) != 1 {
		t.Fatalf("leader: acc=%d emit=%v, want 1/false", len(acc), emit)
	}
	// Each continuation must be consumed in one call; completion at the last.
	steps := []byte{0xB8, 0xAD}
	for i, b := range steps {
		var r rune
		acc, r, emit = feedAssembler(acc, b)
		last := i == len(steps)-1
		if emit != last {
			t.Fatalf("step %d: emit=%v, want %v", i, emit, last)
		}
		if emit && r != '中' {
			t.Fatalf("completed rune = %q, want 中", r)
		}
	}
	if acc != nil {
		t.Fatalf("accumulator not reset after completion: %v", acc)
	}
}

// TestFeedAssemblerMalformed covers the drop paths: stray continuation,
// bad continuation mid-rune (truncation re-seeds on a fresh leader), and
// overlong input beyond 4 bytes.
func TestFeedAssemblerMalformed(t *testing.T) {
	// Stray continuation with no leader: dropped silently.
	if acc, _, emit := feedAssembler(nil, 0x80); acc != nil || emit {
		t.Fatalf("stray continuation: acc=%v emit=%v, want nil/false", acc, emit)
	}
	// Leader then a non-continuation byte: truncated sequence dropped, the
	// new byte re-seeds assembly when it is a valid leader.
	acc, _, emit := feedAssembler(nil, 0xE4)
	if emit {
		t.Fatal("leader emitted?")
	}
	acc, _, emit = feedAssembler(acc, 0xE5) // another leader, not 10xxxxxx
	if emit || len(acc) != 1 {
		t.Fatalf("truncation re-seed: acc=%v emit=%v, want len1/false", acc, emit)
	}
	// Overlong: F8 leader (5-byte form) is outside UTF-8 range - dropped.
	acc, _, _ = feedAssembler(nil, 0xF8)
	if acc != nil {
		t.Fatal("F8 leader should be dropped (out of UTF-8 range)")
	}
	// Valid 4-byte emoji completes exactly at 4 bytes.
	emoji := "\U0001F600"
	if !utf8.ValidString(emoji) {
		t.Fatal("fixture")
	}
	var a []byte
	emitted := rune(0)
	for i := 0; i < len(emoji); i++ {
		var r rune
		var e bool
		a, r, e = feedAssembler(a, emoji[i])
		if e {
			emitted = r
		}
	}
	if emitted != []rune(emoji)[0] {
		t.Fatalf("emoji = %q, want %q", emitted, emoji)
	}
}
