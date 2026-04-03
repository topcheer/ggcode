package context

import "testing"

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"empty", "", 1},
		{"ascii_short", "hello", 2},      // 5/4+1 = 2
		{"ascii_long", "hello world", 3}, // 11/4+1 = 3
		{"cjk_short", "你好", 2},          // (2*2)/4+1 = 2
		{"cjk_long", "你好世界", 3},       // (4*2)/4+1 = 3
		{"mixed", "hello你好", 3},         // (5+4)/4+1 = 3
		{"single_ascii", "a", 1},         // 1/4+1 = 1
		{"single_cjk", "中", 1},          // 2/4+1 = 1
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateTokens(tt.input)
			if got != tt.want {
				t.Errorf("EstimateTokens(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestEstimateTokens_CJKMoreThanASCII(t *testing.T) {
	ascii := EstimateTokens("abcdefgh")
	cjk := EstimateTokens("一二三四五六七八")
	if cjk <= ascii {
		t.Errorf("CJK tokens (%d) should be > ASCII tokens (%d)", cjk, ascii)
	}
}
