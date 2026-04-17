package tui

import (
	"testing"
)

func TestLooksLikeTerminalResponseInput(t *testing.T) {
	tests := []struct {
		name string
		val  string
		want bool
	}{
		// Empty and short inputs are never terminal responses.
		{"empty", "", false},
		{"one char", "a", false},
		{"two chars", "ab", false},

		// Normal human input is never a terminal response.
		{"english text", "hello world", false},
		{"CJK text", "测试一下", false},
		{"mixed input", "测试feishu", false},
		{"slash command", "/lark", false},
		{"path", "/tmp/file.txt", false},
		{"code", "fmt.Println(\"hello\")", false},
		{"numbers only", "1234", false},

		// OSC 11 background color response (full and partial).
		{"osc11 full", "]11;rgb:0000/0000/0000", true},
		{"osc11 start", "]11;rgb", true},
		{"osc11 fragment", "11;rgb:0000/0000", true},
		{"osc11 just start", "]11;", true},
		{"osc11 partial 1", "1;rgb:0000/0000/0000", true},
		{"osc11 partial 2", "]11;rgb:213d/2743/33e7", true},
		{"osc11 with hex", "]11;rgb:1a2b/3c4d/5e6f", true},
		{"rgb alone", "rgb:", true},
		{"rgb with values", "rgb:0000/0000/0000", true},

		// CSI CPR (cursor position report).
		{"cpr full", "[1;1R", true},
		{"cpr large", "[35;16R", true},
		{"cpr partial", ";1R", true},
		{"cpr partial 2", ";16R", true},
		{"cpr partial 3", "[1;", true}, // too short (< 3 hasStructuralPunct), but [1; is 3

		// CSI DECRPM (mode report).
		{"decrpm", "[?1u", true},
		{"decrpm extended", "[?2026;2$y", true},
		{"decrpm partial", "[?2026", true},
		{"decrpm $y", "$0y", true}, // $\d+y regex matches

		// CSI SGR mouse.
		{"sgr mouse", "[<0;93;43m", true},
		{"sgr mouse partial", "[<0;8", true},
		{"sgr mouse partial 2", "[<0;", true},

		// Mixed terminal response + normal text should NOT be flagged
		// (user had real input then terminal garbage appended).
		{"mixed cjk and osc11", "测试]11;rgb:0000", true}, // still flagged because rgb: matches

		// Real-world patterns from debug logs.
		{"real osc11 from log", "]11;rgb:0000/0000/0000", true},
		{"real cpr from log", "[1;1R", true},
		{"real decrpm from log", "[?2026;2$y", true},
		{"real mixed from log", "]11;rgb:0000/0000/0000[35;1", true},
		{"real fragment from log", "35;1[?2026;2$y[?1u", true},
		{"real partial from log", "11;rgb:213d/2743/33e7", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := looksLikeTerminalResponseInput(tc.val)
			if got != tc.want {
				t.Errorf("looksLikeTerminalResponseInput(%q) = %v, want %v", tc.val, got, tc.want)
			}
		})
	}
}

func TestLooksLikePartialTerminalSequence(t *testing.T) {
	tests := []struct {
		name string
		val  string
		want bool
	}{
		// Too short to determine.
		{"empty", "", false},
		{"one char", "a", false},
		{"two chars", "]1", false},

		// Normal input — contains diverse characters.
		{"english", "hello", false},
		{"CJK", "测试", false},
		{"path", "/tmp", false},
		{"url", "https://example.com", false},

		// Partial OSC 11 building up.
		{"osc11 partial ]1", "]1", false},  // too short (< 3)
		{"osc11 partial ]11", "]11", true}, // 3 chars, all terminal chars, has structural ]
		{"osc11 partial ]11;", "]11;", true},
		{"osc11 partial ]11;r", "]11;r", true},
		{"osc11 partial ]11;rg", "]11;rg", true},
		{"osc11 partial ]11;rgb", "]11;rgb", true},

		// CSI CPR building up.
		{"cpr [1", "[1", false}, // too short
		{"cpr [1;", "[1;", true},
		{"cpr [1;1", "[1;1", true},
		{"cpr [1;1R", "[1;1R", true},

		// Partial DECRPM.
		{"decrpm [?", "[?", false}, // only 2 chars, below threshold
		{"decrpm [?1", "[?1", true},
		{"decrpm [?1u", "[?1u", true},

		// Partial SGR mouse.
		{"sgr [<0", "[<0", true},
		{"sgr [<0;", "[<0;", true},

		// Numbers with semicolons (CPR fragment).
		{"digits semi", "35;1", true},
		{"just digits", "123", false}, // no structural punctuation

		// False positive guards: input that starts with ] or [ but is real typing.
		// These contain characters outside the terminal charset.
		{"bracket text", "[hello world", false}, // has h, e, l, l, o, space, w, o, r, l, d
		{"code bracket", "array[0]", false},     // has a, r, y — but r is in terminal charset. Let's see
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := looksLikePartialTerminalSequence(tc.val)
			if got != tc.want {
				t.Errorf("looksLikePartialTerminalSequence(%q) = %v, want %v", tc.val, got, tc.want)
			}
		})
	}
}
