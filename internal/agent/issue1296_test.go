package agent

// Regression tests for GitHub issue #1296: four TIA defects - (1) cache
// cross-directory poisoning (dir written at build START, data only on
// success), (2) TTL expiry never refreshed, (3) go list ignored
// TestImports/XTestImports (test-only dependency edges invisible), (4) the
// emitted go test command lacked the detected build tags.

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

// Defect 3: a package that imports the changed package ONLY from its
// _test.go file must be in the impact closure (the canonical TIA case).
func TestIssue1296_TestOnlyImportsCounted(t *testing.T) {
	resetImportGraphCache()
	dir := initTestModule(t)

	writeTestGoFile(t, dir, "lib/a/a.go", "package a\n\nfunc A() int { return 1 }\n")
	// downstream imports lib/a only from its external test file.
	writeTestGoFile(t, dir, "down/b/b_test.go", "package b_test\n\nimport (\n\t\"testing\"\n\n\t\"test/lib/a\"\n)\n\nfunc TestA(t *testing.T) { _ = a.A() }\n")

	// First call triggers the background build; wait for it to land.
	_ = transitiveImporters(dir, []string{})
	waitForCacheBuild(dir)

	importers := transitiveImporters(dir, []string{"lib/a"})
	found := map[string]bool{}
	for _, d := range importers {
		found[d] = true
	}
	if !found["down/b"] {
		t.Fatalf("#1296: test-only importer down/b missing from impact closure: %v", importers)
	}
}

// Defect 1: a failed build for a NEW directory must not serve the OLD
// directory's graph as a cache hit (dir binds to data only on success).
func TestIssue1296_NoCrossDirPoisoningOnFailedBuild(t *testing.T) {
	resetImportGraphCache()
	dirA := initTestModule(t)
	writeTestGoFile(t, dirA, "lib/a/a.go", "package a\n\nfunc A() int { return 1 }\n")

	buildImportGraph(dirA)
	waitForGraph(t, dirA)

	// dirB: no go.mod, no packages - `go list ./...` fails deterministically.
	dirB := t.TempDir()

	// The failed build must leave the cache bound to dirA only: calls for
	// dirB return nil (cold), never dirA's graph.
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
			break // failed build finished, cache still clean
		}
		time.Sleep(50 * time.Millisecond)
	}
	// And dirA's cache is untouched.
	importGraphCache.Lock()
	bind := importGraphCache.dir == dirA && importGraphCache.data.graph != nil
	importGraphCache.Unlock()
	if !bind {
		t.Fatal("dirA cache entry lost after dirB's failed build")
	}
}

// Defect 2: TTL expiry must trigger a background refresh, not serve the
// frozen graph forever.
func TestIssue1296_StaleTTLTriggersRebuild(t *testing.T) {
	resetImportGraphCache()
	dir := initTestModule(t)
	writeTestGoFile(t, dir, "lib/a/a.go", "package a\n\nfunc A() int { return 1 }\n")

	buildImportGraph(dir)
	waitForGraph(t, dir)

	importGraphCache.Lock()
	staleAt := importGraphCache.data.builtAt
	importGraphCache.data.builtAt = time.Now().Add(-2 * importGraphTTL) // force stale
	importGraphCache.Unlock()

	buildImportGraph(dir) // stale call: must kick a rebuild

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		importGraphCache.Lock()
		fresh := importGraphCache.data.builtAt.After(staleAt)
		importGraphCache.Unlock()
		if fresh {
			return // rebuilt
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("#1296: stale graph never rebuilt after TTL expiry (frozen forever)")
}

// Defect 4: the emitted go test command carries the detected build tags.
func TestIssue1296_GoTestCommandHasTags(t *testing.T) {
	resetImportGraphCache()
	dir := initTestModule(t)
	// detectGoBuildTags reads the Makefile -tags flag (not build
	// constraints): provide one, plus a buildable tagged package.
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("test:\n\tgo test -tags mytag1296 ./...\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestGoFile(t, dir, "lib/a/a.go", "package a\n\nfunc A() int { return 1 }\n")

	// changedGoPackageDirs is git-based: init a repo and dirty the tree so a
	// changed package exists and a command is emitted.
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")
	writeTestGoFile(t, dir, "lib/a/dirty.go", "package a\n\nfunc D() int { return 2 }\n")

	_ = transitiveImporters(dir, []string{})
	waitForCacheBuild(dir)

	cmd := impactScopedTestCommandWithDeps(dir)
	if cmd == "" {
		t.Skip("no impact command emitted for this module state")
	}
	if !strings.Contains(cmd, "-tags") || !strings.Contains(cmd, "mytag1296") {
		t.Fatalf("#1296: go test command lacks build tags: %q", cmd)
	}
}
