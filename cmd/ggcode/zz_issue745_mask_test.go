package main

import "testing"

func TestMaskSecretMultibyte(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"3 CJK runes (9 bytes, previously panicked)", "中中中", "****"},
		{"4 CJK runes (12 bytes, previously unmasked)", "密码令牌", "****"},
		{"7 CJK runes (21 bytes, fully masked under rune guard)", "密码测试一二三", "****"},
		{"ASCII short", "abcd", "****"},
		{"ASCII exactly 8", "abcdefgh", "****"},
		{"ASCII 12", "abcdefghijkl", "abcd********"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := maskSecret(tc.input); got != tc.want {
				t.Errorf("maskSecret(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
