package tool

import (
	"strings"
	"testing"
)

func TestSuggestToolName_ExactMatch(t *testing.T) {
	reg := setupTestRegistry()
	// "read_file" should match "read_file" with distance 0.
	if got := SuggestToolName(reg, "read_file"); got != "read_file" {
		t.Errorf("exact match: want read_file, got %q", got)
	}
}

func TestSuggestToolName_MissingUnderscore(t *testing.T) {
	reg := setupTestRegistry()
	// "readfile" → "read_file" (distance 1)
	if got := SuggestToolName(reg, "readfile"); got != "read_file" {
		t.Errorf("readfile: want read_file, got %q", got)
	}
}

func TestSuggestToolName_Prefix(t *testing.T) {
	reg := setupTestRegistry()
	// "edit" is a prefix of "edit_file" — should match via prefix rule.
	if got := SuggestToolName(reg, "edit"); got != "edit_file" {
		t.Errorf("edit prefix: want edit_file, got %q", got)
	}
}

func TestSuggestToolName_Typo(t *testing.T) {
	reg := setupTestRegistry()
	// "replce" → "replace" not registered, but "grep" → "grepp" (distance 1)
	if got := SuggestToolName(reg, "grepp"); got != "grep" {
		t.Errorf("grepp typo: want grep, got %q", got)
	}
}

func TestSuggestToolName_NoMatch(t *testing.T) {
	reg := setupTestRegistry()
	// "zzzzzz" is too far from everything — should return "".
	if got := SuggestToolName(reg, "zzzzzzzzz"); got != "" {
		t.Errorf("expected no suggestion for unrelated name, got %q", got)
	}
}

func TestSuggestToolName_EmptyName(t *testing.T) {
	reg := setupTestRegistry()
	if got := SuggestToolName(reg, ""); got != "" {
		t.Errorf("expected empty for empty name, got %q", got)
	}
}

func TestSuggestToolName_EmptyRegistry(t *testing.T) {
	reg := NewRegistry()
	if got := SuggestToolName(reg, "read_file"); got != "" {
		t.Errorf("expected empty for empty registry, got %q", got)
	}
}

func TestFormatUnknownToolError_WithSuggestion(t *testing.T) {
	reg := setupTestRegistry()
	msg := FormatUnknownToolError(reg, "readfile")
	if !strings.Contains(msg, "read_file") {
		t.Errorf("expected suggestion in message, got: %s", msg)
	}
	if !strings.Contains(msg, "unknown tool") {
		t.Errorf("expected 'unknown tool' in message, got: %s", msg)
	}
}

func TestFormatUnknownToolError_NoSuggestion(t *testing.T) {
	reg := setupTestRegistry()
	msg := FormatUnknownToolError(reg, "zzzzzzzzz")
	if strings.Contains(msg, "Did you mean") {
		t.Errorf("expected no suggestion for unrelated name, got: %s", msg)
	}
}

func TestToolNameDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"abc", "ab", 1},
		{"abc", "abcd", 1},
		{"", "abc", 3},
		{"abc", "", 3},
		{"kitten", "sitting", 3},
		{"read_file", "readfile", 1},
	}
	for _, tt := range tests {
		got := toolNameDistance(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("distance(%q,%q): want %d, got %d", tt.a, tt.b, tt.want, got)
		}
	}
}

// setupTestRegistry creates a registry with a few common tools for testing.
func setupTestRegistry() *Registry {
	reg := NewRegistry()
	_ = reg.Register(ReadFile{})
	_ = reg.Register(EditFile{WorkingDir: "/tmp"})
	_ = reg.Register(Grep{})
	_ = reg.Register(Glob{})
	return reg
}
