package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsHomeDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot resolve home directory")
	}

	if !isHomeDir(home) {
		t.Errorf("isHomeDir(%q) = false, want true", home)
	}
	if !isHomeDir(home + string(filepath.Separator)) {
		t.Errorf("isHomeDir with trailing separator should still match home")
	}
	if isHomeDir(filepath.Join(home, "projects")) {
		t.Errorf("isHomeDir(subdirectory of home) = true, want false")
	}
	if isHomeDir(filepath.Dir(home)) {
		t.Errorf("isHomeDir(parent of home) = true, want false")
	}
	if isHomeDir("") {
		t.Errorf("isHomeDir(empty) = true, want false")
	}
}

func TestDirHasProjectFiles(t *testing.T) {
	// Directory with only hidden entries -> not a project.
	hiddenOnly := t.TempDir()
	if err := os.WriteFile(filepath.Join(hiddenOnly, ".DS_Store"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if dirHasProjectFiles(hiddenOnly) {
		t.Errorf("dirHasProjectFiles(hidden-only dir) = true, want false")
	}

	// Directory with a real (non-hidden) file -> project.
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "main.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !dirHasProjectFiles(project) {
		t.Errorf("dirHasProjectFiles(project dir) = false, want true")
	}

	// Nonexistent directory -> false.
	if dirHasProjectFiles(filepath.Join(t.TempDir(), "does-not-exist")) {
		t.Errorf("dirHasProjectFiles(nonexistent dir) = true, want false")
	}
}
