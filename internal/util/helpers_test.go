package util

import "testing"

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{"all empty", []string{"", "", ""}, ""},
		{"first non-empty", []string{"hello", "world"}, "hello"},
		{"first few empty then non-empty", []string{"", "", "found", "other"}, "found"},
		{"single non-empty", []string{"only"}, "only"},
		{"single empty", []string{""}, ""},
		{"no args", nil, ""},
		{"whitespace only skipped", []string{"   ", "\t", "\n"}, ""},
		{"mixed empty and whitespace", []string{"", "  ", "\t", "value"}, "value"},
		{"whitespace trimmed from result", []string{"  hello  ", "world"}, "hello"},
		{"whitespace surrounding non-empty", []string{"  ", "  yes  "}, "yes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FirstNonEmpty(tt.values...)
			if got != tt.want {
				t.Errorf("FirstNonEmpty(%v) = %q, want %q", tt.values, got, tt.want)
			}
		})
	}
}
