package util

import (
	"testing"
	"unicode/utf8"
)

func TestSnapToRuneStart(t *testing.T) {
	tests := []struct {
		name string
		s    string
		i    int
		want int
	}{
		{"ascii", "hello world", 5, 5},
		{"zero", "hello", 0, 0},
		{"negative", "hello", -1, 0},
		{"past_end", "hello", 100, 5},
		// "你好" = 6 bytes: e4 bd a0 e5 a5 bd
		// Byte 1 (0xbd) is a continuation byte → snap back to 0
		{"cjk_mid_rune", "你好", 1, 0},
		// Byte 2 (0xa0) is a continuation byte → snap back to 0
		{"cjk_mid_rune_2", "你好", 2, 0},
		// Byte 3 (0xe5) is a rune start (好 begins) → stays at 3
		{"cjk_boundary", "你好", 3, 3},
		// Byte 4 (0xa5) is continuation → snap back to 3
		{"cjk_mid_rune_3", "你好", 4, 3},
		// Emoji: "🎉" = 4 bytes (f0 9f 8e 89)
		// Byte 1 (0x9f) is continuation → snap back to 0
		{"emoji_mid", "🎉", 1, 0},
		// Byte 4 is past first emoji, start of next char
		{"emoji_boundary", "🎉x", 4, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SnapToRuneStart(tt.s, tt.i)
			if got != tt.want {
				t.Errorf("SnapToRuneStart(%q, %d) = %d, want %d", tt.s, tt.i, got, tt.want)
			}
		})
	}
}

// TestSnapToRuneStartProducesValidUTF8 verifies that slicing at the snapped
// position always produces valid UTF-8 strings.
func TestSnapToRuneStartProducesValidUTF8(t *testing.T) {
	// Mix of ASCII, CJK, and emoji
	s := "Hello 世界 🎉🎉 ABCdef 你好"
	for i := 0; i <= len(s); i++ {
		snapped := SnapToRuneStart(s, i)
		head := s[:snapped]
		tail := s[snapped:]
		if !utf8.ValidString(head) {
			t.Errorf("byte %d: head slice invalid UTF-8: %q", i, head)
		}
		if !utf8.ValidString(tail) {
			t.Errorf("byte %d: tail slice invalid UTF-8: %q", i, tail)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxRunes int
		want     string
	}{
		{"short", "hello", 10, "hello"},
		{"exact", "hello", 5, "hello"},
		{"truncate_ascii", "hello world", 8, "hello..."},
		{"truncate_cjk", "你好世界世界", 4, "你..."},
		{"maxRunes_3", "hello", 3, "hel"},
		{"maxRunes_1", "hello", 1, "h"},
		{"empty", "", 10, ""},
		{"multi_byte", "日本語テスト", 4, "日..."},
		{"negative", "hello", -1, "hello"},
		{"zero", "hello", 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.input, tt.maxRunes)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tt.input, tt.maxRunes, got, tt.want)
			}
		})
	}
}
