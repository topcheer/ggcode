package agentruntime

import "testing"

func TestMaskPlaintextMultibyte(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"3 CJK runes (previously panicked)", "中中中", "****"},
		{"8 CJK runes (previously unmasked)", "一二三四五六七八", "****"},
		{"9 CJK runes (partial mask)", "一二三四五六七八九", "一二三四*六七八九"},
		{"ASCII exactly 8", "abcdefgh", "****"},
		{"ASCII 12", "abcdefghijkl", "abcd****ijkl"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := maskPlaintext(tc.input); got != tc.want {
				t.Errorf("maskPlaintext(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
