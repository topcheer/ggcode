package tui

import "testing"

func TestPlaceholderWithPasteShortcutHintFor(t *testing.T) {
	tests := []struct {
		name        string
		placeholder string
		lang        Language
		shortcut    string
		want        string
	}{
		{
			name:        "english",
			placeholder: "Enter your API key...",
			lang:        LangEnglish,
			shortcut:    "Ctrl+Shift+V",
			want:        "Enter your API key... (Paste: Ctrl+Shift+V)",
		},
		{
			name:        "chinese",
			placeholder: "可选补充说明",
			lang:        LangZhCN,
			shortcut:    "Cmd+V",
			want:        "可选补充说明（粘贴：Cmd+V）",
		},
		{
			name:        "empty placeholder stays empty",
			placeholder: "",
			lang:        LangEnglish,
			shortcut:    "Ctrl+V",
			want:        "",
		},
	}
	for _, tt := range tests {
		if got := placeholderWithPasteShortcutHintFor(tt.placeholder, tt.lang, tt.shortcut); got != tt.want {
			t.Fatalf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}
