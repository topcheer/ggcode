package permission

import "testing"

func TestExtractCommandFromInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"not json", "hello world", ""},
		{"command key", `{"command":"git diff --stat"}`, "git diff --stat"},
		{"input key", `{"input":"ls -la"}`, "ls -la"},
		{"command takes priority over input", `{"command":"echo hi","input":"other"}`, "echo hi"},
		{"no relevant keys", `{"description":"test"}`, ""},
		{"command is not string", `{"command":123}`, ""},
		{"whitespace trimmed", `  {"command":"pwd"}  `, "pwd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractCommandFromInput(tt.input)
			if got != tt.want {
				t.Errorf("ExtractCommandFromInput(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
