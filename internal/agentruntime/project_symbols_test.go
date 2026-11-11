package agentruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goSymbolDump runs the on-demand provider (the P3 retrieval path) and
// flattens its output for substring assertions - the extraction-semantics
// tests moved here from the old inline-section assertions (#5446).
func goSymbolDump(t *testing.T, root string) string {
	t.Helper()
	pkgs, err := workspaceSymbols{root: root}.Symbols("go", "", 50)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	var b strings.Builder
	for _, p := range pkgs {
		fmt.Fprintf(&b, "%s: %s\n", p.Path, strings.Join(p.Symbols, ", "))
	}
	return b.String()
}

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

	// Section carries counts + tool pointer only (#5446 P3); the semantic
	// assertions moved to the provider dump below.
	out := buildGoPackageSymbolsSection(root)
	if out == "" {
		t.Fatal("expected non-empty symbol section for Go project")
	}
	for _, want := range []string{"## Package symbols", "Go packages", "symbols", "lsp_workspace_symbols"} {
		if !strings.Contains(out, want) {
			t.Errorf("section missing %q:\n%s", want, out)
		}
	}
	for _, body := range []string{"NewServer()", "Shutdown()", "Handle()"} {
		if strings.Contains(out, body) {
			t.Errorf("section must not inline symbol bodies (%q):\n%s", body, out)
		}
	}

	dump := goSymbolDump(t, root)
	for _, want := range []string{"Server", "Handler", "NewServer()", "Shutdown()"} {
		if !strings.Contains(dump, want) {
			t.Errorf("provider dump missing %q:\n%s", want, dump)
		}
	}
	if strings.Contains(dump, "Handle()") {
		t.Errorf("methods should be excluded:\n%s", dump)
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

	mustMkdir(t, filepath.Join(root, "cmd"))
	mustWrite(t, filepath.Join(root, "cmd", "app.go"), "package cmd\nfunc Run() {}\n")

	mustMkdir(t, filepath.Join(root, "internal", "agent"))
	mustWrite(t, filepath.Join(root, "internal", "agent", "agent.go"), "package agent\ntype Agent struct{}\nfunc New() *Agent { return nil }\n")

	dump := goSymbolDump(t, root)
	if !strings.Contains(dump, "cmd:") {
		t.Errorf("expected cmd package:\n%s", dump)
	}
	if !strings.Contains(dump, "internal/agent:") {
		t.Errorf("expected internal/agent package:\n%s", dump)
	}
	if !strings.Contains(dump, "Run()") || !strings.Contains(dump, "Agent") || !strings.Contains(dump, "New()") {
		t.Errorf("expected symbols Run(), Agent, New():\n%s", dump)
	}
}

func TestBuildGoPackageSymbolsSection_TestFilesExcluded(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example\n")
	mustWrite(t, filepath.Join(root, "pkg.go"), "package main\nfunc RealFunc() {}\n")
	mustWrite(t, filepath.Join(root, "pkg_test.go"), "package main\nfunc TestHelper() {}\n")

	dump := goSymbolDump(t, root)
	if !strings.Contains(dump, "RealFunc()") {
		t.Errorf("expected RealFunc():\n%s", dump)
	}
	if strings.Contains(dump, "TestHelper()") {
		t.Errorf("test-only functions should be excluded:\n%s", dump)
	}
}

func TestBuildGoPackageSymbolsSection_SkipsNoiseDirs(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example\n")

	mustMkdir(t, filepath.Join(root, "vendor", "lib"))
	mustWrite(t, filepath.Join(root, "vendor", "lib", "v.go"), "package lib\nfunc VendorFunc() {}\n")

	mustMkdir(t, filepath.Join(root, "pkg"))
	mustWrite(t, filepath.Join(root, "pkg", "p.go"), "package pkg\nfunc RealFunc() {}\n")

	dump := goSymbolDump(t, root)
	if strings.Contains(dump, "VendorFunc") || strings.Contains(dump, "vendor") {
		t.Errorf("vendor should be excluded:\n%s", dump)
	}
	if !strings.Contains(dump, "RealFunc()") {
		t.Errorf("expected RealFunc() from pkg:\n%s", dump)
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

	dump := goSymbolDump(t, root)
	if !strings.Contains(dump, "Exported") {
		t.Errorf("expected Exported type:\n%s", dump)
	}
	if !strings.Contains(dump, "ExportedFunc()") {
		t.Errorf("expected ExportedFunc():\n%s", dump)
	}

	for _, line := range strings.Split(dump, "\n") {

		if strings.Contains(line, "unexported") || strings.Contains(line, "helperFunc") {
			t.Errorf("unexported symbols should be excluded, found in: %s", line)
		}
	}
}

func TestBuildGoPackageSymbolsSection_ParseErrorGraceful(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example\n")

	mustWrite(t, filepath.Join(root, "broken.go"), "package main\nfunc broken(\n")

	mustWrite(t, filepath.Join(root, "good.go"), "package main\nfunc Good() {}\n")

	dump := goSymbolDump(t, root)

	if !strings.Contains(dump, "Good()") {
		t.Errorf("expected Good() despite parse errors:\n%s", dump)
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

	// File counts left the prompt surface with P3; pin at the extractor.
	_, fc := extractPackageSymbols(root)
	if fc != 2 {
		t.Errorf("expected 2 files counted, got %d", fc)
	}
}

func TestHasGoSource(t *testing.T) {

	goRoot := t.TempDir()
	mustWrite(t, filepath.Join(goRoot, "go.mod"), "module x\n")
	mustWrite(t, filepath.Join(goRoot, "main.go"), "package main\n")
	if !hasGoSource(goRoot) {
		t.Error("expected true for Go project")
	}

	nonGo := t.TempDir()
	mustWrite(t, filepath.Join(nonGo, "README.md"), "hi\n")
	if hasGoSource(nonGo) {
		t.Error("expected false for non-Go project")
	}

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

	mustWrite(t, filepath.Join(dir, "a.go"), "package x\ntype Foo struct{}\nfunc Bar() {}\n")
	mustWrite(t, filepath.Join(dir, "b.go"), "package x\nfunc Bar() {}\n")

	syms, fc := extractPackageSymbols(dir)
	if fc != 2 {
		t.Errorf("expected 2 files, got %d", fc)
	}

	count := 0
	for _, s := range syms {
		if s == "Bar()" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected Bar() once, got %d times in %v", count, syms)
	}

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

var _ = os.Stat
