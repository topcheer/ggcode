package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func initTestModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

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

func TestTransitiveImporters_TwoLevelChain(t *testing.T) {
	resetImportGraphCache()
	dir := initTestModule(t)

	writeTestGoFile(t, dir, "pkg/c/c.go", "package c\n")

	writeTestGoFile(t, dir, "pkg/b/b.go", "package b\nimport \"test/pkg/c\"\n")

	writeTestGoFile(t, dir, "pkg/a/a.go", "package a\nimport \"test/pkg/b\"\n")

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

	_, _ = transitiveImporters(dir, []string{})
	waitForCacheBuild(dir)

	importers, _ := transitiveImporters(dir, []string{"pkg/c"})

	t.Logf("importers result: %v", importers)

	if importers == nil {
		t.Fatal("expected importers, got nil")
	}

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

func TestTransitiveImporters_Determinism(t *testing.T) {
	resetImportGraphCache()
	dir := initTestModule(t)

	writeTestGoFile(t, dir, "pkg/a/a.go", "package a\n")
	writeTestGoFile(t, dir, "pkg/b/b.go", "package b\n")
	writeTestGoFile(t, dir, "pkg/c/c.go", "package c\n")
	writeTestGoFile(t, dir, "pkg/x/x.go", "package x\nimport \"test/pkg/a\"\n")
	writeTestGoFile(t, dir, "pkg/y/y.go", "package y\nimport \"test/pkg/b\"\n")
	writeTestGoFile(t, dir, "pkg/z/z.go", "package z\nimport \"test/pkg/c\"\n")

	_, _ = transitiveImporters(dir, []string{})
	waitForCacheBuild(dir)

	importers1, _ := transitiveImporters(dir, []string{"pkg/a", "pkg/b", "pkg/c"})
	importers2, _ := transitiveImporters(dir, []string{"pkg/a", "pkg/b", "pkg/c"})

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

	expected := []string{"pkg/x", "pkg/y", "pkg/z"}
	for i, exp := range expected {
		if importers1[i] != exp {
			t.Errorf("expected %s at index %d, got %s", exp, i, importers1[i])
		}
	}
}

func TestTransitiveImporters_NoGraph(t *testing.T) {
	resetImportGraphCache()

	importers, _ := transitiveImporters("/nonexistent/path", []string{"pkg/a"})
	if importers != nil {
		t.Errorf("expected nil for non-existent dir, got %v", importers)
	}
}

func TestTransitiveImporters_EmptyChanges(t *testing.T) {
	resetImportGraphCache()
	dir := initTestModule(t)
	importers, _ := transitiveImporters(dir, []string{})
	if importers != nil {
		t.Errorf("expected nil for empty changes, got %v", importers)
	}
}

func TestTransitiveImporters_ExternalImports(t *testing.T) {
	resetImportGraphCache()
	dir := initTestModule(t)

	writeTestGoFile(t, dir, "pkg/a/a.go", "package a\nimport \"fmt\"\n")

	writeTestGoFile(t, dir, "pkg/b/b.go", "package b\n")

	importers, _ := transitiveImporters(dir, []string{"pkg/b"})
	if importers != nil {
		t.Errorf("expected nil (A imports fmt, not B), got %v", importers)
	}
}

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

func resetImportGraphCache() {
	importGraphCache.Lock()
	defer importGraphCache.Unlock()
	importGraphCache.dir = ""
	importGraphCache.data = importGraphEntry{}
	importGraphCache.building = false
}
