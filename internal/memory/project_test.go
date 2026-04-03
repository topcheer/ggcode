package memory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProjectMemory(t *testing.T) {
	// Create temp dirs
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "home", ".ggcode")
	projectDir := filepath.Join(tmpDir, "project")
	subDir := filepath.Join(projectDir, "sub")

	for _, d := range []string{globalDir, projectDir, subDir} {
		os.MkdirAll(d, 0755)
	}

	// Write GGCODE.md files
	os.WriteFile(filepath.Join(globalDir, "GGCODE.md"), []byte("global instructions"), 0644)
	os.WriteFile(filepath.Join(projectDir, "GGCODE.md"), []byte("project instructions"), 0644)
	os.WriteFile(filepath.Join(subDir, "GGCODE.md"), []byte("sub instructions"), 0644)

	// Monkey-patch UserHomeDir
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", filepath.Join(tmpDir, "home"))

	content, files, err := LoadProjectMemory(subDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain all three
	if !contains(content, "global instructions") {
		t.Error("missing global instructions")
	}
	if !contains(content, "project instructions") {
		t.Error("missing project instructions")
	}
	if !contains(content, "sub instructions") {
		t.Error("missing sub instructions")
	}
	if len(files) != 3 {
		t.Errorf("expected 3 files, got %d", len(files))
	}

	// Restore
	_ = os.Setenv("HOME", origHome)
}

func TestLoadProjectMemory_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	// Set home to a dir without .ggcode
	t.Setenv("HOME", tmpDir)

	content, files, err := LoadProjectMemory(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "" {
		t.Errorf("expected empty content, got: %q", content)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
