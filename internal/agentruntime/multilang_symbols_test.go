package agentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildTSSymbolsSection_BasicExports(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "src", "components")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "Button.tsx"), []byte(`
import React from "react";

export interface ButtonProps {
	label: string;
	onClick: () => void;
}

export const DEFAULT_SIZE = "md";

export function Button(props: ButtonProps) {
	return null;
}

export class ButtonManager {
	handle() {}
}

const internal = "not exported";
`), 0644); err != nil {
		t.Fatal(err)
	}

	out := buildTSSymbolsSection(dir)
	if out == "" {
		t.Fatal("expected non-empty symbol section for TS project")
	}

	checks := map[string]bool{
		"Button()":      false,
		"ButtonManager": false,
		"ButtonProps":   false,
		"DEFAULT_SIZE":  false,
	}
	for k := range checks {
		if strings.Contains(out, k) {
			checks[k] = true
		}
	}
	for sym, found := range checks {
		if !found {
			t.Errorf("expected symbol %q in output:\n%s", sym, out)
		}
	}

	if strings.Contains(out, "internal") {
		t.Errorf("non-exported 'internal' should not appear:\n%s", out)
	}
}

func TestBuildTSSymbolsSection_SkipsTestAndDeclFiles(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "lib")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(pkgDir, "utils.ts"), "export function helper() {}\n")
	mustWriteFile(t, filepath.Join(pkgDir, "utils.test.ts"), "export function shouldNotAppear() {}\n")
	mustWriteFile(t, filepath.Join(pkgDir, "api.spec.ts"), "export function alsoNotAppearing() {}\n")
	mustWriteFile(t, filepath.Join(pkgDir, "types.d.ts"), "export function declarationOnly() {}\n")

	out := buildTSSymbolsSection(dir)
	if !strings.Contains(out, "helper") {
		t.Errorf("expected 'helper' in output:\n%s", out)
	}
	if strings.Contains(out, "shouldNotAppear") {
		t.Errorf("test file symbol should not appear:\n%s", out)
	}
	if strings.Contains(out, "alsoNotAppearing") {
		t.Errorf("spec file symbol should not appear:\n%s", out)
	}
	if strings.Contains(out, "declarationOnly") {
		t.Errorf(".d.ts symbol should not appear:\n%s", out)
	}
}

func TestBuildTSSymbolsSection_NoTSSource(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "main.go"), "package main\n")
	out := buildTSSymbolsSection(dir)
	if out != "" {
		t.Errorf("expected empty output for Go-only project, got:\n%s", out)
	}
}

func TestBuildPythonSymbolsSection_BasicExports(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "myapp", "api")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(pkgDir, "views.py"), `
def public_view():
    pass

def _private_helper():
    pass

class PublicHandler:
    def method(self):
        pass

class _Internal:
    pass
`)

	out := buildPythonSymbolsSection(dir)
	if out == "" {
		t.Fatal("expected non-empty symbol section for Python project")
	}
	if !strings.Contains(out, "public_view()") {
		t.Errorf("expected 'public_view()' in output:\n%s", out)
	}
	if !strings.Contains(out, "PublicHandler") {
		t.Errorf("expected 'PublicHandler' in output:\n%s", out)
	}
	if strings.Contains(out, "_private_helper") {
		t.Errorf("private function should not appear:\n%s", out)
	}
	if strings.Contains(out, "_Internal") {
		t.Errorf("private class should not appear:\n%s", out)
	}
}

func TestBuildPythonSymbolsSection_SkipsTestFiles(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "core")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(pkgDir, "service.py"), "def process():\n    pass\n")
	mustWriteFile(t, filepath.Join(pkgDir, "test_service.py"), "def test_process():\n    pass\n")
	mustWriteFile(t, filepath.Join(pkgDir, "conftest.py"), "def fixture():\n    pass\n")
	mustWriteFile(t, filepath.Join(pkgDir, "__init__.py"), "def init_func():\n    pass\n")

	out := buildPythonSymbolsSection(dir)
	if !strings.Contains(out, "process") {
		t.Errorf("expected 'process' in output:\n%s", out)
	}
	if strings.Contains(out, "test_process") {
		t.Errorf("test file symbol should not appear:\n%s", out)
	}
	if strings.Contains(out, "fixture") {
		t.Errorf("conftest symbol should not appear:\n%s", out)
	}
}

func TestBuildPythonSymbolsSection_NoPythonSource(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "index.js"), "module.exports = {}\n")
	out := buildPythonSymbolsSection(dir)
	if out != "" {
		t.Errorf("expected empty output for JS-only project, got:\n%s", out)
	}
}

func TestIsLikelyImportantExport(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"MAX_RETRIES", true},
		{"AppConfig", true},
		{"BaseUrl", true},
		{"x", false},
		{"items", false},
		{"data", false},
		{"API", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLikelyImportantExport(tt.name); got != tt.want {
				t.Errorf("isLikelyImportantExport(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestHasTSSource(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "main.go"), "package main\n")
	if hasTSSource(dir) {
		t.Error("expected hasTSSource=false for Go-only project")
	}
	mustWriteFile(t, filepath.Join(dir, "app.ts"), "export const x = 1;\n")
	if !hasTSSource(dir) {
		t.Error("expected hasTSSource=true after adding .ts file")
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
