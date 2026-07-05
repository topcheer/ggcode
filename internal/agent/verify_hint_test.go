package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectBuildSystem(t *testing.T) {
	tmp := t.TempDir()

	// Empty dir → no build system.
	if cmd := detectBuildSystem(tmp); cmd != "" {
		t.Errorf("expected empty build command for empty dir, got %q", cmd)
	}

	// Go module.
	goDir := filepath.Join(tmp, "goproject")
	os.MkdirAll(goDir, 0755)
	os.WriteFile(filepath.Join(goDir, "go.mod"), []byte("module test\n"), 0644)
	if cmd := detectBuildSystem(goDir); cmd != "go build ./..." {
		t.Errorf("expected 'go build ./...', got %q", cmd)
	}

	// Makefile.
	mkDir := filepath.Join(tmp, "mkproject")
	os.MkdirAll(mkDir, 0755)
	os.WriteFile(filepath.Join(mkDir, "Makefile"), []byte("build:\n\techo hi\n"), 0644)
	if cmd := detectBuildSystem(mkDir); cmd != "make build" {
		t.Errorf("expected 'make build', got %q", cmd)
	}

	// Rust.
	rustDir := filepath.Join(tmp, "rustproject")
	os.MkdirAll(rustDir, 0755)
	os.WriteFile(filepath.Join(rustDir, "Cargo.toml"), []byte("[package]\nname=\"t\"\n"), 0644)
	if cmd := detectBuildSystem(rustDir); cmd != "cargo build" {
		t.Errorf("expected 'cargo build', got %q", cmd)
	}

	// Node.
	nodeDir := filepath.Join(tmp, "nodeproject")
	os.MkdirAll(nodeDir, 0755)
	os.WriteFile(filepath.Join(nodeDir, "package.json"), []byte("{}"), 0644)
	if cmd := detectBuildSystem(nodeDir); cmd != "npm run build" {
		t.Errorf("expected 'npm run build', got %q", cmd)
	}

	// Empty working dir.
	if cmd := detectBuildSystem(""); cmd != "" {
		t.Errorf("expected empty for empty dir, got %q", cmd)
	}
}

func TestIsSourceCodeFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/foo/bar/baz.go", true},
		{"main.py", true},
		{"index.tsx", true},
		{"App.swift", true},
		{"README.md", false},
		{"config.json", false},
		{"go.mod", false},
		{"script.sh", true},
		{"Dockerfile", false},
		{"Makefile", false},
		{"/abs/path/lib.rs", true},
		{"noext", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isSourceCodeFile(tt.path); got != tt.want {
				t.Errorf("isSourceCodeFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestExtractFilePathFromArgs(t *testing.T) {
	// edit_file uses "file_path".
	args, _ := json.Marshal(map[string]string{"file_path": "/foo/bar.go"})
	if got := extractFilePathFromArgs("edit_file", args); got != "/foo/bar.go" {
		t.Errorf("got %q", got)
	}

	// write_file uses "path".
	args, _ = json.Marshal(map[string]string{"path": "/baz/main.py"})
	if got := extractFilePathFromArgs("write_file", args); got != "/baz/main.py" {
		t.Errorf("got %q", got)
	}

	// multi_file_edit uses "files" array with "path".
	args, _ = json.Marshal(map[string]interface{}{
		"files": []map[string]string{{"path": "/multi.go"}},
	})
	if got := extractFilePathFromArgs("multi_file_edit", args); got != "/multi.go" {
		t.Errorf("got %q", got)
	}

	// Empty args.
	if got := extractFilePathFromArgs("edit_file", nil); got != "" {
		t.Errorf("expected empty, got %q", got)
	}

	// Invalid JSON.
	if got := extractFilePathFromArgs("edit_file", json.RawMessage(`{invalid`)); got != "" {
		t.Errorf("expected empty for invalid JSON, got %q", got)
	}
}

func TestPostEditVerifyHintCooldown(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module test\n"), 0644)

	a := &Agent{
		workingDir: tmp,
	}

	// First edit: no hint (counter=1, need 3).
	args, _ := json.Marshal(map[string]string{"file_path": "/foo.go"})
	hint := a.postEditVerifyHint("edit_file", args)
	if hint != "" {
		t.Errorf("expected no hint on 1st edit, got: %s", hint)
	}

	// Second edit: no hint (counter=2).
	hint = a.postEditVerifyHint("edit_file", args)
	if hint != "" {
		t.Errorf("expected no hint on 2nd edit, got: %s", hint)
	}

	// Third edit: hint fires (counter=3 = threshold).
	hint = a.postEditVerifyHint("edit_file", args)
	if hint == "" {
		t.Error("expected hint on 3rd edit")
	}
	if !strings.Contains(hint, "go build") {
		t.Errorf("hint should mention build command, got: %s", hint)
	}

	// Counter resets, so next two edits should be silent.
	hint = a.postEditVerifyHint("edit_file", args)
	if hint != "" {
		t.Errorf("expected no hint after reset, got: %s", hint)
	}
	hint = a.postEditVerifyHint("edit_file", args)
	if hint != "" {
		t.Errorf("expected no hint, got: %s", hint)
	}
	// Third edit again: hint fires.
	hint = a.postEditVerifyHint("edit_file", args)
	if hint == "" {
		t.Error("expected hint on 3rd edit after reset")
	}
}

func TestPostEditVerifyHintNonSourceSkipped(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module test\n"), 0644)

	a := &Agent{workingDir: tmp}

	// Editing a markdown file should not trigger.
	args, _ := json.Marshal(map[string]string{"file_path": "/README.md"})
	hint := a.postEditVerifyHint("edit_file", args)
	if hint != "" {
		t.Errorf("expected no hint for markdown, got: %s", hint)
	}
	if a.postEditVerify.sourceEditsSinceHint != 0 {
		t.Errorf("non-source edit should not increment counter, got %d", a.postEditVerify.sourceEditsSinceHint)
	}
}

func TestPostEditVerifyHintNonEditToolSkipped(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module test\n"), 0644)

	a := &Agent{workingDir: tmp}

	// read_file is not an editing tool.
	args, _ := json.Marshal(map[string]string{"path": "/foo.go"})
	hint := a.postEditVerifyHint("read_file", args)
	if hint != "" {
		t.Errorf("expected no hint for read_file, got: %s", hint)
	}
}

func TestPostEditVerifyHintNoBuildSystem(t *testing.T) {
	tmp := t.TempDir()

	a := &Agent{workingDir: tmp}

	args, _ := json.Marshal(map[string]string{"file_path": "/foo.go"})
	// Fire enough edits to trigger, but no build system → no hint.
	for i := 0; i < 5; i++ {
		hint := a.postEditVerifyHint("edit_file", args)
		if hint != "" {
			t.Errorf("expected no hint without build system, got: %s", hint)
		}
	}
}

func TestResetPostEditVerify(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module test\n"), 0644)

	a := &Agent{workingDir: tmp}

	// Do some edits to build up state.
	args, _ := json.Marshal(map[string]string{"file_path": "/foo.go"})
	a.postEditVerifyHint("edit_file", args)
	a.postEditVerifyHint("edit_file", args)

	// Reset.
	a.resetPostEditVerify()

	if a.postEditVerify.sourceEditsSinceHint != 0 {
		t.Errorf("expected counter reset to 0, got %d", a.postEditVerify.sourceEditsSinceHint)
	}
	if a.postEditVerify.buildCmdChecked {
		t.Error("expected buildCmdChecked=false after reset")
	}
}
