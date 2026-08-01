package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCrossPackageDiagnostics_NoServer(t *testing.T) {
	// With no LSP server available, should return nil, nil.
	diags, err := CrossPackageDiagnostics(t.Context(), "/nonexistent/workspace", "/nonexistent/workspace/foo.go")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if diags != nil {
		t.Fatalf("expected nil diagnostics, got %v", diags)
	}
}

func TestCrossPackageDiagnostics_NonGoFile(t *testing.T) {
	diags, err := CrossPackageDiagnostics(t.Context(), "/tmp", "/tmp/foo.ts")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if diags != nil {
		t.Fatalf("expected nil for non-Go file, got %v", diags)
	}
}

func TestGoPackageImportPath(t *testing.T) {
	// Create a temp module structure.
	tmp := t.TempDir()
	moduleDir := filepath.Join(tmp, "mymod")
	pkgDir := filepath.Join(moduleDir, "internal", "agent")
	os.MkdirAll(pkgDir, 0755)

	// Write go.mod.
	os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte("module github.com/example/mymod\n\ngo 1.23\n"), 0644)

	// Write a Go file in the package.
	goFile := filepath.Join(pkgDir, "agent.go")
	os.WriteFile(goFile, []byte("package agent\n"), 0644)

	pkgPath, ok := goPackageImportPath(moduleDir, goFile)
	if !ok {
		t.Fatal("expected ok=true")
	}
	expected := "github.com/example/mymod/internal/agent"
	if pkgPath != expected {
		t.Fatalf("expected %q, got %q", expected, pkgPath)
	}
}

func TestGoPackageImportPath_NoGoMod(t *testing.T) {
	tmp := t.TempDir()
	goFile := filepath.Join(tmp, "pkg", "main.go")
	os.MkdirAll(filepath.Dir(goFile), 0755)
	os.WriteFile(goFile, []byte("package main\n"), 0644)

	_, ok := goPackageImportPath(tmp, goFile)
	if ok {
		t.Fatal("expected ok=false when no go.mod found")
	}
}

func TestGoPackageImportPath_RootPackage(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.com/root\n\ngo 1.23\n"), 0644)
	goFile := filepath.Join(tmp, "main.go")
	os.WriteFile(goFile, []byte("package main\n"), 0644)

	pkgPath, ok := goPackageImportPath(tmp, goFile)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if pkgPath != "example.com/root" {
		t.Fatalf("expected root package path, got %q", pkgPath)
	}
}

func TestParseGoModModule(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "simple",
			content: "module github.com/example/myproject\n\ngo 1.23\n",
			want:    "github.com/example/myproject",
		},
		{
			name:    "with require",
			content: "module example.com/foo\n\nrequire (\n\tbar v1.0.0\n)\n",
			want:    "example.com/foo",
		},
		{
			name:    "no module line",
			content: "go 1.23\n",
			want:    "",
		},
		{
			name:    "empty",
			content: "",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGoModModule(tt.content)
			if got != tt.want {
				t.Errorf("parseGoModModule() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFileImportsPackage(t *testing.T) {
	tmp := t.TempDir()
	tests := []struct {
		name     string
		content  string
		pkgPath  string
		expected bool
	}{
		{
			name: "single import",
			content: `package main

import "github.com/example/mymod/internal/agent"
`,
			pkgPath:  "github.com/example/mymod/internal/agent",
			expected: true,
		},
		{
			name: "block import",
			content: `package main

import (
	"fmt"
	"github.com/example/mymod/internal/agent"
)
`,
			pkgPath:  "github.com/example/mymod/internal/agent",
			expected: true,
		},
		{
			name: "no match",
			content: `package main

import (
	"fmt"
	"os"
)
`,
			pkgPath:  "github.com/example/mymod/internal/agent",
			expected: false,
		},
		{
			name: "substring not match",
			content: `package main

import "github.com/example/mymod/internal/agenttest"
`,
			pkgPath:  "github.com/example/mymod/internal/agent",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := filepath.Join(tmp, tt.name+".go")
			os.WriteFile(fp, []byte(tt.content), 0644)
			got := fileImportsPackage(fp, tt.pkgPath)
			if got != tt.expected {
				t.Errorf("fileImportsPackage() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestFileImportsPackage_NonExistentFile(t *testing.T) {
	got := fileImportsPackage("/nonexistent/file.go", "some/package")
	if got {
		t.Fatal("expected false for non-existent file")
	}
}

func TestSortCandidatesByErrorCount(t *testing.T) {
	candidates := []crossPackageCandidate{
		{path: "a.go", diag: []Diagnostic{{Severity: 1}}},
		{path: "b.go", diag: []Diagnostic{{Severity: 1}, {Severity: 1}, {Severity: 1}}},
		{path: "c.go", diag: []Diagnostic{{Severity: 1}, {Severity: 1}}},
	}
	sortCandidatesByErrorCount(candidates)
	if candidates[0].path != "b.go" {
		t.Fatalf("expected b.go first (3 errors), got %s", candidates[0].path)
	}
	if candidates[1].path != "c.go" {
		t.Fatalf("expected c.go second (2 errors), got %s", candidates[1].path)
	}
	if candidates[2].path != "a.go" {
		t.Fatalf("expected a.go third (1 error), got %s", candidates[2].path)
	}
}

func TestCrossPackageDiagnostics_Integration(t *testing.T) {
	// Create a mini Go module with two packages.
	tmp := t.TempDir()
	moduleDir := filepath.Join(tmp, "mymod")
	pkgADir := filepath.Join(moduleDir, "pkg", "a")
	pkgBDir := filepath.Join(moduleDir, "pkg", "b")
	os.MkdirAll(pkgADir, 0755)
	os.MkdirAll(pkgBDir, 0755)

	// Write go.mod.
	os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte("module github.com/example/mymod\n\ngo 1.23\n"), 0644)

	// Write package a (the edited package).
	fileA := filepath.Join(pkgADir, "a.go")
	os.WriteFile(fileA, []byte("package a\n\nfunc Foo() {}\n"), 0644)

	// Write package b (depends on a).
	fileB := filepath.Join(pkgBDir, "b.go")
	os.WriteFile(fileB, []byte("package b\n\nimport \"github.com/example/mymod/pkg/a\"\n\nfunc Bar() { a.Foo() }\n"), 0644)

	// Verify package import path derivation.
	pkgPath, ok := goPackageImportPath(moduleDir, fileA)
	if !ok {
		t.Fatal("expected ok=true for package path derivation")
	}
	expectedPkg := "github.com/example/mymod/pkg/a"
	if pkgPath != expectedPkg {
		t.Fatalf("expected %q, got %q", expectedPkg, pkgPath)
	}

	// Verify import detection.
	if !fileImportsPackage(fileB, pkgPath) {
		t.Fatal("expected fileB to import package a")
	}

	// Verify that fileA itself is NOT detected as importing its own package.
	if fileImportsPackage(fileA, pkgPath) {
		t.Fatal("fileA should not import its own package")
	}
}

func TestUriToPath(t *testing.T) {
	tests := []struct {
		uri  string
		want string
	}{
		{"file:///tmp/foo.go", "/tmp/foo.go"},
		{"file:///home/user/bar.go", "/home/user/bar.go"},
	}

	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			got := uriToPath(tt.uri)
			if got != tt.want {
				t.Errorf("uriToPath(%q) = %q, want %q", tt.uri, got, tt.want)
			}
		})
	}
}
