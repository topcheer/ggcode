package util

import "testing"

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
