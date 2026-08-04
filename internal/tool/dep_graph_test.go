package tool

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDepGraph_Overview(t *testing.T) {
	dir := setupTestModule(t)

	tool := DepGraphTool{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"path":        dir,
		"action":      "overview",
		"description": "test overview",
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content)
	}

	if !contains(result.Content, "Dependency Graph") {
		t.Errorf("expected 'Dependency Graph' in output, got: %s", result.Content)
	}
	if !contains(result.Content, "packages") {
		t.Errorf("expected 'packages' in output, got: %s", result.Content)
	}
}

func TestDepGraph_ReverseDeps(t *testing.T) {
	dir := setupTestModule(t)

	tool := DepGraphTool{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"path":        dir,
		"action":      "reverse_deps",
		"target":      "util",
		"description": "test reverse deps",
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content)
	}

	// alpha imports util, beta imports util
	if !contains(result.Content, "alpha") {
		t.Errorf("expected 'alpha' in reverse deps, got: %s", result.Content)
	}
	if !contains(result.Content, "beta") {
		t.Errorf("expected 'beta' in reverse deps, got: %s", result.Content)
	}
}

func TestDepGraph_Hotspots(t *testing.T) {
	dir := setupTestModule(t)

	tool := DepGraphTool{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"path":        dir,
		"action":      "hotspots",
		"description": "test hotspots",
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content)
	}

	// util should be the top hotspot (imported by alpha and beta)
	if !contains(result.Content, "util") {
		t.Errorf("expected 'util' in hotspots, got: %s", result.Content)
	}
}

func TestDepGraph_Cycles(t *testing.T) {
	dir := setupTestModule(t)

	tool := DepGraphTool{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"path":        dir,
		"action":      "cycles",
		"description": "test cycles",
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content)
	}

	// cycA imports cycB, cycB imports cycA — should detect a cycle
	if !contains(result.Content, "cycA") {
		t.Errorf("expected 'cycA' in cycle detection, got: %s", result.Content)
	}
	if !contains(result.Content, "cycB") {
		t.Errorf("expected 'cycB' in cycle detection, got: %s", result.Content)
	}
}

func TestDepGraph_NoGoMod(t *testing.T) {
	dir := t.TempDir()

	tool := DepGraphTool{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"path":        dir,
		"action":      "overview",
		"description": "test no go.mod",
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Errorf("expected error for missing go.mod, got: %s", result.Content)
	}
}

func TestDepGraph_ReverseDepsMissingTarget(t *testing.T) {
	dir := setupTestModule(t)

	tool := DepGraphTool{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"path":        dir,
		"action":      "reverse_deps",
		"description": "test missing target",
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Errorf("expected error for missing target, got: %s", result.Content)
	}
}

func TestDedupSorted(t *testing.T) {
	in := []string{"b", "a", "b", "c", "a", "d"}
	out := dedupSorted(in)
	want := []string{"a", "b", "c", "d"}
	if len(out) != len(want) {
		t.Fatalf("got %v, want %v", out, want)
	}
	for i := range want {
		if out[i] != want[i] {
			t.Errorf("index %d: got %q, want %q", i, out[i], want[i])
		}
	}
}

func TestShortPkgName(t *testing.T) {
	tests := []struct {
		pkgPath, modulePath, want string
	}{
		{"example.com/mymod", "example.com/mymod", "mymod"},
		{"example.com/mymod/internal/util", "example.com/mymod", "internal/util"},
		{"example.com/mymod/pkg/foo", "example.com/mymod", "pkg/foo"},
	}
	for _, tt := range tests {
		got := shortPkgName(tt.pkgPath, tt.modulePath)
		if got != tt.want {
			t.Errorf("shortPkgName(%q, %q) = %q, want %q", tt.pkgPath, tt.modulePath, got, tt.want)
		}
	}
}

// setupTestModule creates a temporary Go module with a dependency structure:
//
//	alpha -> util
//	beta  -> util
//	cycA  -> cycB
//	cycB  -> cycA  (cycle) - creates import cycle
//	gamma -> alpha -> util  (transitive)
func setupTestModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	modPath := "example.com/testmod"

	// Create go.mod
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module "+modPath+"\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}

	pkgs := map[string]string{
		"util":  "package util\n\nfunc Helper() {}\n",
		"alpha": "package alpha\n\nimport \"" + modPath + "/util\"\n\nfunc Use() { util.Helper() }\n",
		"beta":  "package beta\n\nimport \"" + modPath + "/util\"\n\nfunc Use() { util.Helper() }\n",
		"gamma": "package gamma\n\nimport \"" + modPath + "/alpha\"\n\nfunc Use() { alpha.Use() }\n",
		"cycA":  "package cycA\n\nimport \"" + modPath + "/cycB\"\n\nfunc Use() { cycB.Use() }\n",
		"cycB":  "package cycB\n\nimport \"" + modPath + "/cycA\"\n\nfunc Use() { cycA.Use() }\n",
	}

	for name, content := range pkgs {
		pkgDir := filepath.Join(dir, name)
		if err := os.MkdirAll(pkgDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkgDir, name+".go"),
			[]byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

// mustJSON and contains are defined in other test files in this package.
