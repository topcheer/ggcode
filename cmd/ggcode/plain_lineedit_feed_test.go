package main

import (
	"testing"
	"unicode/utf8"
)

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

func TestFeedAssemblerNoDeadlockAfterLeader(t *testing.T) {
	acc, _, emit := feedAssembler(nil, 0xE4)
	if emit || len(acc) != 1 {
		t.Fatalf("leader: acc=%d emit=%v, want 1/false", len(acc), emit)
	}

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

func TestFeedAssemblerMalformed(t *testing.T) {

	if acc, _, emit := feedAssembler(nil, 0x80); acc != nil || emit {
		t.Fatalf("stray continuation: acc=%v emit=%v, want nil/false", acc, emit)
	}

	acc, _, emit := feedAssembler(nil, 0xE4)
	if emit {
		t.Fatal("leader emitted?")
	}
	acc, _, emit = feedAssembler(acc, 0xE5)
	if emit || len(acc) != 1 {
		t.Fatalf("truncation re-seed: acc=%v emit=%v, want len1/false", acc, emit)
	}

	acc, _, _ = feedAssembler(nil, 0xF8)
	if acc != nil {
		t.Fatal("F8 leader should be dropped (out of UTF-8 range)")
	}

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
