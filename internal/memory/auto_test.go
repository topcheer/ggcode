package memory

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestAutoMemory_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	am := &AutoMemory{dir: tmpDir}

	err := am.SaveMemory("test-key", "test content")
	if err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(filepath.Join(tmpDir, "test-key.md")); err != nil {
		t.Fatal("file not created")
	}

	// Load
	content, files, err := am.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(files) != 1 {
		t.Errorf("expected 1 file, got %d", len(files))
	}
	if content == "" {
		t.Error("expected non-empty content")
	}
}

func TestAutoMemory_ListAndClear(t *testing.T) {
	tmpDir := t.TempDir()
	am := &AutoMemory{dir: tmpDir}

	am.SaveMemory("alpha", "a")
	am.SaveMemory("beta", "b")

	keys, err := am.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	sort.Strings(keys)
	if len(keys) != 2 || keys[0] != "alpha" || keys[1] != "beta" {
		t.Errorf("unexpected keys: %v", keys)
	}

	am.Clear()
	keys, err = am.List()
	if err != nil {
		t.Fatalf("List after clear: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected 0 keys after clear, got %d", len(keys))
	}
}

func TestSanitizeKey(t *testing.T) {
	tests := []struct{ in, want string }{
		{"hello", "hello"},
		{"Hello World!", "Hello-World"},
		{"", ""},
		{"a--b", "a-b"},
	}
	for _, tc := range tests {
		got := sanitizeKey(tc.in)
		if got != tc.want {
			t.Errorf("sanitizeKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
