package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSuggestFilePath_Typo(t *testing.T) {
	dir := t.TempDir()
	// Create a file that the agent might misspell
	mustWriteFile(t, filepath.Join(dir, "agent_runtime.go"), "package agent")
	mustWriteFile(t, filepath.Join(dir, "overseer.go"), "package agent")

	// Typo: "agent_runtim.go" (missing 'e')
	suggestion := suggestFilePath(filepath.Join(dir, "agent_runtim.go"))
	if suggestion == "" {
		t.Fatal("expected suggestion for typo'd path, got empty")
	}
	if !strings.Contains(suggestion, "agent_runtime.go") {
		t.Errorf("suggestion should contain agent_runtime.go, got: %s", suggestion)
	}
}

func TestSuggestFilePath_WrongExtension(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "config.go"), "package config")

	// Wrong extension: "config.ts" instead of "config.go"
	suggestion := suggestFilePath(filepath.Join(dir, "config.ts"))
	if suggestion == "" {
		t.Fatal("expected suggestion for wrong extension, got empty")
	}
	if !strings.Contains(suggestion, "config.go") {
		t.Errorf("suggestion should contain config.go, got: %s", suggestion)
	}
}

func TestSuggestFilePath_PrefixMatch(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "checkpoint.go"), "package checkpoint")
	mustWriteFile(t, filepath.Join(dir, "checkpoint_test.go"), "package checkpoint")

	// Short name that's a prefix of existing files
	suggestion := suggestFilePath(filepath.Join(dir, "check.go"))
	if suggestion == "" {
		t.Fatal("expected suggestion for prefix match, got empty")
	}
}

func TestSuggestFilePath_NoMatch(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "completely_different.go"), "package main")

	// Completely unrelated path
	suggestion := suggestFilePath(filepath.Join(dir, "xyz123.go"))
	if suggestion != "" {
		t.Errorf("expected no suggestion for unrelated path, got: %s", suggestion)
	}
}

func TestSuggestFilePath_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	suggestion := suggestFilePath(filepath.Join(dir, "foo.go"))
	if suggestion != "" {
		t.Errorf("expected no suggestion in empty dir, got: %s", suggestion)
	}
}

func TestSuggestFilePath_SkipsNodeModules(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "target.go"), "package main")
	// Create node_modules with a matching file that should NOT be suggested
	nodeDir := filepath.Join(dir, "node_modules")
	os.MkdirAll(nodeDir, 0755)
	mustWriteFile(t, filepath.Join(nodeDir, "target.js"), "module.exports = {}")

	suggestion := suggestFilePath(filepath.Join(dir, "target.ts"))
	if suggestion == "" {
		t.Fatal("expected suggestion, got empty")
	}
	if strings.Contains(suggestion, "node_modules") {
		t.Errorf("should not suggest files from node_modules, got: %s", suggestion)
	}
}

func TestSuggestFilePath_Subdirectory(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "agent")
	os.MkdirAll(subDir, 0755)
	mustWriteFile(t, filepath.Join(subDir, "agent_runtime.go"), "package agent")

	// Try to read from the wrong parent directory
	suggestion := suggestFilePath(filepath.Join(dir, "agent_runtim.go"))
	if suggestion == "" {
		t.Fatal("expected suggestion from subdirectory, got empty")
	}
}

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"kitten", "sitting", 3},
		{"agent", "agemt", 1},
	}
	for _, tt := range tests {
		got := levenshtein(tt.a, tt.b)
		if got != tt.expected {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.expected)
		}
	}
}

func TestIsLikelyMatch(t *testing.T) {
	tests := []struct {
		stem1, stem2, full1, full2 string
		expected                   bool
	}{
		{"agent", "agent", "agent.go", "agent.ts", true},           // same stem
		{"check", "checkpoint", "check.go", "checkpoint.go", true}, // prefix
		{"agent", "overseer", "agent.go", "overseer.go", false},    // unrelated
	}
	for _, tt := range tests {
		got := isLikelyMatch(tt.stem1, tt.stem2, tt.full1, tt.full2)
		if got != tt.expected {
			t.Errorf("isLikelyMatch(%q, %q) = %v, want %v", tt.stem1, tt.stem2, got, tt.expected)
		}
	}
}

// --- helpers ---

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
