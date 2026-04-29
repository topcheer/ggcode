package lsp

import (
	"strings"
	"testing"
)

// --- discovery.go pure functions ---

func TestEscapePowerShellSingleQuoted(t *testing.T) {
	got := escapePowerShellSingleQuoted("it's a test")
	if got != "it''s a test" {
		t.Errorf("expected \"it''s a test\", got %q", got)
	}
	if escapePowerShellSingleQuoted("no quotes") != "no quotes" {
		t.Error("expected passthrough")
	}
}

func TestLaunchArgs(t *testing.T) {
	tests := []struct {
		specID  string
		binary  string
		wantLen int
	}{
		{"go", "gopls", 0},
		{"python", "pyright-langserver", 1},
		{"typescript", "typescript-language-server", 1},
		{"yaml", "yaml-language-server", 1},
		{"json", "vscode-json-language-server", 1},
		{"dockerfile", "docker-langserver", 1},
		{"shell", "bash-language-server", 1},
		{"terraform", "terraform-ls", 1},
	}
	for _, tt := range tests {
		args := launchArgs(tt.specID, tt.binary, "")
		if len(args) != tt.wantLen {
			t.Errorf("launchArgs(%q,%q) = %v (len %d, want %d)", tt.specID, tt.binary, args, len(args), tt.wantLen)
		}
	}
}

func TestBinaryBaseName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/usr/bin/gopls", "gopls"},
		{"C:\\path\\to\\pyright-langserver.cmd", "c:\\path\\to\\pyright-langserver"},
		{"/usr/bin/typescript-language-server.exe", "typescript-language-server"},
		{"  /path/to/server  ", "server"},
	}
	for _, tt := range tests {
		got := binaryBaseName(tt.input)
		if got != tt.expected {
			t.Errorf("binaryBaseName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFirstNonEmpty(t *testing.T) {
	got := firstNonEmpty("", "  ", "hello", "world")
	if got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
	if firstNonEmpty("", "  ") != "" {
		t.Error("expected empty when all empty")
	}
}

func TestVenvBinDir(t *testing.T) {
	dir := venvBinDir()
	if dir == "" {
		t.Error("expected non-empty")
	}
}

func TestExecutableName(t *testing.T) {
	name := executableName("gopls")
	if name == "" {
		t.Error("expected non-empty")
	}
}

func TestLanguageIDForFile(t *testing.T) {
	tests := []struct {
		specID string
		path   string
		want   string
	}{
		{"typescript", "file.tsx", "typescript"},
		{"typescript", "file.jsx", "javascript"},
		{"typescript", "file.ts", "typescript"},
		{"go", "file.go", "go"},
		{"python", "file.py", "python"},
	}
	for _, tt := range tests {
		got := languageIDForFile(tt.specID, tt.path)
		if got != tt.want {
			t.Errorf("languageIDForFile(%q,%q) = %q, want %q", tt.specID, tt.path, got, tt.want)
		}
	}
}

func TestUnsupportedInstallCommand(t *testing.T) {
	got := unsupportedInstallCommand("test hint")
	if got == "" {
		t.Error("expected non-empty")
	}
	if !strings.Contains(got, "test hint") {
		t.Errorf("expected hint in output: %s", got)
	}
}

func TestCommandWithPrereq(t *testing.T) {
	got := commandWithPrereq("go", "need go", "go install X")
	if got == "" {
		t.Error("expected non-empty")
	}
}

func TestNodeWorkspaceInstallCommand(t *testing.T) {
	got := nodeWorkspaceInstallCommand("typescript-language-server", "need npm", "npm not found")
	if got == "" {
		t.Error("expected non-empty")
	}
}

func TestCsharpToolInstallCommand(t *testing.T) {
	got := csharpToolInstallCommand()
	if got == "" {
		t.Error("expected non-empty")
	}
}

func TestPythonVenvInstallCommand(t *testing.T) {
	got := pythonVenvInstallCommand("pyright", "need python")
	if got == "" {
		t.Error("expected non-empty")
	}
}

// --- client.go pure functions ---

func TestStringifyHoverContents(t *testing.T) {
	tests := []struct {
		input    interface{}
		expected string
	}{
		{"plain text", "plain text"},
		{nil, ""},
	}
	for _, tt := range tests {
		got := stringifyHoverContents(tt.input)
		if got != tt.expected {
			t.Errorf("stringifyHoverContents(%v) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestServerLaunchEnv(t *testing.T) {
	env := serverLaunchEnv("go")
	if env == nil {
		t.Error("expected non-nil env")
	}
}
