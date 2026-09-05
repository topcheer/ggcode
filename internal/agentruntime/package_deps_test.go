package agentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildPackageDepsSection_NonGoProject(t *testing.T) {
	dir := t.TempDir()
	got := buildPackageDepsSection(dir)
	if got != "" {
		t.Fatalf("expected empty string for non-Go project, got: %q", got)
	}
}

func TestBuildPackageDepsSection_NoGoMod(t *testing.T) {
	dir := t.TempDir()
	writePkgFile(t, filepath.Join(dir, "main.go"), "package main\nfunc main() {}\n")
	got := buildPackageDepsSection(dir)
	if got != "" {
		t.Fatalf("expected empty string for project without go.mod, got: %q", got)
	}
}

func TestBuildPackageDepsSection_SinglePackage(t *testing.T) {
	dir := t.TempDir()
	writeMod(t, dir, "example.com/test")
	writePkgFile(t, filepath.Join(dir, "main.go"), "package main\nfunc main() {}\n")
	got := buildPackageDepsSection(dir)
	if got != "" {
		t.Fatalf("expected empty for single package, got: %q", got)
	}
}

func TestBuildPackageDepsSection_HubPackage(t *testing.T) {
	dir := t.TempDir()
	writeMod(t, dir, "example.com/myproj")

	// pkg/util - a hub package imported by 3 others.
	writePkgFile(t, filepath.Join(dir, "pkg/util/util.go"),
		"package util\n\nfunc Helper() {}\n")
	writePkgFile(t, filepath.Join(dir, "pkg/a/a.go"),
		"package a\n\nimport \"example.com/myproj/pkg/util\"\n\nfunc A() { util.Helper() }\n")
	writePkgFile(t, filepath.Join(dir, "pkg/b/b.go"),
		"package b\n\nimport \"example.com/myproj/pkg/util\"\n\nfunc B() { util.Helper() }\n")
	writePkgFile(t, filepath.Join(dir, "pkg/c/c.go"),
		"package c\n\nimport \"example.com/myproj/pkg/util\"\n\nfunc C() { util.Helper() }\n")

	got := buildPackageDepsSection(dir)
	if got == "" {
		t.Fatal("expected non-empty deps section for multi-package project")
	}
	if !strings.Contains(got, "Package dependencies") {
		t.Errorf("expected 'Package dependencies' header, got: %q", got)
	}
	if !strings.Contains(got, "pkg/util") {
		t.Errorf("expected pkg/util in output, got: %q", got)
	}
	if !strings.Contains(got, "\u21903") {
		t.Errorf("expected fan-in indicator in output, got: %q", got)
	}
}

func TestBuildPackageDepsSection_LowFanInFiltered(t *testing.T) {
	dir := t.TempDir()
	writeMod(t, dir, "example.com/myproj")

	writePkgFile(t, filepath.Join(dir, "pkg/util/util.go"),
		"package util\n\nfunc Helper() {}\n")
	writePkgFile(t, filepath.Join(dir, "pkg/a/a.go"),
		"package a\n\nimport \"example.com/myproj/pkg/util\"\n\nfunc A() { util.Helper() }\n")

	got := buildPackageDepsSection(dir)
	// Only 1 importer, below depGraphMinFanIn=2.
	if got != "" {
		t.Fatalf("expected empty for low fan-in (1), got: %q", got)
	}
}

func TestBuildPackageDepsSection_ExternalImportsIgnored(t *testing.T) {
	dir := t.TempDir()
	writeMod(t, dir, "example.com/myproj")

	writePkgFile(t, filepath.Join(dir, "pkg/a/a.go"),
		"package a\n\nimport \"fmt\"\n\nfunc A() { fmt.Println(\"hi\") }\n")
	writePkgFile(t, filepath.Join(dir, "pkg/b/b.go"),
		"package b\n\nimport \"fmt\"\n\nfunc B() { fmt.Println(\"hi\") }\n")

	got := buildPackageDepsSection(dir)
	if got != "" {
		t.Fatalf("expected empty for external-only imports, got: %q", got)
	}
}

func TestReadModulePath(t *testing.T) {
	dir := t.TempDir()
	writeMod(t, dir, "github.com/foo/bar")
	got := readModulePath(dir)
	if got != "github.com/foo/bar" {
		t.Errorf("expected 'github.com/foo/bar', got %q", got)
	}
}

func TestReadModulePath_NoGoMod(t *testing.T) {
	dir := t.TempDir()
	got := readModulePath(dir)
	if got != "" {
		t.Errorf("expected empty for missing go.mod, got %q", got)
	}
}

func TestParsePackageImports(t *testing.T) {
	dir := t.TempDir()
	writePkgFile(t, filepath.Join(dir, "f.go"), `package test

import (
	"fmt"
	"strings"

	"example.com/myproj/pkg/util"
)

var _ = fmt.Sprintf
var _ = strings.Builder{}
var _ = util.Helper
`)

	imports := parsePackageImports(dir)
	wantSet := map[string]bool{
		"fmt":                         true,
		"strings":                     true,
		"example.com/myproj/pkg/util": true,
	}
	if len(imports) != len(wantSet) {
		t.Fatalf("expected %d imports, got %d: %v", len(wantSet), len(imports), imports)
	}
	for _, imp := range imports {
		if !wantSet[imp] {
			t.Errorf("unexpected import: %q", imp)
		}
	}
}

// --- local helpers (prefixed to avoid collision with project_commands_test.go) ---

func writePkgFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeMod(t *testing.T, dir, module string) {
	t.Helper()
	writePkgFile(t, filepath.Join(dir, "go.mod"), "module "+module+"\n\ngo 1.26\n")
}

// Regression for #1489: a trailing // comment on the module line (legal per
// x/mod's modfile lexer) used to leak into the module path, making every
// import key mismatch and silently dropping the package-deps section.
func TestReadModulePath_TrailingComment(t *testing.T) {
	dir := t.TempDir()
	writeMod(t, dir, "github.com/foo/bar // internal fork")
	if got := readModulePath(dir); got != "github.com/foo/bar" {
		t.Fatalf("module path with trailing comment = %q, want github.com/foo/bar", got)
	}
}
