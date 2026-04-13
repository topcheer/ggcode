package lsp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDetectWorkspaceStatusMarksMissingJavaServerWithInstallHint(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "pom.xml"), []byte("<project/>"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	status := DetectWorkspaceStatus(workspace)
	if len(status.Languages) != 1 {
		t.Fatalf("expected 1 detected language, got %#v", status.Languages)
	}
	java := status.Languages[0]
	if java.ID != "java" {
		t.Fatalf("expected java detection, got %#v", java)
	}
	if java.Available {
		t.Fatalf("expected java server to be unavailable in test env, got %#v", java)
	}
	if strings.TrimSpace(java.InstallHint) == "" {
		t.Fatalf("expected java install hint, got %#v", java)
	}
}

func TestDetectWorkspaceStatusUsesInstalledBinaryFromPATH(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.com/test\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	binDir := t.TempDir()
	binaryName := "gopls"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(binDir, binaryName)
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath)

	status := DetectWorkspaceStatus(workspace)
	if len(status.Languages) != 1 {
		t.Fatalf("expected 1 detected language, got %#v", status.Languages)
	}
	goStatus := status.Languages[0]
	if goStatus.ID != "go" {
		t.Fatalf("expected go detection, got %#v", goStatus)
	}
	if !goStatus.Available || goStatus.Binary != "gopls" {
		t.Fatalf("expected available gopls binary, got %#v", goStatus)
	}
}

func TestDetectWorkspaceStatusUsesRustupManagedRustAnalyzer(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "Cargo.toml"), []byte("[package]\nname = \"board\"\nversion = \"0.1.0\"\nedition = \"2024\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(Cargo.toml) error = %v", err)
	}

	binDir := t.TempDir()
	rustupPath := filepath.Join(binDir, "rustup")
	fakeAnalyzer := filepath.Join(t.TempDir(), "rust-analyzer")
	if runtime.GOOS == "windows" {
		rustupPath += ".bat"
		fakeAnalyzer += ".exe"
	}
	if err := os.WriteFile(fakeAnalyzer, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(fake rust-analyzer) error = %v", err)
	}
	if err := os.WriteFile(rustupPath, []byte("#!/bin/sh\nprintf '%s\\n' \""+fakeAnalyzer+"\"\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(fake rustup) error = %v", err)
	}
	t.Setenv("PATH", binDir)

	status := DetectWorkspaceStatus(workspace)
	if len(status.Languages) != 1 {
		t.Fatalf("expected 1 detected language, got %#v", status.Languages)
	}
	rustStatus := status.Languages[0]
	if rustStatus.ID != "rust" {
		t.Fatalf("expected rust detection, got %#v", rustStatus)
	}
	if !rustStatus.Available || rustStatus.Binary != "rust-analyzer" {
		t.Fatalf("expected rustup-managed rust-analyzer to be detected, got %#v", rustStatus)
	}
}

func TestResolveServerForFileUsesRustupManagedBinaryPath(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "Cargo.toml"), []byte("[package]\nname = \"board\"\nversion = \"0.1.0\"\nedition = \"2024\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(Cargo.toml) error = %v", err)
	}
	srcDir := filepath.Join(workspace, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(src) error = %v", err)
	}
	mainPath := filepath.Join(srcDir, "main.rs")
	if err := os.WriteFile(mainPath, []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(main.rs) error = %v", err)
	}

	binDir := t.TempDir()
	rustupPath := filepath.Join(binDir, "rustup")
	fakeAnalyzer := filepath.Join(t.TempDir(), "rust-analyzer")
	if runtime.GOOS == "windows" {
		rustupPath += ".bat"
		fakeAnalyzer += ".exe"
	}
	if err := os.WriteFile(fakeAnalyzer, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(fake rust-analyzer) error = %v", err)
	}
	if err := os.WriteFile(rustupPath, []byte("#!/bin/sh\nprintf '%s\\n' \""+fakeAnalyzer+"\"\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(fake rustup) error = %v", err)
	}
	t.Setenv("PATH", binDir)

	resolved, ok := ResolveServerForFile(workspace, mainPath)
	if !ok {
		t.Fatal("expected rust server resolution")
	}
	if resolved.Binary != fakeAnalyzer {
		t.Fatalf("expected resolved rust-analyzer path %q, got %#v", fakeAnalyzer, resolved)
	}
}

func TestDetectWorkspaceStatusIncludesPythonInstallOptions(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "pyproject.toml"), []byte("[project]\nname = \"board\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(pyproject.toml) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "app.py"), []byte("print('hello')\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(app.py) error = %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	status := DetectWorkspaceStatus(workspace)
	if len(status.Languages) != 1 {
		t.Fatalf("expected 1 detected language, got %#v", status.Languages)
	}
	python := status.Languages[0]
	if python.ID != "python" {
		t.Fatalf("expected python detection, got %#v", python)
	}
	if python.Available {
		t.Fatalf("expected python server to be unavailable in isolated PATH, got %#v", python)
	}
	if len(python.InstallOptions) != 2 {
		t.Fatalf("expected 2 python install options, got %#v", python.InstallOptions)
	}
	if !python.InstallOptions[0].Recommended || !strings.Contains(python.InstallOptions[0].Command, "pyright") {
		t.Fatalf("expected recommended pyright install option, got %#v", python.InstallOptions)
	}
	if !strings.Contains(python.InstallOptions[0].Command, ".venv") || !strings.Contains(python.InstallOptions[0].Command, "python3 -m venv") {
		t.Fatalf("expected python install option to bootstrap a venv, got %#v", python.InstallOptions[0])
	}
	if !strings.Contains(python.InstallOptions[1].Command, "python-lsp-server") {
		t.Fatalf("expected pylsp install option, got %#v", python.InstallOptions)
	}
}

func TestResolveServerForFileUsesPyrightStdioArgs(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "pyproject.toml"), []byte("[project]\nname = \"board\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(pyproject.toml) error = %v", err)
	}
	binDir := t.TempDir()
	binaryName := "pyright-langserver"
	if runtime.GOOS == "windows" {
		binaryName += ".cmd"
	}
	binaryPath := filepath.Join(binDir, binaryName)
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(pyright-langserver) error = %v", err)
	}
	t.Setenv("PATH", binDir)

	resolved, ok := ResolveServerForFile(workspace, filepath.Join(workspace, "app.py"))
	if !ok {
		t.Fatal("expected python server resolution")
	}
	if resolved.Binary != "pyright-langserver" {
		t.Fatalf("expected pyright-langserver, got %#v", resolved)
	}
	if len(resolved.Args) != 1 || resolved.Args[0] != "--stdio" {
		t.Fatalf("expected pyright stdio args, got %#v", resolved.Args)
	}
}

func TestDetectWorkspaceStatusUsesPythonWorkspaceVenvBinary(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "pyproject.toml"), []byte("[project]\nname = \"board\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(pyproject.toml) error = %v", err)
	}
	binDir := filepath.Join(workspace, ".venv", venvBinDir())
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(.venv bin) error = %v", err)
	}
	pyrightPath := filepath.Join(binDir, executableName("pyright-langserver"))
	if err := os.WriteFile(pyrightPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(pyright-langserver) error = %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	status := DetectWorkspaceStatus(workspace)
	if len(status.Languages) != 1 {
		t.Fatalf("expected 1 detected language, got %#v", status.Languages)
	}
	python := status.Languages[0]
	if python.ID != "python" {
		t.Fatalf("expected python detection, got %#v", python)
	}
	if !python.Available || python.Binary != "pyright-langserver" {
		t.Fatalf("expected workspace venv pyright to be detected, got %#v", python)
	}
}

func TestResolveServerForFileUsesPythonWorkspaceVenvPath(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "pyproject.toml"), []byte("[project]\nname = \"board\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(pyproject.toml) error = %v", err)
	}
	appPath := filepath.Join(workspace, "app.py")
	if err := os.WriteFile(appPath, []byte("print('hello')\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(app.py) error = %v", err)
	}
	binDir := filepath.Join(workspace, ".venv", venvBinDir())
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(.venv bin) error = %v", err)
	}
	pyrightPath := filepath.Join(binDir, executableName("pyright-langserver"))
	if err := os.WriteFile(pyrightPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(pyright-langserver) error = %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	resolved, ok := ResolveServerForFile(workspace, appPath)
	if !ok {
		t.Fatal("expected python server resolution from workspace venv")
	}
	if resolved.Binary != pyrightPath {
		t.Fatalf("expected resolved python venv path %q, got %#v", pyrightPath, resolved)
	}
	if len(resolved.Args) != 1 || resolved.Args[0] != "--stdio" {
		t.Fatalf("expected pyright stdio args for venv binary, got %#v", resolved.Args)
	}
}

func TestPythonVenvInstallCommandHasValidShellSyntax(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell syntax check only applies to POSIX command generation")
	}
	cmd := pythonVenvInstallCommand("", "pyright")
	if strings.TrimSpace(cmd) == "" {
		t.Fatal("expected non-empty python venv install command")
	}
	if err := exec.Command("sh", "-n", "-c", cmd).Run(); err != nil {
		t.Fatalf("expected valid shell syntax, got %v for %q", err, cmd)
	}
}

func TestDocumentSymbolsWithInstalledGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.com/test\n\ngo 1.26.1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	source := `package sample

func Add(a int, b int) int {
	return a + b
}
`
	path := filepath.Join(workspace, "sample.go")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("WriteFile(sample.go) error = %v", err)
	}
	symbols, err := DocumentSymbols(context.Background(), workspace, path)
	if err != nil {
		t.Fatalf("DocumentSymbols() error = %v", err)
	}
	if len(symbols) == 0 {
		t.Fatal("expected at least one symbol")
	}
	found := false
	for _, symbol := range symbols {
		if symbol.Name == "Add" {
			found = true
			if symbol.Range.Start.Line != 3 || symbol.Range.Start.Character != 1 {
				t.Fatalf("expected Add symbol at 3:1, got %#v", symbol.Range.Start)
			}
			break
		}
	}
	if !found {
		t.Fatalf("expected Add symbol, got %#v", symbols)
	}
}

func TestParseSymbolsFallsBackToSymbolInformation(t *testing.T) {
	raw, err := json.Marshal([]map[string]any{
		{
			"name": "Add",
			"kind": 12,
			"location": map[string]any{
				"uri": "file:///tmp/sample.go",
				"range": map[string]any{
					"start": map[string]any{"line": 2, "character": 0},
					"end":   map[string]any{"line": 4, "character": 1},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	symbols := parseSymbols(raw)
	if len(symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %#v", symbols)
	}
	if symbols[0].Name != "Add" {
		t.Fatalf("expected Add symbol, got %#v", symbols[0])
	}
	if symbols[0].Range.Start.Line != 3 || symbols[0].Range.Start.Character != 1 {
		t.Fatalf("expected start position 3:1, got %#v", symbols[0].Range.Start)
	}
	if symbols[0].Range.End.Line != 5 || symbols[0].Range.End.Character != 2 {
		t.Fatalf("expected end position 5:2, got %#v", symbols[0].Range.End)
	}
}

func TestWorkspaceSymbolsWithInstalledGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.com/test\n\ngo 1.26.1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	source := `package sample

func Add(a int, b int) int {
	return a + b
}
`
	path := filepath.Join(workspace, "sample.go")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("WriteFile(sample.go) error = %v", err)
	}
	symbols, err := WorkspaceSymbols(context.Background(), workspace, "Add")
	if err != nil {
		t.Fatalf("WorkspaceSymbols() error = %v", err)
	}
	if len(symbols) == 0 {
		t.Fatal("expected at least one workspace symbol")
	}
	if symbols[0].Name != "Add" {
		t.Fatalf("expected Add symbol, got %#v", symbols[0])
	}
}

func TestSequentialRepoFileLSPCallsWithInstalledGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	workspace := filepath.Clean(filepath.Join("..", ".."))
	path := filepath.Join(workspace, "internal", "lsp", "client.go")

	symbols, err := DocumentSymbols(context.Background(), workspace, path)
	if err != nil || len(symbols) == 0 {
		t.Fatalf("DocumentSymbols() err=%v len=%d", err, len(symbols))
	}
	hover, err := Hover(context.Background(), workspace, path, Position{Line: 184, Character: 6})
	if err != nil || strings.TrimSpace(hover) == "" {
		t.Fatalf("Hover() err=%v hover=%q", err, hover)
	}
	diagnostics, err := Diagnostics(context.Background(), workspace, path)
	if err != nil {
		t.Fatalf("Diagnostics() err=%v", err)
	}
	_ = diagnostics
	definition, err := Definition(context.Background(), workspace, path, Position{Line: 184, Character: 6})
	if err != nil || len(definition) == 0 {
		t.Fatalf("Definition() err=%v definition=%#v", err, definition)
	}
	references, err := References(context.Background(), workspace, path, Position{Line: 184, Character: 6})
	if err != nil || len(references) == 0 {
		t.Fatalf("References() err=%v references=%#v", err, references)
	}
}

func TestSequentialTimedRepoFileLSPCallsWithInstalledGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	workspace := filepath.Clean(filepath.Join("..", ".."))
	path := filepath.Join(workspace, "internal", "lsp", "client.go")

	ctx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
	symbols, err := DocumentSymbols(ctx1, workspace, path)
	cancel1()
	if err != nil || len(symbols) == 0 {
		t.Fatalf("DocumentSymbols() err=%v len=%d", err, len(symbols))
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	definition, err := Definition(ctx2, workspace, path, Position{Line: 184, Character: 6})
	cancel2()
	if err != nil || len(definition) == 0 {
		t.Fatalf("Definition() err=%v definition=%#v", err, definition)
	}

	ctx3, cancel3 := context.WithTimeout(context.Background(), 5*time.Second)
	references, err := References(ctx3, workspace, path, Position{Line: 184, Character: 6})
	cancel3()
	if err != nil || len(references) == 0 {
		t.Fatalf("References() err=%v references=%#v", err, references)
	}
}
