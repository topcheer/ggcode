package agentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildGoPackageSymbolsSection_Basic(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example\n")
	mustWrite(t, filepath.Join(root, "main.go"), "package main\nfunc main() {}\n")
	mustMkdir(t, filepath.Join(root, "pkg"))
	mustWrite(t, filepath.Join(root, "pkg", "service.go"), `package pkg

type Server struct { addr string }

type Handler interface { Handle() }

func NewServer() *Server { return nil }
func Shutdown() {}
`)

	out := buildGoPackageSymbolsSection(root)
	if out == "" {
		t.Fatal("expected non-empty symbol section for Go project")
	}
	for _, want := range []string{"## Package symbols", "Server", "Handler", "NewServer()", "Shutdown()"} {
		if !strings.Contains(out, want) {
			t.Errorf("symbol section missing %q:\n%s", want, out)
		}
	}
	// Methods should NOT appear (only top-level functions)
	if strings.Contains(out, "Handle()") {
		t.Errorf("methods should be excluded:\n%s", out)
	}
}

func TestBuildGoPackageSymbolsSection_NonGoProject(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "README.md"), "hello\n")
	mustMkdir(t, filepath.Join(root, "src"))
	mustWrite(t, filepath.Join(root, "src", "main.py"), "print('hello')\n")

	out := buildGoPackageSymbolsSection(root)
	if out != "" {
		t.Errorf("expected empty for non-Go project, got:\n%s", out)
	}
}

func TestBuildGoPackageSymbolsSection_NestedPackages(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example\n")
	// Depth-1 package
	mustMkdir(t, filepath.Join(root, "cmd"))
	mustWrite(t, filepath.Join(root, "cmd", "app.go"), "package cmd\nfunc Run() {}\n")
	// Depth-2 package
	mustMkdir(t, filepath.Join(root, "internal", "agent"))
	mustWrite(t, filepath.Join(root, "internal", "agent", "agent.go"), "package agent\ntype Agent struct{}\nfunc New() *Agent { return nil }\n")

	out := buildGoPackageSymbolsSection(root)
	if !strings.Contains(out, "cmd/") {
		t.Errorf("expected cmd/ package:\n%s", out)
	}
	if !strings.Contains(out, "internal/agent/") {
		t.Errorf("expected internal/agent/ package:\n%s", out)
	}
	if !strings.Contains(out, "Run()") || !strings.Contains(out, "Agent") || !strings.Contains(out, "New()") {
		t.Errorf("expected symbols Run(), Agent, New():\n%s", out)
	}
}

func TestBuildGoPackageSymbolsSection_TestFilesExcluded(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example\n")
	mustWrite(t, filepath.Join(root, "pkg.go"), "package main\nfunc RealFunc() {}\n")
	mustWrite(t, filepath.Join(root, "pkg_test.go"), "package main\nfunc TestHelper() {}\n")

	out := buildGoPackageSymbolsSection(root)
	if !strings.Contains(out, "RealFunc()") {
		t.Errorf("expected RealFunc():\n%s", out)
	}
	if strings.Contains(out, "TestHelper()") {
		t.Errorf("test-only functions should be excluded:\n%s", out)
	}
}

func TestBuildGoPackageSymbolsSection_SkipsNoiseDirs(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example\n")
	// vendor should be skipped
	mustMkdir(t, filepath.Join(root, "vendor", "lib"))
	mustWrite(t, filepath.Join(root, "vendor", "lib", "v.go"), "package lib\nfunc VendorFunc() {}\n")
	// Real package
	mustMkdir(t, filepath.Join(root, "pkg"))
	mustWrite(t, filepath.Join(root, "pkg", "p.go"), "package pkg\nfunc RealFunc() {}\n")

	out := buildGoPackageSymbolsSection(root)
	if strings.Contains(out, "VendorFunc") || strings.Contains(out, "vendor") {
		t.Errorf("vendor should be excluded:\n%s", out)
	}
	if !strings.Contains(out, "RealFunc()") {
		t.Errorf("expected RealFunc() from pkg:\n%s", out)
	}
}

func TestBuildGoPackageSymbolsSection_UnexportedExcluded(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example\n")
	mustWrite(t, filepath.Join(root, "p.go"), `package main

type Exported struct {}
type unexported struct {}
func ExportedFunc() {}
func helperFunc() {}
`)

	out := buildGoPackageSymbolsSection(root)
	if !strings.Contains(out, "Exported") {
		t.Errorf("expected Exported type:\n%s", out)
	}
	if !strings.Contains(out, "ExportedFunc()") {
		t.Errorf("expected ExportedFunc():\n%s", out)
	}
	// unexported should not appear (but "Exported" contains substring, so check exact)
	for _, line := range strings.Split(out, "\n") {
		// Each symbol is separated by ", " or is inside parentheses
		// Check that "unexported" doesn't appear as a standalone symbol
		if strings.Contains(line, "unexported") || strings.Contains(line, "helperFunc") {
			t.Errorf("unexported symbols should be excluded, found in: %s", line)
		}
	}
}

func TestBuildGoPackageSymbolsSection_ParseErrorGraceful(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example\n")
	// A file with syntax errors
	mustWrite(t, filepath.Join(root, "broken.go"), "package main\nfunc broken(\n")
	// A valid file in the same dir
	mustWrite(t, filepath.Join(root, "good.go"), "package main\nfunc Good() {}\n")

	out := buildGoPackageSymbolsSection(root)
	// Should still pick up Good() from the valid file despite parse errors
	if !strings.Contains(out, "Good()") {
		t.Errorf("expected Good() despite parse errors:\n%s", out)
	}
}

func TestBuildGoPackageSymbolsSection_EmptyDir(t *testing.T) {
	if out := buildGoPackageSymbolsSection(t.TempDir()); out != "" {
		t.Errorf("expected empty for non-Go empty dir, got: %s", out)
	}
	if out := buildGoPackageSymbolsSection(""); out != "" {
		t.Errorf("expected empty for empty root, got: %s", out)
	}
}

func TestBuildGoPackageSymbolsSection_FileCount(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example\n")
	mustWrite(t, filepath.Join(root, "a.go"), "package main\ntype A struct{}\n")
	mustWrite(t, filepath.Join(root, "b.go"), "package main\ntype B struct{}\n")
	mustWrite(t, filepath.Join(root, "c_test.go"), "package main\nfunc TestC() {}\n")

	out := buildGoPackageSymbolsSection(root)
	// Should show 2 files (excluding test file)
	if !strings.Contains(out, "2 files") {
		t.Errorf("expected 2 files in output:\n%s", out)
	}
	if strings.Contains(out, "3 files") {
		t.Errorf("test files should not be counted:\n%s", out)
	}
}

func TestHasGoSource(t *testing.T) {
	// Go project
	goRoot := t.TempDir()
	mustWrite(t, filepath.Join(goRoot, "go.mod"), "module x\n")
	mustWrite(t, filepath.Join(goRoot, "main.go"), "package main\n")
	if !hasGoSource(goRoot) {
		t.Error("expected true for Go project")
	}

	// Non-Go project
	nonGo := t.TempDir()
	mustWrite(t, filepath.Join(nonGo, "README.md"), "hi\n")
	if hasGoSource(nonGo) {
		t.Error("expected false for non-Go project")
	}

	// Go file in subdir
	subGo := t.TempDir()
	mustMkdir(t, filepath.Join(subGo, "pkg"))
	mustWrite(t, filepath.Join(subGo, "pkg", "p.go"), "package pkg\n")
	if !hasGoSource(subGo) {
		t.Error("expected true for Go file in subdir")
	}
}

func TestDirHasGoFiles(t *testing.T) {
	dir := t.TempDir()
	if dirHasGoFiles(dir) {
		t.Error("expected false for empty dir")
	}
	mustWrite(t, dir+"/"+"test_only_test.go", "package x\n")
	if dirHasGoFiles(dir) {
		t.Error("expected false for dir with only test files")
	}
	mustWrite(t, dir+"/"+"real.go", "package x\n")
	if !dirHasGoFiles(dir) {
		t.Error("expected true for dir with .go file")
	}
}

func TestExtractPackageSymbols_Dedup(t *testing.T) {
	dir := t.TempDir()
	// Same symbol name in two files of the same package should be listed once
	mustWrite(t, filepath.Join(dir, "a.go"), "package x\ntype Foo struct{}\nfunc Bar() {}\n")
	mustWrite(t, filepath.Join(dir, "b.go"), "package x\nfunc Bar() {}\n")

	syms, fc := extractPackageSymbols(dir)
	if fc != 2 {
		t.Errorf("expected 2 files, got %d", fc)
	}
	// Bar() should appear only once
	count := 0
	for _, s := range syms {
		if s == "Bar()" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected Bar() once, got %d times in %v", count, syms)
	}
	// Foo should be there
	found := false
	for _, s := range syms {
		if s == "Foo" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Foo in symbols: %v", syms)
	}
}

// Ensure os import is used (referenced in extractPackageSymbols signature)
var _ = os.Stat
