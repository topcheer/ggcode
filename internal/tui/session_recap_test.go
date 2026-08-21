package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

func TestSessionRecap(t *testing.T) {
	now := time.Now()

	t.Run("nil session returns empty", func(t *testing.T) {
		if got := sessionRecap(nil, now); got != "" {
			t.Fatalf("expected empty, got %q", got)
		}
	})

	t.Run("empty messages returns empty", func(t *testing.T) {
		ses := &session.Session{Messages: nil}
		if got := sessionRecap(ses, now); got != "" {
			t.Fatalf("expected empty, got %q", got)
		}
	})

	t.Run("no user messages returns empty", func(t *testing.T) {
		ses := &session.Session{
			Messages: []provider.Message{
				{Role: "assistant"},
			},
		}
		if got := sessionRecap(ses, now); got != "" {
			t.Fatalf("expected empty for assistant-only, got %q", got)
		}
	})

	t.Run("basic recap with user messages", func(t *testing.T) {
		ses := &session.Session{
			Title:     "Fix login bug",
			UpdatedAt: now.Add(-2 * time.Hour),
			Preview:   "Can you fix the login validation?",
			Messages: []provider.Message{
				{Role: "user"},
				{Role: "assistant"},
				{Role: "user"},
			},
			TokenUsage: provider.TokenUsage{
				InputTokens:  50000,
				OutputTokens: 3000,
			},
		}
		recap := sessionRecap(ses, now)
		if recap == "" {
			t.Fatal("expected non-empty recap")
		}
		if !contains(recap, "Fix login bug") {
			t.Errorf("recap should contain title, got: %s", recap)
		}
		if !contains(recap, "3 messages") {
			t.Errorf("recap should contain message count, got: %s", recap)
		}
		if !contains(recap, "2 user turns") {
			t.Errorf("recap should contain user turns, got: %s", recap)
		}
		if !contains(recap, "2h ago") {
			t.Errorf("recap should contain age, got: %s", recap)
		}
		if !contains(recap, "login validation") {
			t.Errorf("recap should contain preview, got: %s", recap)
		}
	})

	t.Run("files touched extracted from tool calls", func(t *testing.T) {
		ses := &session.Session{
			Title:     "Test",
			UpdatedAt: now,
			Messages: []provider.Message{
				{Role: "user"},
				{
					Role: "assistant",
					Content: []provider.ContentBlock{
						{
							Type:     "tool_use",
							ToolName: "edit_file",
							Input:    json.RawMessage(`{"file_path":"/src/main.go"}`),
						},
					},
				},
			},
		}
		recap := sessionRecap(ses, now)
		if recap == "" {
			t.Fatal("expected non-empty recap")
		}
		if !contains(recap, "main.go") || !contains(recap, "files touched") {
			t.Errorf("recap should mention files touched, got: %s", recap)
		}
	})

	t.Run("long preview truncated", func(t *testing.T) {
		longPreview := ""
		for i := 0; i < 300; i++ {
			longPreview += "x"
		}
		ses := &session.Session{
			Title:     "Test",
			UpdatedAt: now,
			Preview:   longPreview,
			Messages: []provider.Message{
				{Role: "user"},
			},
		}
		recap := sessionRecap(ses, now)
		if recap == "" {
			t.Fatal("expected non-empty recap")
		}
		if !contains(recap, "...") {
			t.Errorf("recap should truncate long preview, got: %s", recap[len(recap)-50:])
		}
	})

	// #913: byte-slicing at 120 split CJK runes mid-character (mojibake on
	// resume); truncation must land on rune boundaries.
	t.Run("cjk preview truncates on rune boundary", func(t *testing.T) {
		longPreview := strings.Repeat("好", 130) // 130 runes, 390 bytes
		ses := &session.Session{
			Title:     "Test",
			UpdatedAt: now,
			Preview:   longPreview,
			Messages: []provider.Message{
				{Role: "user"},
			},
		}
		recap := sessionRecap(ses, now)
		if recap == "" {
			t.Fatal("expected non-empty recap")
		}
		// Whatever got kept must be valid UTF-8 with no replacement chars.
		if strings.ContainsRune(recap, utf8.RuneError) {
			t.Errorf("cjk truncation produced invalid utf-8: %q", recap[len(recap)-60:])
		}
		if !contains(recap, "...") {
			t.Errorf("recap should truncate long cjk preview")
		}
	})
}

func TestFormatSessionAge(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		updated  time.Time
		expected string
	}{
		{"zero time", time.Time{}, "unknown"},
		{"just now", now.Add(-30 * time.Second), "just now"},
		{"minutes", now.Add(-15 * time.Minute), "15m ago"},
		{"hours", now.Add(-3 * time.Hour), "3h ago"},
		{"days", now.Add(-2 * 24 * time.Hour), "2d ago"},
		{"weeks", now.Add(-15 * 24 * time.Hour), "2 weeks ago"},
		{"far past", now.Add(-400 * 24 * time.Hour), now.Add(-400 * 24 * time.Hour).Format("Jan 2, 2006")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatSessionAge(tt.updated, now)
			if got != tt.expected {
				t.Errorf("formatSessionAge() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestFormatK(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{500, "500"},
		{1500, "1.5K"},
		{50000, "50.0K"},
		{1500000, "1.5M"},
	}
	for _, tt := range tests {
		got := formatK(tt.input)
		if got != tt.expected {
			t.Errorf("formatK(%d) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestLooksLikeFilePath(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"", false},
		{"http://example.com", false},
		{"https://example.com", false},
		{"*.go", false},
		{"/src/main.go", true},
		{"src/main.go", true},
		{"README.md", true},
		{"/absolute/path/to/file.ts", true},
	}
	for _, tt := range tests {
		got := looksLikeFilePath(tt.input)
		if got != tt.expected {
			t.Errorf("looksLikeFilePath(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}
