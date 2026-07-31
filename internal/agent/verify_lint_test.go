package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestDetectLintCommand(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func(dir string)
		want      string
	}{
		{
			name:      "go project without makefile",
			setupFunc: func(d string) { writeTestFile(t, filepath.Join(d, "go.mod"), "module test\n") },
			want:      "go vet ./...",
		},
		{
			name:      "makefile with lint target",
			setupFunc: func(d string) { writeTestFile(t, filepath.Join(d, "Makefile"), "lint:\n\tgo vet ./...\n") },
			want:      "make lint",
		},
		{
			name: "makefile without lint target, with go.mod",
			setupFunc: func(d string) {
				writeTestFile(t, filepath.Join(d, "Makefile"), "build:\n\tgo build\n")
				writeTestFile(t, filepath.Join(d, "go.mod"), "module x\n")
			},
			want: "go vet ./...",
		},
		{
			name:      "rust project",
			setupFunc: func(d string) { writeTestFile(t, filepath.Join(d, "Cargo.toml"), "[package]\nname=\"x\"\n") },
			want:      "cargo clippy",
		},
		{
			name:      "no recognizable project",
			setupFunc: func(d string) {},
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setupFunc(dir)
			got := detectLintCommand(dir)
			if got != tt.want {
				t.Errorf("detectLintCommand() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractLintWarnings(t *testing.T) {
	tests := []struct {
		name  string
		input string
		wantN int
	}{
		{
			name:  "go vet output",
			input: "main.go:10:2: printf format problem\nmain.go:20:5: unreachable code\n",
			wantN: 2,
		},
		{
			name:  "empty output",
			input: "",
			wantN: 0,
		},
		{
			name:  "clippy warnings",
			input: "warning: unused variable: `x`\n  --> src/main.rs:10:5\nwarning: this loop can be written more concisely\n",
			wantN: 2,
		},
		{
			name:  "noise lines filtered",
			input: "checking package...\ncompiling...\nmain.go:5:2: unused import\nok done\n",
			wantN: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractLintWarnings(tt.input)
			if len(got) < tt.wantN {
				t.Errorf("extractLintWarnings() got %d warnings, want at least %d. Got: %v", len(got), tt.wantN, got)
			}
		})
	}
}

func TestLooksLikeLintWarning(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"main.go:10: error: undefined: foo", true},
		{"warning: unused variable", true},
		{"checking package...", false},
		{"OK", false},
		{"", false},
		{"config.go:42:5: SA4006: value never used", true},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			lower := strings.ToLower(tt.line)
			got := looksLikeLintWarning(lower, tt.line)
			if got != tt.want {
				t.Errorf("looksLikeLintWarning(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestLintCommandAvailable(t *testing.T) {
	if !lintCommandAvailable(t.TempDir(), "go vet ./...") {
		t.Error("expected go to be available")
	}
	if lintCommandAvailable(t.TempDir(), "nonexistent_linter_xyz ./...") {
		t.Error("expected nonexistent tool to not be available")
	}
}
