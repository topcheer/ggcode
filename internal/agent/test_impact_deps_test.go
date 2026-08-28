package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// initTestModule creates a temporary Go module with a basic go.mod.
func initTestModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeTestGoFile writes a Go file with imports in the test module.
func writeTestGoFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	abs := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestTransitiveImporters_TwoLevelChain tests the core transitive behavior:
// A imports B, B imports C, C changed → A should be in the result.
func TestTransitiveImporters_TwoLevelChain(t *testing.T) {
	resetImportGraphCache()
	dir := initTestModule(t)

	// Create a three-level import chain: A → B → C
	// Package C (leaf)
	writeTestGoFile(t, dir, "pkg/c/c.go", "package c\n")
	// Package B imports C
	writeTestGoFile(t, dir, "pkg/b/b.go", "package b\nimport \"test/pkg/c\"\n")
	// Package A imports B (but not C directly)
	writeTestGoFile(t, dir, "pkg/a/a.go", "package a\nimport \"test/pkg/b\"\n")

	// Debug: list files in the test directory
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		t.Logf("test dir root: %s", f.Name())
	}
	if dirs, err := os.ReadDir(filepath.Join(dir, "pkg")); err == nil {
		for _, d := range dirs {
			t.Logf("test dir pkg: %s", d.Name())
			if subfiles, err := os.ReadDir(filepath.Join(dir, "pkg", d.Name())); err == nil {
				for _, sf := range subfiles {
					t.Logf("test dir pkg/%s: %s", d.Name(), sf.Name())
				}
			}
		}
	}

	// The first call to transitiveImporters triggers a background build.
	// Call it twice: the first triggers the build, the second uses the cache.
	_ = transitiveImporters(dir, []string{})
	waitForCacheBuild(dir)

	// Now call with the actual changed package
	importers := transitiveImporters(dir, []string{"pkg/c"})

	t.Logf("importers result: %v", importers)

	if importers == nil {
		t.Fatal("expected importers, got nil")
	}

	// Verify that both B and A are included (transitive closure)
	foundB := false
	foundA := false
	for _, imp := range importers {
		if imp == "pkg/b" {
			foundB = true
		}
		if imp == "pkg/a" {
			foundA = true
		}
	}

	if !foundB {
		t.Error("pkg/b (direct importer) not found in results")
	}
	if !foundA {
		t.Error("pkg/a (transitive importer via B) not found in results")
	}
}

// TestTransitiveImporters_Determinism tests that the same input produces
// the same output ordering (deterministic BFS with sorted iteration).
func TestTransitiveImporters_Determinism(t *testing.T) {
	resetImportGraphCache()
	dir := initTestModule(t)

	// Create a small graph: Z imports C, Y imports B, X imports A
	// All A, B, C are changed.
	writeTestGoFile(t, dir, "pkg/a/a.go", "package a\n")
	writeTestGoFile(t, dir, "pkg/b/b.go", "package b\n")
	writeTestGoFile(t, dir, "pkg/c/c.go", "package c\n")
	writeTestGoFile(t, dir, "pkg/x/x.go", "package x\nimport \"test/pkg/a\"\n")
	writeTestGoFile(t, dir, "pkg/y/y.go", "package y\nimport \"test/pkg/b\"\n")
	writeTestGoFile(t, dir, "pkg/z/z.go", "package z\nimport \"test/pkg/c\"\n")

	// Trigger cache build
	_ = transitiveImporters(dir, []string{})
	waitForCacheBuild(dir)

	// Run twice and compare
	importers1 := transitiveImporters(dir, []string{"pkg/a", "pkg/b", "pkg/c"})
	importers2 := transitiveImporters(dir, []string{"pkg/a", "pkg/b", "pkg/c"})

	if importers1 == nil {
		t.Fatal("expected importers on first call, got nil")
	}
	if importers2 == nil {
		t.Fatal("expected importers on second call, got nil")
	}

	if len(importers1) != len(importers2) {
		t.Fatalf("determinism failed: different lengths %d vs %d", len(importers1), len(importers2))
	}

	for i := range importers1 {
		if importers1[i] != importers2[i] {
			t.Errorf("determinism failed: at index %d, got %s vs %s", i, importers1[i], importers2[i])
		}
	}

	// Verify sorted order (A < B < C < X < Y < Z alphabetically)
	expected := []string{"pkg/x", "pkg/y", "pkg/z"}
	for i, exp := range expected {
		if importers1[i] != exp {
			t.Errorf("expected %s at index %d, got %s", exp, i, importers1[i])
		}
	}
}

// TestTransitiveImporters_NoGraph tests graceful fallback when import graph
// can't be built.
func TestTransitiveImporters_NoGraph(t *testing.T) {
	resetImportGraphCache()
	// Non-existent directory
	importers := transitiveImporters("/nonexistent/path", []string{"pkg/a"})
	if importers != nil {
		t.Errorf("expected nil for non-existent dir, got %v", importers)
	}
}

// TestTransitiveImporters_EmptyChanges tests empty changed dirs list.
func TestTransitiveImporters_EmptyChanges(t *testing.T) {
	resetImportGraphCache()
	dir := initTestModule(t)
	importers := transitiveImporters(dir, []string{})
	if importers != nil {
		t.Errorf("expected nil for empty changes, got %v", importers)
	}
}

// TestTransitiveImporters_ExternalImports tests that only module-internal
// importers are included (packages outside the module are ignored).
func TestTransitiveImporters_ExternalImports(t *testing.T) {
	resetImportGraphCache()
	dir := initTestModule(t)

	// Package A imports stdlib (not in module)
	writeTestGoFile(t, dir, "pkg/a/a.go", "package a\nimport \"fmt\"\n")
	// Package B changes
	writeTestGoFile(t, dir, "pkg/b/b.go", "package b\n")

	// A does not import B, so no importers should be found
	importers := transitiveImporters(dir, []string{"pkg/b"})
	if importers != nil {
		t.Errorf("expected nil (A imports fmt, not B), got %v", importers)
	}
}

// containsSubstring is a helper for string containment checks.
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && indexOfSubstring(s, substr) >= 0)
}

func indexOfSubstring(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// waitForCacheBuild waits for the background import graph build to complete.
// This is only used in tests to ensure the cache is ready before querying.
func waitForCacheBuild(workingDir string) {
	for i := 0; i < 20; i++ {
		importGraphCache.Lock()
		ready := importGraphCache.dir == workingDir && importGraphCache.data.graph != nil && !importGraphCache.building
		importGraphCache.Unlock()
		if ready {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// resetImportGraphCache resets the shared import graph cache.
// This should be called in tests that use different working directories.
func resetImportGraphCache() {
	importGraphCache.Lock()
	defer importGraphCache.Unlock()
	importGraphCache.dir = ""
	importGraphCache.data = importGraphEntry{}
	importGraphCache.building = false
}
