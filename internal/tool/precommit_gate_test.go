package tool

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPrecommitBuildCheck_Disabled(t *testing.T) {
	old := precommitGateEnabled
	precommitGateEnabled = false
	defer func() { precommitGateEnabled = old }()

	got := precommitBuildCheck(context.Background(), t.TempDir())
	if got != "" {
		t.Errorf("expected empty string when disabled, got %q", got)
	}
}

func TestPrecommitBuildCheck_EmptyDir(t *testing.T) {
	got := precommitBuildCheck(context.Background(), "")
	if got != "" {
		t.Errorf("expected empty string for empty dir, got %q", got)
	}
}

func TestPrecommitBuildCheck_NoBuildSystem(t *testing.T) {
	dir := t.TempDir()
	got := precommitBuildCheck(context.Background(), dir)
	if got != "" {
		t.Errorf("expected empty string for no build system, got %q", got)
	}
}

func TestPrecommitBuildCheck_GoPasses(t *testing.T) {
	dir := t.TempDir()
	// Write a minimal go.mod that doesn't depend on anything.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testpkg\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got := precommitBuildCheck(context.Background(), dir)
	if got != "" {
		t.Errorf("expected empty string for passing build, got %q", got)
	}
}

func TestPrecommitBuildCheck_GoFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testpkg\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Write a Go file with an obvious compile error.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {\n\tx = undefinedVar\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got := precommitBuildCheck(context.Background(), dir)
	if got == "" {
		t.Fatal("expected warning for failing build, got empty string")
	}
	if !contains(got, "FAILED") && !contains(got, "failed") {
		t.Errorf("expected failure indicator, got %q", got)
	}
}

func TestPrecommitBuildCheck_Makefile(t *testing.T) {
	dir := t.TempDir()
	// Create a Makefile with a build target that always fails.
	makefile := "build:\n\t@echo 'Makefile build error'\n\t@exit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0644); err != nil {
		t.Fatal(err)
	}

	got := precommitBuildCheck(context.Background(), dir)
	// The build target fails, but since the output goes to stdout (not stderr)
	// and doesn't match our error patterns, we get a generic warning.
	if got == "" {
		t.Fatal("expected warning for failing Makefile build")
	}
}

func TestDetectPrecommitBuildCommand(t *testing.T) {
	t.Run("empty dir", func(t *testing.T) {
		if got := detectPrecommitBuildCommand(""); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})

	t.Run("go project", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
		got := detectPrecommitBuildCommand(dir)
		if got != "go build ./..." {
			t.Errorf("expected 'go build ./...', got %q", got)
		}
	})

	t.Run("rust project", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"test\"\n"), 0644)
		got := detectPrecommitBuildCommand(dir)
		if got != "cargo check" {
			t.Errorf("expected 'cargo check', got %q", got)
		}
	})

	t.Run("makefile with build target", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build:\n\tgo build ./...\n"), 0644)
		got := detectPrecommitBuildCommand(dir)
		if got != "make build" {
			t.Errorf("expected 'make build', got %q", got)
		}
	})

	t.Run("makefile with verify-ci target", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "Makefile"), []byte("verify-ci:\n\tgo vet ./...\n"), 0644)
		got := detectPrecommitBuildCommand(dir)
		if got != "make verify-ci" {
			t.Errorf("expected 'make verify-ci', got %q", got)
		}
	})

	t.Run("no build system", func(t *testing.T) {
		dir := t.TempDir()
		if got := detectPrecommitBuildCommand(dir); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})
}

func TestExtractBuildErrors(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		wantMin int // minimum number of expected errors
	}{
		{
			name: "go errors",
			output: `main.go:5:2: undefined: x
main.go:6:3: cannot use foo (type int) as type string`,
			wantMin: 2,
		},
		{
			name:    "rust errors",
			output:  "error[E0425]: cannot find value x in this scope\n  --> src/main.rs:3:5\nerror[E0308]: mismatched types",
			wantMin: 2,
		},
		{
			name:    "no errors",
			output:  "Build successful\n",
			wantMin: 0,
		},
		{
			name:    "empty output",
			output:  "",
			wantMin: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := extractBuildErrors(tt.output)
			if len(errors) < tt.wantMin {
				t.Errorf("expected at least %d errors, got %d: %v", tt.wantMin, len(errors), errors)
			}
		})
	}
}

func TestIsBuildErrorLine(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"main.go:5:2: undefined: x", true},
		{"main.go:5:2: error: expected semicolon", true},
		{"error[E0425]: cannot find value `x`", true},
		{"error: mismatched types", true},
		{"src/main.c:10:5: error: use of undeclared identifier", true},
		{"app.ts(10,5): error TS2322: Type 'string' is not assignable", true},
		{"SyntaxError: invalid syntax", true},
		{"warning: unused variable", false},
		{"Building project...", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			if got := isBuildErrorLine(tt.line); got != tt.want {
				t.Errorf("isBuildErrorLine(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestSetPrecommitGateEnabled(t *testing.T) {
	old := precommitGateEnabled
	defer func() { precommitGateEnabled = old }()

	SetPrecommitGateEnabled(false)
	if precommitGateEnabled != false {
		t.Error("expected precommitGateEnabled to be false")
	}

	SetPrecommitGateEnabled(true)
	if precommitGateEnabled != true {
		t.Error("expected precommitGateEnabled to be true")
	}
}
