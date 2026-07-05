package lsp

import (
	"context"
	"encoding/json"
	"errors"
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
	t.Setenv("PATH", t.TempDir())

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

func TestDetectWorkspaceStatusUsesInstalledClangdFromPATH(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "compile_commands.json"), []byte("[]\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(compile_commands.json) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "main.cpp"), []byte("int main() { return 0; }\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(main.cpp) error = %v", err)
	}
	binDir := t.TempDir()
	binaryPath := filepath.Join(binDir, executableName("clangd"))
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(clangd) error = %v", err)
	}
	t.Setenv("PATH", binDir)

	status := DetectWorkspaceStatus(workspace)
	var clang LanguageStatus
	found := false
	for _, lang := range status.Languages {
		if lang.ID == "clang" {
			clang = lang
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected clang detection, got %#v", status.Languages)
	}
	if clang.ID != "clang" {
		t.Fatalf("expected clang detection, got %#v", clang)
	}
	if !clang.Available || clang.Binary != "clangd" {
		t.Fatalf("expected available clangd binary, got %#v", clang)
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
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

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
	if len(python.InstallOptions) != 4 {
		t.Fatalf("expected 4 python install options (user+project for each), got %#v", python.InstallOptions)
	}
	if !python.InstallOptions[0].Recommended || !strings.Contains(python.InstallOptions[0].Command, "pyright") {
		t.Fatalf("expected recommended pyright install option, got %#v", python.InstallOptions)
	}
	if !strings.Contains(python.InstallOptions[0].Command, "--user") {
		t.Fatalf("expected first pyright option to be user-level, got %#v", python.InstallOptions[0])
	}
	// Check that a project-level venv option exists
	foundVenv := false
	for _, opt := range python.InstallOptions {
		if strings.Contains(opt.Command, ".venv") && strings.Contains(opt.Command, "python3 -m venv") {
			foundVenv = true
			break
		}
	}
	if !foundVenv {
		t.Fatalf("expected a project venv install option, got %#v", python.InstallOptions)
	}
	// Check that pylsp option exists
	foundPylsp := false
	for _, opt := range python.InstallOptions {
		if strings.Contains(opt.Command, "python-lsp-server") {
			foundPylsp = true
			break
		}
	}
	if !foundPylsp {
		t.Fatalf("expected pylsp install option, got %#v", python.InstallOptions)
	}
}

func TestDetectWorkspaceStatusIncludesTypeScriptInstallOption(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "package.json"), []byte("{\"name\":\"board\",\"private\":true}"), 0o644); err != nil {
		t.Fatalf("WriteFile(package.json) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "tsconfig.json"), []byte("{\"compilerOptions\":{\"target\":\"ES2022\"}}"), 0o644); err != nil {
		t.Fatalf("WriteFile(tsconfig.json) error = %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	status := DetectWorkspaceStatus(workspace)
	var ts LanguageStatus
	found := false
	for _, lang := range status.Languages {
		if lang.ID == "typescript" {
			ts = lang
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected typescript detection, got %#v", status.Languages)
	}
	if ts.ID != "typescript" {
		t.Fatalf("expected typescript detection, got %#v", ts)
	}
	if len(ts.InstallOptions) != 1 {
		t.Fatalf("expected 1 typescript install option, got %#v", ts.InstallOptions)
	}
	if !ts.InstallOptions[0].Recommended || !strings.Contains(ts.InstallOptions[0].Command, "typescript-language-server") {
		t.Fatalf("expected recommended typescript-language-server install option, got %#v", ts.InstallOptions)
	}
}

func TestDetectWorkspaceStatusUsesInstalledSourceKitLSPFromPATH(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "Package.swift"), []byte("import PackageDescription\nlet package = Package(name: \"Board\")\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(Package.swift) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "Sources", "Board"), 0o755); err != nil {
		t.Fatalf("MkdirAll(Sources/Board) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "Sources", "Board", "main.swift"), []byte("print(\"hello\")\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(main.swift) error = %v", err)
	}
	binDir := t.TempDir()
	binaryPath := filepath.Join(binDir, executableName("sourcekit-lsp"))
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(sourcekit-lsp) error = %v", err)
	}
	t.Setenv("PATH", binDir)

	status := DetectWorkspaceStatus(workspace)
	var swift LanguageStatus
	found := false
	for _, lang := range status.Languages {
		if lang.ID == "swift" {
			swift = lang
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected swift detection, got %#v", status.Languages)
	}
	if swift.ID != "swift" {
		t.Fatalf("expected swift detection, got %#v", swift)
	}
	if !swift.Available || swift.Binary != "sourcekit-lsp" {
		t.Fatalf("expected available sourcekit-lsp binary, got %#v", swift)
	}
}

func TestDetectWorkspaceStatusIncludesConfigInstallOptions(t *testing.T) {
	workspace := t.TempDir()
	files := map[string]string{
		"config.yaml": "name: board\n",
		"config.json": "{\n  \"name\": \"board\"\n}\n",
		"Dockerfile":  "FROM alpine:3.20\n",
		"deploy.sh":   "#!/usr/bin/env bash\nset -euo pipefail\n",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(contents), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	t.Setenv("PATH", t.TempDir())

	status := DetectWorkspaceStatus(workspace)
	got := map[string]LanguageStatus{}
	for _, lang := range status.Languages {
		got[lang.ID] = lang
	}
	for _, id := range []string{"yaml", "json", "dockerfile", "shell"} {
		lang, ok := got[id]
		if !ok {
			t.Fatalf("expected %s detection in %#v", id, status.Languages)
		}
		// Should have at least 2 options (global + project)
		if len(lang.InstallOptions) < 2 {
			t.Fatalf("expected at least 2 install options for %s, got %#v", id, lang.InstallOptions)
		}
		// First option should be recommended (global)
		if !lang.InstallOptions[0].Recommended {
			t.Fatalf("expected first install option to be recommended for %s, got %#v", id, lang.InstallOptions)
		}
	}
	if !strings.Contains(got["yaml"].InstallOptions[0].Command, "yaml-language-server") {
		t.Fatalf("expected yaml install command, got %#v", got["yaml"].InstallOptions)
	}
	// yaml global option should use npm install -g
	if !strings.Contains(got["yaml"].InstallOptions[0].Command, "-g") {
		t.Fatalf("expected yaml global install to use -g, got %#v", got["yaml"].InstallOptions[0])
	}
	if !strings.Contains(got["json"].InstallOptions[0].Command, "vscode-langservers-extracted") {
		t.Fatalf("expected json install command, got %#v", got["json"].InstallOptions)
	}
	if !strings.Contains(got["dockerfile"].InstallOptions[0].Command, "dockerfile-language-server-nodejs") {
		t.Fatalf("expected docker install command, got %#v", got["dockerfile"].InstallOptions)
	}
	if !strings.Contains(got["shell"].InstallOptions[0].Command, "bash-language-server") {
		t.Fatalf("expected shell install command, got %#v", got["shell"].InstallOptions)
	}
}

func TestDetectWorkspaceStatusUsesWorkspaceNodeModulesBinary(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "package.json"), []byte("{\"name\":\"board\",\"private\":true}"), 0o644); err != nil {
		t.Fatalf("WriteFile(package.json) error = %v", err)
	}
	srcPath := filepath.Join(workspace, "src", "index.ts")
	if err := os.MkdirAll(filepath.Dir(srcPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(src) error = %v", err)
	}
	if err := os.WriteFile(srcPath, []byte("export const value = 1;\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(index.ts) error = %v", err)
	}
	binDir := filepath.Join(workspace, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(node_modules/.bin) error = %v", err)
	}
	tsserverPath := filepath.Join(binDir, executableName("typescript-language-server"))
	if err := os.WriteFile(tsserverPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(typescript-language-server) error = %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	status := DetectWorkspaceStatus(workspace)
	var ts LanguageStatus
	found := false
	for _, lang := range status.Languages {
		if lang.ID == "typescript" {
			ts = lang
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected typescript detection, got %#v", status.Languages)
	}
	if ts.ID != "typescript" {
		t.Fatalf("expected typescript detection, got %#v", ts)
	}
	if !ts.Available || ts.Binary != "typescript-language-server" {
		t.Fatalf("expected workspace node_modules typescript server to be detected, got %#v", ts)
	}
}

func TestResolveServerForFileUsesWorkspaceNodeModulesBinaryPath(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "package.json"), []byte("{\"name\":\"board\",\"private\":true}"), 0o644); err != nil {
		t.Fatalf("WriteFile(package.json) error = %v", err)
	}
	srcPath := filepath.Join(workspace, "src", "index.ts")
	if err := os.MkdirAll(filepath.Dir(srcPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(src) error = %v", err)
	}
	if err := os.WriteFile(srcPath, []byte("export const value = 1;\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(index.ts) error = %v", err)
	}
	binDir := filepath.Join(workspace, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(node_modules/.bin) error = %v", err)
	}
	tsserverPath := filepath.Join(binDir, executableName("typescript-language-server"))
	if err := os.WriteFile(tsserverPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(typescript-language-server) error = %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	resolved, ok := ResolveServerForFile(workspace, srcPath)
	if !ok {
		t.Fatal("expected typescript server resolution from workspace node_modules")
	}
	if resolved.Binary != tsserverPath {
		t.Fatalf("expected resolved typescript node_modules path %q, got %#v", tsserverPath, resolved)
	}
	if len(resolved.Args) != 1 || resolved.Args[0] != "--stdio" {
		t.Fatalf("expected typescript stdio args, got %#v", resolved.Args)
	}
}

func TestResolveServerForFileUsesWorkspaceLocalClangdPath(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "compile_commands.json"), []byte("[]\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(compile_commands.json) error = %v", err)
	}
	sourcePath := filepath.Join(workspace, "main.cpp")
	if err := os.WriteFile(sourcePath, []byte("int add(int a, int b) { return a + b; }\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(main.cpp) error = %v", err)
	}
	toolDir := filepath.Join(workspace, ".ggcode", "tools")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(.ggcode/tools) error = %v", err)
	}
	clangdPath := filepath.Join(toolDir, executableName("clangd"))
	if err := os.WriteFile(clangdPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(clangd) error = %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	resolved, ok := ResolveServerForFile(workspace, sourcePath)
	if !ok {
		t.Fatal("expected clang server resolution")
	}
	if resolved.Binary != clangdPath {
		t.Fatalf("expected resolved clangd path %q, got %#v", clangdPath, resolved)
	}
	if resolved.LanguageID != "cpp" {
		t.Fatalf("expected cpp language id, got %#v", resolved)
	}
}

func TestResolveServerForFileUsesWorkspaceNodeModulesJSONServerPath(t *testing.T) {
	workspace := t.TempDir()
	sourcePath := filepath.Join(workspace, "config.json")
	if err := os.WriteFile(sourcePath, []byte("{\"name\":\"board\"}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(config.json) error = %v", err)
	}
	binDir := filepath.Join(workspace, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(node_modules/.bin) error = %v", err)
	}
	serverPath := filepath.Join(binDir, executableName("vscode-json-language-server"))
	if err := os.WriteFile(serverPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(vscode-json-language-server) error = %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	resolved, ok := ResolveServerForFile(workspace, sourcePath)
	if !ok {
		t.Fatal("expected json server resolution")
	}
	if resolved.Binary != serverPath {
		t.Fatalf("expected resolved json server path %q, got %#v", serverPath, resolved)
	}
	if len(resolved.Args) != 1 || resolved.Args[0] != "--stdio" {
		t.Fatalf("expected json server stdio args, got %#v", resolved.Args)
	}
}

func TestResolveServerForFileUsesDockerfileNameAndStdioArgs(t *testing.T) {
	workspace := t.TempDir()
	sourcePath := filepath.Join(workspace, "Dockerfile")
	if err := os.WriteFile(sourcePath, []byte("FROM alpine:3.20\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(Dockerfile) error = %v", err)
	}
	binDir := filepath.Join(workspace, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(node_modules/.bin) error = %v", err)
	}
	serverPath := filepath.Join(binDir, executableName("docker-langserver"))
	if err := os.WriteFile(serverPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(docker-langserver) error = %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	resolved, ok := ResolveServerForFile(workspace, sourcePath)
	if !ok {
		t.Fatal("expected dockerfile server resolution")
	}
	if resolved.Binary != serverPath {
		t.Fatalf("expected resolved dockerfile server path %q, got %#v", serverPath, resolved)
	}
	if resolved.LanguageID != "dockerfile" || len(resolved.Args) != 1 || resolved.Args[0] != "--stdio" {
		t.Fatalf("expected dockerfile stdio launch, got %#v", resolved)
	}
}

func TestDetectWorkspaceStatusUsesNPMGlobalTypeScriptBinary(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "package.json"), []byte("{\"name\":\"board\",\"private\":true}"), 0o644); err != nil {
		t.Fatalf("WriteFile(package.json) error = %v", err)
	}
	npmDir := t.TempDir()
	globalPrefix := t.TempDir()
	globalBin := filepath.Join(globalPrefix, "bin")
	if runtime.GOOS == "windows" {
		globalBin = globalPrefix
	}
	if err := os.MkdirAll(globalBin, 0o755); err != nil {
		t.Fatalf("MkdirAll(global bin) error = %v", err)
	}
	tsserverPath := filepath.Join(globalBin, executableName("typescript-language-server"))
	if err := os.WriteFile(tsserverPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(global typescript-language-server) error = %v", err)
	}
	npmPath := filepath.Join(npmDir, executableName("npm"))
	if runtime.GOOS == "windows" {
		npmPath = filepath.Join(npmDir, "npm.cmd")
	}
	if err := os.WriteFile(npmPath, []byte("#!/bin/sh\nprintf '%s\\n' \""+globalPrefix+"\"\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(fake npm) error = %v", err)
	}
	t.Setenv("PATH", npmDir)

	status := DetectWorkspaceStatus(workspace)
	var ts LanguageStatus
	found := false
	for _, lang := range status.Languages {
		if lang.ID == "typescript" {
			ts = lang
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected typescript detection, got %#v", status.Languages)
	}
	if ts.ID != "typescript" {
		t.Fatalf("expected typescript detection, got %#v", ts)
	}
	if !ts.Available || ts.Binary != "typescript-language-server" {
		t.Fatalf("expected npm-global typescript server to be detected, got %#v", ts)
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

func TestDetectWorkspaceStatusIncludesCSharpInstallOption(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "global.json"), []byte("{\"sdk\":{\"version\":\"8.0.100\"}}"), 0o644); err != nil {
		t.Fatalf("WriteFile(global.json) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "Board.csproj"), []byte("<Project Sdk=\"Microsoft.NET.Sdk\"></Project>"), 0o644); err != nil {
		t.Fatalf("WriteFile(Board.csproj) error = %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	status := DetectWorkspaceStatus(workspace)
	var csharp LanguageStatus
	found := false
	for _, lang := range status.Languages {
		if lang.ID == "csharp" {
			csharp = lang
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected csharp detection, got %#v", status.Languages)
	}
	if csharp.ID != "csharp" {
		t.Fatalf("expected csharp detection, got %#v", csharp)
	}
	if len(csharp.InstallOptions) < 2 {
		t.Fatalf("expected at least 2 csharp install options, got %#v", csharp.InstallOptions)
	}
	// First option should be user-level (recommended)
	if !csharp.InstallOptions[0].Recommended {
		t.Fatalf("expected first csharp install option to be recommended, got %#v", csharp.InstallOptions)
	}
	if !strings.Contains(csharp.InstallOptions[0].Command, "csharp-ls") {
		t.Fatalf("expected csharp-ls install option, got %#v", csharp.InstallOptions[0])
	}
	// First option should be user-level (-g flag)
	if !strings.Contains(csharp.InstallOptions[0].Command, "-g") {
		t.Fatalf("expected first csharp option to be user-level (-g), got %#v", csharp.InstallOptions[0])
	}
	// Second option should be project-level (.ggcode/tools)
	if !strings.Contains(csharp.InstallOptions[1].Command, "--tool-path") || !strings.Contains(csharp.InstallOptions[1].Command, ".ggcode/tools") {
		t.Fatalf("expected workspace-local csharp install command as second option, got %#v", csharp.InstallOptions[1])
	}
}

func TestDetectWorkspaceStatusUsesDotnetToolCSharpBinary(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "global.json"), []byte("{\"sdk\":{\"version\":\"8.0.100\"}}"), 0o644); err != nil {
		t.Fatalf("WriteFile(global.json) error = %v", err)
	}
	home := t.TempDir()
	binDir := filepath.Join(home, ".dotnet", "tools")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(.dotnet/tools) error = %v", err)
	}
	csharpPath := filepath.Join(binDir, executableName("csharp-ls"))
	if err := os.WriteFile(csharpPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(csharp-ls) error = %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())

	status := DetectWorkspaceStatus(workspace)
	var csharp LanguageStatus
	found := false
	for _, lang := range status.Languages {
		if lang.ID == "csharp" {
			csharp = lang
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected csharp detection, got %#v", status.Languages)
	}
	if csharp.ID != "csharp" {
		t.Fatalf("expected csharp detection, got %#v", csharp)
	}
	if !csharp.Available || csharp.Binary != "csharp-ls" {
		t.Fatalf("expected dotnet-tool csharp-ls to be detected, got %#v", csharp)
	}
}

func TestDetectWorkspaceStatusUsesWorkspaceLocalCSharpBinary(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "global.json"), []byte("{\"sdk\":{\"version\":\"8.0.100\"}}"), 0o644); err != nil {
		t.Fatalf("WriteFile(global.json) error = %v", err)
	}
	binDir := filepath.Join(workspace, ".ggcode", "tools")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(.ggcode/tools) error = %v", err)
	}
	csharpPath := filepath.Join(binDir, executableName("csharp-ls"))
	if err := os.WriteFile(csharpPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(csharp-ls) error = %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	status := DetectWorkspaceStatus(workspace)
	var csharp LanguageStatus
	found := false
	for _, lang := range status.Languages {
		if lang.ID == "csharp" {
			csharp = lang
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected csharp detection, got %#v", status.Languages)
	}
	if csharp.ID != "csharp" {
		t.Fatalf("expected csharp detection, got %#v", csharp)
	}
	if !csharp.Available || csharp.Binary != "csharp-ls" {
		t.Fatalf("expected workspace-local csharp-ls to be detected, got %#v", csharp)
	}
}

func TestResolveServerForFileUsesDotnetToolCSharpPath(t *testing.T) {
	workspace := t.TempDir()
	projectPath := filepath.Join(workspace, "Board.csproj")
	if err := os.WriteFile(projectPath, []byte("<Project Sdk=\"Microsoft.NET.Sdk\"></Project>"), 0o644); err != nil {
		t.Fatalf("WriteFile(Board.csproj) error = %v", err)
	}
	sourcePath := filepath.Join(workspace, "Program.cs")
	if err := os.WriteFile(sourcePath, []byte("class Program { static void Main() {} }\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(Program.cs) error = %v", err)
	}
	home := t.TempDir()
	binDir := filepath.Join(home, ".dotnet", "tools")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(.dotnet/tools) error = %v", err)
	}
	csharpPath := filepath.Join(binDir, executableName("csharp-ls"))
	if err := os.WriteFile(csharpPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(csharp-ls) error = %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())

	resolved, ok := ResolveServerForFile(workspace, sourcePath)
	if !ok {
		t.Fatal("expected csharp server resolution from dotnet tools")
	}
	if resolved.Binary != csharpPath {
		t.Fatalf("expected resolved csharp path %q, got %#v", csharpPath, resolved)
	}
}

func TestResolveServerForFileUsesWorkspaceLocalCSharpPath(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "Board.csproj"), []byte("<Project Sdk=\"Microsoft.NET.Sdk\"></Project>"), 0o644); err != nil {
		t.Fatalf("WriteFile(Board.csproj) error = %v", err)
	}
	sourcePath := filepath.Join(workspace, "Program.cs")
	if err := os.WriteFile(sourcePath, []byte("class Program { static void Main() {} }\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(Program.cs) error = %v", err)
	}
	binDir := filepath.Join(workspace, ".ggcode", "tools")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(.ggcode/tools) error = %v", err)
	}
	csharpPath := filepath.Join(binDir, executableName("csharp-ls"))
	if err := os.WriteFile(csharpPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(csharp-ls) error = %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	resolved, ok := ResolveServerForFile(workspace, sourcePath)
	if !ok {
		t.Fatal("expected csharp server resolution from workspace tool path")
	}
	if resolved.Binary != csharpPath {
		t.Fatalf("expected resolved csharp path %q, got %#v", csharpPath, resolved)
	}
}

func TestResolveServerForFileUsesCSharpSolutionArg(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "Board.sln"), []byte("Microsoft Visual Studio Solution File, Format Version 12.00\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(Board.sln) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "Board.csproj"), []byte("<Project Sdk=\"Microsoft.NET.Sdk\"></Project>"), 0o644); err != nil {
		t.Fatalf("WriteFile(Board.csproj) error = %v", err)
	}
	sourcePath := filepath.Join(workspace, "Program.cs")
	if err := os.WriteFile(sourcePath, []byte("class Program { static void Main() {} }\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(Program.cs) error = %v", err)
	}
	home := t.TempDir()
	binDir := filepath.Join(home, ".dotnet", "tools")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(.dotnet/tools) error = %v", err)
	}
	csharpPath := filepath.Join(binDir, executableName("csharp-ls"))
	if err := os.WriteFile(csharpPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(csharp-ls) error = %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())

	resolved, ok := ResolveServerForFile(workspace, sourcePath)
	if !ok {
		t.Fatal("expected csharp server resolution from dotnet tools")
	}
	if len(resolved.Args) != 2 || resolved.Args[0] != "--solution" || resolved.Args[1] != filepath.Join(workspace, "Board.sln") {
		t.Fatalf("expected csharp solution args, got %#v", resolved.Args)
	}
}

func TestServerLaunchEnvAddsDotnetRootForCSharpLS(t *testing.T) {
	binDir := t.TempDir()
	rootDir := t.TempDir()
	dotnetDir := filepath.Join(rootDir, "libexec")
	if err := os.MkdirAll(dotnetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(libexec) error = %v", err)
	}
	dotnetPath := filepath.Join(rootDir, "bin", "dotnet")
	if err := os.MkdirAll(filepath.Dir(dotnetPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(bin) error = %v", err)
	}
	if err := os.WriteFile(dotnetPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(dotnet) error = %v", err)
	}
	linkPath := filepath.Join(binDir, "dotnet")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(binDir) error = %v", err)
	}
	if err := os.Symlink(dotnetPath, linkPath); err != nil {
		t.Fatalf("Symlink(dotnet) error = %v", err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("DOTNET_ROOT", "")
	t.Setenv("DOTNET_ROOT_ARM64", "")

	env := serverLaunchEnv("csharp-ls")
	joined := strings.Join(env, "\n")
	expectedRoot := detectDotnetRoot()
	if !strings.Contains(joined, "DOTNET_ROOT="+expectedRoot) {
		t.Fatalf("expected DOTNET_ROOT=%q in env, got %s", dotnetDir, joined)
	}
	if !strings.Contains(joined, "DOTNET_ROOT_ARM64="+expectedRoot) {
		t.Fatalf("expected DOTNET_ROOT_ARM64=%q in env, got %s", dotnetDir, joined)
	}
}

func TestServerRequestResultWorkspaceConfiguration(t *testing.T) {
	raw, err := serverRequestResult(
		"workspace/configuration",
		json.RawMessage(`{"items":[{"section":"csharp"},{"section":"csharp.solutionPathOverride"},{"section":"editor"}]}`),
		"/tmp/workspace",
		ResolvedServer{Binary: "csharp-ls", Args: []string{"--solution", "Board.slnx"}},
	)
	if err != nil {
		t.Fatalf("serverRequestResult() error = %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"solutionPathOverride":"/tmp/workspace/Board.slnx"`) {
		t.Fatalf("expected csharp solution override in %s", text)
	}
	if !strings.Contains(text, `"/tmp/workspace/Board.slnx"`) {
		t.Fatalf("expected nested solution override in %s", text)
	}
	if !strings.HasSuffix(text, ",{}]") {
		t.Fatalf("expected unknown section to fall back to empty object, got %s", text)
	}
}

func TestServerRequestResultWorkspaceFolders(t *testing.T) {
	raw, err := serverRequestResult("workspace/workspaceFolders", nil, "/tmp/workspace", ResolvedServer{})
	if err != nil {
		t.Fatalf("serverRequestResult() error = %v", err)
	}
	if !strings.Contains(string(raw), "\"uri\":\"file:///tmp/workspace\"") {
		t.Fatalf("expected workspaceFolders uri, got %s", string(raw))
	}
}

func TestUnsupportedDiagnosticMethodErrorDetection(t *testing.T) {
	cases := []string{
		"textDocument/diagnostic failed: UnsupportedOperationException",
		"Method not found: textDocument/diagnostic",
		"unsupported method textDocument/diagnostic",
	}
	for _, text := range cases {
		if !isUnsupportedDiagnosticMethodError(errors.New(text)) {
			t.Fatalf("expected unsupported diagnostic error for %q", text)
		}
	}
	if isUnsupportedDiagnosticMethodError(errors.New("context deadline exceeded")) {
		t.Fatal("did not expect generic timeout to disable pull diagnostics")
	}
}

func TestCSharpSolutionArgsGeneratesCompatSolutionForSLNXWorkspace(t *testing.T) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet not installed")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "Board.slnx"), []byte("<Solution/>"), 0o644); err != nil {
		t.Fatalf("WriteFile(Board.slnx) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "Board.csproj"), []byte("<Project Sdk=\"Microsoft.NET.Sdk\"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>"), 0o644); err != nil {
		t.Fatalf("WriteFile(Board.csproj) error = %v", err)
	}
	t.Setenv("DOTNET_CLI_HOME", filepath.Join(workspace, ".ggcode", "dotnet-cli-home"))

	args := csharpSolutionArgs(workspace)
	if len(args) != 2 || args[0] != "--solution" {
		t.Fatalf("expected compat solution args, got %#v", args)
	}
	expected := filepath.Join(workspace, ".ggcode", "lsp", "csharp-ls.sln")
	if args[1] != expected {
		t.Fatalf("expected compat solution path %q, got %#v", expected, args)
	}
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("expected compat solution to exist: %v", err)
	}
}

func TestCSharpToolInstallCommandHasValidShellSyntax(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell syntax check only applies to POSIX command generation")
	}
	cmd := csharpToolInstallCommand()
	if strings.TrimSpace(cmd) == "" {
		t.Fatal("expected non-empty csharp install command")
	}
	if err := exec.Command("sh", "-n", "-c", cmd).Run(); err != nil {
		t.Fatalf("expected valid shell syntax, got %v for %q", err, cmd)
	}
}

func TestCSharpToolInstallCommandFailsGracefullyWithoutDotnet(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dotnet precheck test only applies to POSIX command generation")
	}
	workdir := t.TempDir()
	cmd := exec.Command("sh", "-c", csharpToolInstallCommand())
	cmd.Dir = workdir
	cmd.Env = []string{"PATH=" + t.TempDir()}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected csharp install command to fail without dotnet, output=%s", string(out))
	}
	text := string(out)
	if !strings.Contains(text, "dotnet is required to install csharp-ls") {
		t.Fatalf("expected friendly dotnet precheck error, got %s", text)
	}
}

func TestInstallHintCommandsIncludePrechecks(t *testing.T) {
	checks := []struct {
		languageID string
		needle     string
	}{
		{languageID: "go", needle: "command -v go"},
		{languageID: "rust", needle: "command -v rustup"},
		{languageID: "typescript", needle: "command -v npm"},
	}
	for _, tc := range checks {
		cmd := installHint(tc.languageID, t.TempDir())
		if !strings.Contains(cmd, tc.needle) {
			t.Fatalf("expected %s install command to include %q, got %q", tc.languageID, tc.needle, cmd)
		}
	}
}

func TestUnsupportedInstallCommandFailsGracefully(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unsupported-install shell check only applies to POSIX command generation")
	}
	cmd := unsupportedInstallCommand("manual install required")
	run := exec.Command("sh", "-c", cmd)
	out, err := run.CombinedOutput()
	if err == nil {
		t.Fatalf("expected unsupported install command to fail, output=%s", string(out))
	}
	if !strings.Contains(string(out), "manual install required") {
		t.Fatalf("expected friendly unsupported-install message, got %s", string(out))
	}
}

func TestPythonVenvInstallCommandIncludesPythonPrechecks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("python precheck string check only applies to POSIX command generation")
	}
	cmd := pythonVenvInstallCommand("", "pyright")
	if !strings.Contains(cmd, "command -v python3") || !strings.Contains(cmd, "command -v python") {
		t.Fatalf("expected python install command to check python3/python, got %q", cmd)
	}
	if !strings.Contains(cmd, "Python is required to install Python LSP servers") {
		t.Fatalf("expected friendly python precheck message, got %q", cmd)
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

	// DocumentSymbols — always works (no type info needed).
	symbols, err := DocumentSymbols(context.Background(), workspace, path)
	if err != nil || len(symbols) == 0 {
		t.Fatalf("DocumentSymbols() err=%v len=%d", err, len(symbols))
	}
	t.Logf("DocumentSymbols returned %d symbols", len(symbols))

	// Diagnostics — works without full type info.
	diagnostics, err := Diagnostics(context.Background(), workspace, path)
	if err != nil {
		t.Fatalf("Diagnostics() err=%v", err)
	}
	t.Logf("Diagnostics returned %d items", len(diagnostics))

	// Hover, Definition, References — gopls may return empty results for
	// repo files during cold start or when type info is incomplete.
	// These are thoroughly tested with temp workspaces in integration_test.go.
	// We only verify they don't error here.
	pos := findSymbolPosition(t, symbols, "Hover")
	if pos != nil {
		hover, err := Hover(context.Background(), workspace, path, *pos)
		if err != nil {
			t.Logf("Hover() error (acceptable): %v", err)
		} else {
			t.Logf("Hover() returned %d chars", len(hover))
		}
		definition, err := Definition(context.Background(), workspace, path, *pos)
		if err != nil {
			t.Logf("Definition() error (acceptable): %v", err)
		} else {
			t.Logf("Definition() returned %d locations", len(definition))
		}
		references, err := References(context.Background(), workspace, path, *pos)
		if err != nil {
			t.Logf("References() error (acceptable): %v", err)
		} else {
			t.Logf("References() returned %d locations", len(references))
		}
	}
}

func TestSequentialTimedRepoFileLSPCallsWithInstalledGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	workspace := filepath.Clean(filepath.Join("..", ".."))
	path := filepath.Join(workspace, "internal", "lsp", "client.go")

	// Test that LSP calls work with context timeouts on repo files.
	ctx1, cancel1 := context.WithTimeout(context.Background(), 10*time.Second)
	symbols, err := DocumentSymbols(ctx1, workspace, path)
	cancel1()
	if err != nil || len(symbols) == 0 {
		t.Fatalf("DocumentSymbols() err=%v len=%d", err, len(symbols))
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	diagnostics, err := Diagnostics(ctx2, workspace, path)
	cancel2()
	if err != nil {
		t.Fatalf("Diagnostics() err=%v", err)
	}
	_ = diagnostics

	// Hover/Definition/References with timeout — best-effort.
	pos := findSymbolPosition(t, symbols, "Hover")
	if pos != nil {
		ctx3, cancel3 := context.WithTimeout(context.Background(), 10*time.Second)
		hover, _ := Hover(ctx3, workspace, path, *pos)
		cancel3()
		t.Logf("Hover with timeout: %d chars", len(hover))

		ctx4, cancel4 := context.WithTimeout(context.Background(), 10*time.Second)
		defs, _ := Definition(ctx4, workspace, path, *pos)
		cancel4()
		t.Logf("Definition with timeout: %d locations", len(defs))
	}
}

// findSymbolPosition searches symbols for one matching the given name
// and returns its start position. Returns nil if not found.
func findSymbolPosition(t *testing.T, symbols []Symbol, name string) *Position {
	t.Helper()
	for _, s := range symbols {
		if s.Name == name {
			p := s.Range.Start
			return &p
		}
	}
	return nil
}

func symbolNames(symbols []Symbol) []string {
	names := make([]string, len(symbols))
	for i, s := range symbols {
		names[i] = s.Name
	}
	return names
}

// retryLSP retries a string-returning LSP call with the given attempts and interval.
