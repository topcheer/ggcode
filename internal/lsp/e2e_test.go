//go:build integration_local

package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestE2E_BashLanguageServer_InstallAndUse tests the full LSP lifecycle:
// 1. Create workspace with .sh file
// 2. Detect language → Available=false
// 3. Run InstallOption.Command to install bash-language-server
// 4. Re-detect → Available=true
// 5. Use the installed server for LSP operations (Hover, Diagnostics)
func TestE2E_BashLanguageServer_InstallAndUse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping install E2E in short mode")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not found, skipping install E2E")
	}
	// Skip if bash-language-server is already globally installed
	if _, err := exec.LookPath("bash-language-server"); err == nil {
		t.Skip("bash-language-server already in PATH, install test not meaningful")
	}

	workspace := t.TempDir()

	// Step 1: Create a shell script
	script := `#!/usr/bin/env bash

# Greets the given user
greet() {
  local name="$1"
  echo "Hello, ${name}!"
}

# Adds two numbers
add() {
  local a="$1"
  local b="$2"
  echo $((a + b))
}

# Main entry point
main() {
  greet "World"
  add 1 2
}

main
`
	scriptFile := filepath.Join(workspace, "demo.sh")
	if err := os.WriteFile(scriptFile, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	// Step 2: Detect — should find shell language but NOT available
	status := DetectWorkspaceStatus(workspace)
	var shellLang *LanguageStatus
	for i := range status.Languages {
		if status.Languages[i].ID == "shell" {
			shellLang = &status.Languages[i]
			break
		}
	}
	if shellLang == nil {
		t.Fatal("expected shell language to be detected in workspace with .sh file")
	}
	t.Logf("Before install: Available=%v Binary=%q", shellLang.Available, shellLang.Binary)

	if shellLang.Available {
		// Could happen if bash-language-server is in npm global prefix
		t.Log("bash-language-server found via fallback, skipping install step")
	} else {
		// Step 3: Get install command and execute it
		if len(shellLang.InstallOptions) == 0 {
			t.Fatal("expected install options for shell language")
		}
		// Use the project-level install option so the binary lands in node_modules/.bin
		var installCmd string
		for _, opt := range shellLang.InstallOptions {
			if opt.Scope == ScopeProject {
				installCmd = opt.Command
				break
			}
		}
		if installCmd == "" {
			installCmd = shellLang.InstallOptions[0].Command
		}
		t.Logf("Install command: %s", installCmd)

		cmd := exec.Command("sh", "-c", installCmd)
		cmd.Dir = workspace
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("install failed: %v", err)
		}

		// Step 4: Re-detect — should now be available
		status2 := DetectWorkspaceStatus(workspace)
		var shellLang2 *LanguageStatus
		for i := range status2.Languages {
			if status2.Languages[i].ID == "shell" {
				shellLang2 = &status2.Languages[i]
				break
			}
		}
		if shellLang2 == nil {
			t.Fatal("shell language not detected after install")
		}
		t.Logf("After install: Available=%v Binary=%q", shellLang2.Available, shellLang2.Binary)
		if !shellLang2.Available {
			t.Fatal("expected shell language to be available after install")
		}

		// Verify the binary is in node_modules/.bin
		localBin := filepath.Join(workspace, "node_modules", ".bin", "bash-language-server")
		if _, err := os.Stat(localBin); err != nil {
			t.Errorf("expected %s to exist after install: %v", localBin, err)
		}
	}

	// Step 5: Use the installed server for real LSP operations
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Hover over the greet function
	hoverResult, err := Hover(ctx, workspace, scriptFile, Position{Line: 4, Character: 2})
	if err != nil {
		t.Fatalf("Hover failed: %v", err)
	}
	t.Logf("Hover result: %s", truncateForLog(hoverResult, 300))

	// Get diagnostics
	diags, err := Diagnostics(ctx, workspace, scriptFile)
	if err != nil {
		t.Fatalf("Diagnostics failed: %v", err)
	}
	t.Logf("Diagnostics: %d items", len(diags))

	// References to greet function
	locs, err := References(ctx, workspace, scriptFile, Position{Line: 4, Character: 2})
	if err != nil {
		t.Fatalf("References failed: %v", err)
	}
	t.Logf("References: %d results", len(locs))

	// Definition of greet
	defs, err := Definition(ctx, workspace, scriptFile, Position{Line: 4, Character: 2})
	if err != nil {
		t.Fatalf("Definition failed: %v", err)
	}
	t.Logf("Definition: %d results", len(defs))

	// Clean up
	cleanupSessions(t, workspace)
}

// TestE2E_YAMLLanguageServer_InstallAndUse tests installing yaml-language-server
// into a workspace and using it for diagnostics.
func TestE2E_YAMLLanguageServer_InstallAndUse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping install E2E in short mode")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not found, skipping install E2E")
	}
	if _, err := exec.LookPath("yaml-language-server"); err == nil {
		t.Skip("yaml-language-server already in PATH")
	}

	workspace := t.TempDir()

	// Create a YAML file
	yamlContent := `name: test-project
version: 1.0.0
description: A test project
dependencies:
  lodash: ^4.17.0
  express: ^4.18.0
scripts:
  build: tsc
  test: jest
`
	yamlFile := filepath.Join(workspace, "config.yaml")
	if err := os.WriteFile(yamlFile, []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Detect
	status := DetectWorkspaceStatus(workspace)
	var yamlLang *LanguageStatus
	for i := range status.Languages {
		if status.Languages[i].ID == "yaml" {
			yamlLang = &status.Languages[i]
			break
		}
	}
	if yamlLang == nil {
		t.Fatal("expected yaml language to be detected")
	}
	t.Logf("Before install: Available=%v", yamlLang.Available)

	if yamlLang.Available {
		t.Log("yaml-language-server found via fallback, skipping install")
	} else {
		// Install
		if len(yamlLang.InstallOptions) == 0 {
			t.Fatal("expected install options for yaml")
		}
		installCmd := yamlLang.InstallOptions[0].Command
		t.Logf("Install command: %s", installCmd)

		cmd := exec.Command("sh", "-c", installCmd)
		cmd.Dir = workspace
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("install failed: %v", err)
		}

		// Re-detect
		status2 := DetectWorkspaceStatus(workspace)
		for i := range status2.Languages {
			if status2.Languages[i].ID == "yaml" {
				yamlLang = &status2.Languages[i]
				break
			}
		}
		t.Logf("After install: Available=%v Binary=%q", yamlLang.Available, yamlLang.Binary)
		if !yamlLang.Available {
			t.Fatal("expected yaml to be available after install")
		}
	}

	// Use it
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	hoverResult, err := Hover(ctx, workspace, yamlFile, Position{Line: 1, Character: 2})
	if err != nil {
		t.Fatalf("Hover failed: %v", err)
	}
	t.Logf("YAML Hover: %s", truncateForLog(hoverResult, 200))

	diags, err := Diagnostics(ctx, workspace, yamlFile)
	if err != nil {
		t.Fatalf("Diagnostics failed: %v", err)
	}
	t.Logf("YAML Diagnostics: %d items", len(diags))

	cleanupSessions(t, workspace)
}

// TestDetectWorkspaceStatus_Empty tests detection on empty directory.
func TestDetectWorkspaceStatus_Empty(t *testing.T) {
	dir := t.TempDir()
	status := DetectWorkspaceStatus(dir)
	if len(status.Languages) != 0 {
		t.Errorf("expected no languages for empty dir, got %d", len(status.Languages))
	}
}

// TestDetectWorkspaceStatus_MixedLang tests detection on a workspace with multiple languages.
func TestDetectWorkspaceStatus_MixedLang(t *testing.T) {
	dir := t.TempDir()

	// Go
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644)

	// TypeScript
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(dir, "index.ts"), []byte("export {}"), 0644)

	// Shell
	os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/bash\n"), 0755)

	status := DetectWorkspaceStatus(dir)

	langMap := map[string]bool{}
	for _, l := range status.Languages {
		langMap[l.ID] = true
		t.Logf("Found: %s Available=%v Binary=%q", l.ID, l.Available, l.Binary)
	}

	for _, expected := range []string{"go", "typescript", "shell"} {
		if !langMap[expected] {
			t.Errorf("expected %s language to be detected", expected)
		}
	}
}
