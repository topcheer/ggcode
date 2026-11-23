package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectBuildSystem(t *testing.T) {
	tmp := t.TempDir()

	if cmd := detectBuildSystem(tmp); cmd != "" {
		t.Errorf("expected empty build command for empty dir, got %q", cmd)
	}

	goDir := filepath.Join(tmp, "goproject")
	os.MkdirAll(goDir, 0755)
	os.WriteFile(filepath.Join(goDir, "go.mod"), []byte("module test\n"), 0644)
	if cmd := detectBuildSystem(goDir); cmd != "go build ./..." {
		t.Errorf("expected 'go build ./...', got %q", cmd)
	}

	mkDir := filepath.Join(tmp, "mkproject")
	os.MkdirAll(mkDir, 0755)
	os.WriteFile(filepath.Join(mkDir, "Makefile"), []byte("build:\n\techo hi\n"), 0644)
	if cmd := detectBuildSystem(mkDir); cmd != "make build" {
		t.Errorf("expected 'make build', got %q", cmd)
	}

	rustDir := filepath.Join(tmp, "rustproject")
	os.MkdirAll(rustDir, 0755)
	os.WriteFile(filepath.Join(rustDir, "Cargo.toml"), []byte("[package]\nname=\"t\"\n"), 0644)
	if cmd := detectBuildSystem(rustDir); cmd != "cargo build" {
		if commandAvailable("cargo") {
			t.Errorf("expected 'cargo build', got %q", cmd)
		}

	}

	nodeDir := filepath.Join(tmp, "nodeproject")
	os.MkdirAll(nodeDir, 0755)

	os.WriteFile(filepath.Join(nodeDir, "package.json"), []byte(`{"scripts":{"build":"tsc"}}`), 0644)
	if cmd := detectBuildSystem(nodeDir); cmd != "npm run build" {
		if commandAvailable("npm") {
			t.Errorf("expected 'npm run build', got %q", cmd)
		}

	}

	noScriptDir := filepath.Join(tmp, "noscriptproject")
	os.MkdirAll(noScriptDir, 0755)
	os.WriteFile(filepath.Join(noScriptDir, "package.json"), []byte("{}"), 0644)
	if cmd := detectBuildSystem(noScriptDir); cmd != "" {
		t.Errorf("expected no command for package.json without build script, got %q", cmd)
	}

	testOnlyDir := filepath.Join(tmp, "testonlyproject")
	os.MkdirAll(testOnlyDir, 0755)
	os.WriteFile(filepath.Join(testOnlyDir, "package.json"), []byte(`{"scripts":{"test":"jest"}}`), 0644)
	if cmd := detectBuildSystem(testOnlyDir); cmd != "npm test" {
		if commandAvailable("npm") {
			t.Errorf("expected 'npm test', got %q", cmd)
		}
	}

	if cmd := detectBuildSystem(""); cmd != "" {
		t.Errorf("expected empty for empty dir, got %q", cmd)
	}
}

func TestCommandAvailable(t *testing.T) {

	if !commandAvailable("go") {
		t.Error("expected 'go' to be available in PATH")
	}

	if commandAvailable("definitely_not_a_real_binary_xyz123") {
		t.Error("expected nonexistent binary to be unavailable")
	}
}

func TestVerifyCommandAvailable(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"echo hello", true},
		{"sh -c 'echo hi'", true},
		{"go build ./...", true},
		{"make build", true},
		{"", false},
		{"nonexistent_tool_xyz arg", false},
		{"bash /definitely/not/a/real/script_xyz123.sh", false},
		{"bash /etc/hosts", true},
		{"cd /app && go test ./...", true},
		{"cd /app && GOFLAGS=-p=1 go test ./...", true},
		{`GREETING="hello world" make test`, true},
		{"cd /app && nonexistent_tool_xyz arg", false},
	}
	for _, tt := range tests {
		got := verifyCommandAvailable(tt.cmd)
		if got != tt.want {
			t.Errorf("verifyCommandAvailable(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
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
