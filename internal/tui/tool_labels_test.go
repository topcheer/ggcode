package tui

import "testing"

func TestFirstLine(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello world", "hello world"},
		{"first line\nsecond line", "first line"},
		{"only one line", "only one line"},
		{"", ""},
		{"line1\nline2\nline3", "line1"},
	}
	for _, tt := range tests {
		got := firstLine(tt.input)
		if got != tt.want {
			t.Errorf("firstLine(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
