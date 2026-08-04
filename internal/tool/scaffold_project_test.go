package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldProject_Go(t *testing.T) {
	dir := t.TempDir()
	tool := ScaffoldProject{WorkingDir: dir}

	input := scaffoldMustMarshal(t, map[string]interface{}{
		"language":     "go",
		"project_name": "my-app",
		"output_dir":   dir,
		"options": map[string]interface{}{
			"module_path": "github.com/test/my-app",
			"ci":          true,
		},
	})

	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", res.Content)
	}

	var out scaffoldResult
	if err := json.Unmarshal([]byte(res.Content), &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if out.Created == 0 {
		t.Error("expected at least 1 file created")
	}

	// Verify key files exist.
	mustExist := []string{
		"go.mod",
		"cmd/my-app/main.go",
		"cmd/my-app/main_test.go",
		"Makefile",
		".gitignore",
		"README.md",
		".github/workflows/ci.yml",
	}
	for _, f := range mustExist {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expected file %s to exist: %v", f, err)
		}
	}

	// Verify module path in go.mod.
	modData, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	if !scaffoldContains(string(modData), "github.com/test/my-app") {
		t.Errorf("go.mod should contain module path")
	}
}

func TestScaffoldProject_TypeScript(t *testing.T) {
	dir := t.TempDir()
	tool := ScaffoldProject{WorkingDir: dir}

	input := scaffoldMustMarshal(t, map[string]interface{}{
		"language":     "typescript",
		"project_name": "my-app",
		"output_dir":   dir,
		"options": map[string]interface{}{
			"docker": true,
		},
	})

	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", res.Content)
	}

	var out scaffoldResult
	if err := json.Unmarshal([]byte(res.Content), &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	mustExist := []string{
		"package.json",
		"tsconfig.json",
		"src/index.ts",
		"src/index.test.ts",
		".gitignore",
		"Dockerfile",
		".github/workflows/ci.yml",
	}
	for _, f := range mustExist {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expected file %s to exist: %v", f, err)
		}
	}
}

func TestScaffoldProject_Python(t *testing.T) {
	dir := t.TempDir()
	tool := ScaffoldProject{WorkingDir: dir}

	input := scaffoldMustMarshal(t, map[string]interface{}{
		"language":     "python",
		"project_name": "My Cool App",
		"output_dir":   dir,
	})

	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", res.Content)
	}

	var out scaffoldResult
	if err := json.Unmarshal([]byte(res.Content), &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// Python package name should be sanitized to my_cool_app.
	mustExist := []string{
		"pyproject.toml",
		"my_cool_app/__init__.py",
		"my_cool_app/main.py",
		"tests/test_main.py",
		".gitignore",
		"README.md",
	}
	for _, f := range mustExist {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expected file %s to exist: %v", f, err)
		}
	}
}

func TestScaffoldProject_Rust(t *testing.T) {
	dir := t.TempDir()
	tool := ScaffoldProject{WorkingDir: dir}

	input := scaffoldMustMarshal(t, map[string]interface{}{
		"language":     "rust",
		"project_name": "my_rust_app",
		"output_dir":   dir,
	})

	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", res.Content)
	}

	mustExist := []string{
		"Cargo.toml",
		"src/main.rs",
		"src/lib.rs",
		"tests/integration_test.rs",
		".gitignore",
	}
	for _, f := range mustExist {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expected file %s to exist: %v", f, err)
		}
	}
}

func TestScaffoldProject_NoOverwrite(t *testing.T) {
	dir := t.TempDir()
	tool := ScaffoldProject{WorkingDir: dir}

	// Pre-create a file that the template would generate.
	existing := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(existing, []byte("# custom\n"), 0644); err != nil {
		t.Fatal(err)
	}

	input := scaffoldMustMarshal(t, map[string]interface{}{
		"language":     "go",
		"project_name": "test-app",
		"output_dir":   dir,
	})

	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", res.Content)
	}

	var out scaffoldResult
	if err := json.Unmarshal([]byte(res.Content), &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if out.Skipped == 0 {
		t.Error("expected at least 1 file skipped (pre-existing .gitignore)")
	}

	// Verify the pre-existing file was not overwritten.
	data, _ := os.ReadFile(existing)
	if string(data) != "# custom\n" {
		t.Errorf("pre-existing file was overwritten: got %q", string(data))
	}
}

func TestScaffoldProject_InvalidLanguage(t *testing.T) {
	dir := t.TempDir()
	tool := ScaffoldProject{WorkingDir: dir}

	input := scaffoldMustMarshal(t, map[string]interface{}{
		"language":     "cobol",
		"project_name": "test",
		"output_dir":   dir,
	})

	res, _ := tool.Execute(context.Background(), input)
	if !res.IsError {
		t.Error("expected error for unsupported language")
	}
}

func TestScaffoldProject_MissingName(t *testing.T) {
	dir := t.TempDir()
	tool := ScaffoldProject{WorkingDir: dir}

	input := scaffoldMustMarshal(t, map[string]interface{}{
		"language":   "go",
		"output_dir": dir,
	})

	res, _ := tool.Execute(context.Background(), input)
	if !res.IsError {
		t.Error("expected error for missing project_name")
	}
}

func TestScaffoldProject_DefaultOutputDir(t *testing.T) {
	dir := t.TempDir()
	tool := ScaffoldProject{WorkingDir: dir}

	// No output_dir -- should default to WorkingDir.
	input := scaffoldMustMarshal(t, map[string]interface{}{
		"language":     "go",
		"project_name": "defdir",
	})

	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", res.Content)
	}

	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Errorf("expected go.mod in WorkingDir: %v", err)
	}
}

// --- Helpers ---

func scaffoldMustMarshal(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func scaffoldContains(s, sub string) bool {
	return len(s) >= len(sub) && strings.Contains(s, sub)
}
