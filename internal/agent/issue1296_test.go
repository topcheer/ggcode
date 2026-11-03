package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func waitForGraph(t *testing.T, dir string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		importGraphCache.Lock()
		ok := importGraphCache.dir == dir && importGraphCache.data.graph != nil
		importGraphCache.Unlock()
		if ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("import graph for %s never built", dir)
}

func TestIssue1296_TestOnlyImportsCounted(t *testing.T) {
	resetImportGraphCache()
	dir := initTestModule(t)

	writeTestGoFile(t, dir, "lib/a/a.go", "package a\n\nfunc A() int { return 1 }\n")

	writeTestGoFile(t, dir, "down/b/b_test.go", "package b_test\n\nimport (\n\t\"testing\"\n\n\t\"test/lib/a\"\n)\n\nfunc TestA(t *testing.T) { _ = a.A() }\n")

	_, _ = transitiveImporters(dir, []string{})
	waitForCacheBuild(dir)

	importers, _ := transitiveImporters(dir, []string{"lib/a"})
	found := map[string]bool{}
	for _, d := range importers {
		found[d] = true
	}
	if !found["down/b"] {
		t.Fatalf("#1296: test-only importer down/b missing from impact closure: %v", importers)
	}
}

func TestIssue1296_NoCrossDirPoisoningOnFailedBuild(t *testing.T) {
	resetImportGraphCache()
	dirA := initTestModule(t)
	writeTestGoFile(t, dirA, "lib/a/a.go", "package a\n\nfunc A() int { return 1 }\n")

	buildImportGraph(dirA)
	waitForGraph(t, dirA)

	dirB := t.TempDir()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		g, _ := buildImportGraph(dirB)
		if g != nil {
			t.Fatalf("#1296: cross-dir cache poisoning - dirB got %d packages from dirA's graph", len(g))
		}
		importGraphCache.Lock()
		building := importGraphCache.building
		importGraphCache.Unlock()
		if !building {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	importGraphCache.Lock()
	bind := importGraphCache.dir == dirA && importGraphCache.data.graph != nil
	importGraphCache.Unlock()
	if !bind {
		t.Fatal("dirA cache entry lost after dirB's failed build")
	}
}

func TestIssue1296_StaleTTLTriggersRebuild(t *testing.T) {
	resetImportGraphCache()
	dir := initTestModule(t)
	writeTestGoFile(t, dir, "lib/a/a.go", "package a\n\nfunc A() int { return 1 }\n")

	buildImportGraph(dir)
	waitForGraph(t, dir)

	importGraphCache.Lock()
	staleAt := importGraphCache.data.builtAt
	importGraphCache.data.builtAt = time.Now().Add(-2 * importGraphTTL)
	importGraphCache.Unlock()

	buildImportGraph(dir)

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		importGraphCache.Lock()
		fresh := importGraphCache.data.builtAt.After(staleAt)
		importGraphCache.Unlock()
		if fresh {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("#1296: stale graph never rebuilt after TTL expiry (frozen forever)")
}

func TestIssue1296_GoTestCommandHasTags(t *testing.T) {
	resetImportGraphCache()
	dir := initTestModule(t)

	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("test:\n\tgo test -tags mytag1296 ./...\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestGoFile(t, dir, "lib/a/a.go", "package a\n\nfunc A() int { return 1 }\n")

	runGit(t, dir, "init", "-q")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")
	writeTestGoFile(t, dir, "lib/a/dirty.go", "package a\n\nfunc D() int { return 2 }\n")

	_, _ = transitiveImporters(dir, []string{})
	waitForCacheBuild(dir)

	cmd := impactScopedTestCommandWithDeps(dir)
	if cmd == "" {
		t.Skip("no impact command emitted for this module state")
	}
	if !strings.Contains(cmd, "-tags") || !strings.Contains(cmd, "mytag1296") {
		t.Fatalf("#1296: go test command lacks build tags: %q", cmd)
	}
}
