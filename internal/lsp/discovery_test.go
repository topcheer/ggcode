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
