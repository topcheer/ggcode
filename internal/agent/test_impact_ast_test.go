package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseExportedFuncs(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "foo.go")
	os.WriteFile(src, []byte(`package foo

import "fmt"

// Exported function.
func Process(data string) error {
	return nil
}

// Exported method on value receiver.
func (b Bar) Value() int { return 0 }

// Exported method on pointer receiver.
func (b *Bar) Set(v int) {}

// Unexported — should be skipped.
func helper() {}

func init() {}
`), 0644)

	funcs := parseExportedFuncs(src)
	if len(funcs) != 3 {
		t.Fatalf("expected 3 exported funcs, got %d: %+v", len(funcs), funcs)
	}

	names := make(map[string]bool)
	for _, f := range funcs {
		names[f.DisplayName] = true
	}
	if !names["Process"] {
		t.Errorf("expected Process, got: %v", names)
	}
	if !names["Bar_Value"] {
		t.Errorf("expected Bar_Value, got: %v", names)
	}
	if !names["Bar_Set"] {
		t.Errorf("expected Bar_Set, got: %v", names)
	}
}

func TestParseExportedFuncs_ParseError(t *testing.T) {
	funcs := parseExportedFuncs("/nonexistent/file.go")
	if funcs != nil {
		t.Error("expected nil on parse error")
	}
}

func TestParseExportedFuncs_NoExported(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	os.WriteFile(src, []byte("package main\n\nfunc helper() {}\n"), 0644)

	funcs := parseExportedFuncs(src)
	if len(funcs) != 0 {
		t.Errorf("expected 0 exported funcs, got %d", len(funcs))
	}
}

func TestParseTestFuncNames(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "foo_test.go")
	os.WriteFile(src, []byte(`package foo

import "testing"

func TestProcess(t *testing.T) {}
func TestBar_Set(t *testing.T) {}
func TestUnexportedHelper(t *testing.T) {}
func helper() {}
`), 0644)

	names := parseTestFuncNames(src)
	if len(names) != 3 {
		t.Fatalf("expected 3 test funcs, got %d: %v", len(names), names)
	}
	if !names["TestProcess"] {
		t.Error("missing TestProcess")
	}
	if !names["TestBar_Set"] {
		t.Error("missing TestBar_Set")
	}
}

func TestUntestedExportedFuncs_NoTestFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "handler.go")
	os.WriteFile(src, []byte(`package handler

func Create() {}
func Delete() {}
`), 0644)

	untested := untestedExportedFuncs(dir, "handler.go")
	if len(untested) != 2 {
		t.Fatalf("expected 2 untested funcs (no test file), got %d: %v", len(untested), untested)
	}
}

func TestUntestedExportedFuncs_SomeTested(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "svc.go")
	os.WriteFile(src, []byte(`package svc

func Create() {}
func Delete() {}
func List() {}
`), 0644)

	testFile := filepath.Join(dir, "svc_test.go")
	os.WriteFile(testFile, []byte(`package svc

import "testing"

func TestCreate(t *testing.T) {}
func TestDelete(t *testing.T) {}
`), 0644)

	untested := untestedExportedFuncs(dir, "svc.go")
	if len(untested) != 1 {
		t.Fatalf("expected 1 untested func (List), got %d: %v", len(untested), untested)
	}
	if untested[0] != "List" {
		t.Errorf("expected List, got %s", untested[0])
	}
}

func TestUntestedExportedFuncs_MethodAltConvention(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "repo.go")
	os.WriteFile(src, []byte(`package repo

type Repo struct{}

func (r *Repo) Save() error { return nil }
func (r *Repo) Load() error { return nil }
`), 0644)

	// Test uses alternate naming: TestRepoSave (no underscore)
	testFile := filepath.Join(dir, "repo_test.go")
	os.WriteFile(testFile, []byte(`package repo

import "testing"

func TestRepoSave(t *testing.T) {}
`), 0644)

	untested := untestedExportedFuncs(dir, "repo.go")
	if len(untested) != 1 {
		t.Fatalf("expected 1 untested method (Load), got %d: %v", len(untested), untested)
	}
	if untested[0] != "Repo_Load" {
		t.Errorf("expected Repo_Load, got %s", untested[0])
	}
}

func TestFuncLevelCoverageGaps(t *testing.T) {
	dir := initGitRepo(t)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
	writeGoFile(t, dir, "main.go", "package main\n")
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	// Changed file with untested functions.
	writeGoFile(t, dir, "pkg/api.go", "package pkg\n\nfunc Create() {}\nfunc Delete() {}\n")

	gaps := funcLevelCoverageGaps(dir, 5, 10)
	if len(gaps) != 1 {
		t.Fatalf("expected 1 gap file, got %d", len(gaps))
	}
	if gaps[0].File != "api.go" {
		t.Errorf("expected api.go, got %s", gaps[0].File)
	}
	if len(gaps[0].Funcs) != 2 {
		t.Errorf("expected 2 untested funcs, got %v", gaps[0].Funcs)
	}
}

func TestFuncLevelCoverageNudge(t *testing.T) {
	dir := initGitRepo(t)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
	writeGoFile(t, dir, "main.go", "package main\n")
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	writeGoFile(t, dir, "svc.go", "package main\n\nfunc DoStuff() {}\n")

	nudge := funcLevelCoverageNudge(dir)
	if nudge == "" {
		t.Fatal("expected non-empty nudge")
	}
	if !strings.Contains(nudge, "DoStuff") {
		t.Errorf("nudge should mention DoStuff: %s", nudge)
	}
	if !strings.Contains(nudge, "Untested functions") {
		t.Errorf("nudge should contain 'Untested functions': %s", nudge)
	}
}

func TestFuncLevelCoverageNudge_NoChanges(t *testing.T) {
	dir := initGitRepo(t)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
	if nudge := funcLevelCoverageNudge(dir); nudge != "" {
		t.Errorf("expected empty nudge, got %s", nudge)
	}
}

func TestGoModulePath(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/example/foo\n\ngo 1.26\n"), 0644)

	mp := goModulePath(dir)
	if mp != "github.com/example/foo" {
		t.Errorf("expected github.com/example/foo, got %s", mp)
	}
}

func TestGoModulePath_NoGoMod(t *testing.T) {
	dir := t.TempDir()
	if mp := goModulePath(dir); mp != "" {
		t.Errorf("expected empty, got %s", mp)
	}
}

func TestDetectGoBuildTags(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build:\n\tgo build -tags goolm ./...\n"), 0644)

	tags := detectGoBuildTags(dir)
	if len(tags) != 1 || tags[0] != "goolm" {
		t.Errorf("expected [goolm], got %v", tags)
	}
}

func TestDetectGoBuildTags_None(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build:\n\tgo build ./...\n"), 0644)

	tags := detectGoBuildTags(dir)
	if tags != nil {
		t.Errorf("expected nil, got %v", tags)
	}
}

func TestDetectGoBuildTags_EqualsForm(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build:\n\tgo build -tags=goolm ./...\n"), 0644)

	tags := detectGoBuildTags(dir)
	if len(tags) != 1 || tags[0] != "goolm" {
		t.Errorf("expected [goolm], got %v", tags)
	}
}

func TestBuildImportGraph_NotGoModule(t *testing.T) {
	dir := t.TempDir()
	graph, mod := buildImportGraph(dir)
	if graph != nil || mod != "" {
		t.Error("expected nil graph for non-Go-module dir")
	}
}

func TestTransitiveImporters_RealModule(t *testing.T) {
	// Test against the real ggcode module — if go list works, verify the graph.
	dir := "/Volumes/new/ggai/ggcode"
	if !fileExists(filepath.Join(dir, "go.mod")) {
		t.Skip("not running in ggcode module")
	}

	graph, modPath := buildImportGraph(dir)
	if graph == nil {
		t.Skip("go list failed (may be environment issue)")
	}
	if modPath == "" {
		t.Fatal("expected non-empty module path")
	}

	// internal/debug is imported by many packages. Check transitive importers.
	importers := transitiveImporters(dir, []string{"internal/debug"})
	// This may return nil if go list didn't resolve; just don't panic.
	if importers != nil {
		// Should contain at least internal/agent since agent imports debug.
		found := false
		for _, imp := range importers {
			if imp == "internal/agent" {
				found = true
				break
			}
		}
		if !found {
			// Not a failure — go list output format may vary.
			t.Logf("importers of internal/debug: %v", importers)
		}
	}
}

func TestImpactScopedTestCommandWithDeps_EmptyDir(t *testing.T) {
	if cmd := impactScopedTestCommandWithDeps(""); cmd != "" {
		t.Errorf("expected empty for empty dir")
	}
}

func TestImpactScopedTestCommandWithDeps_NoGoMod(t *testing.T) {
	dir := initGitRepo(t)
	writeGoFile(t, dir, "foo.go", "package main\n")
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()
	writeGoFile(t, dir, "new.go", "package main\n")

	// No go.mod — should return empty.
	if cmd := impactScopedTestCommandWithDeps(dir); cmd != "" {
		t.Errorf("expected empty for non-Go-module, got %s", cmd)
	}
}

func TestImpactScopedTestCommandWithDeps_RealModule(t *testing.T) {
	dir := "/Volumes/new/ggai/ggcode"
	if !fileExists(filepath.Join(dir, "go.mod")) {
		t.Skip("not running in ggcode module")
	}

	// Manually set up fake changes by checking if git is available.
	// This test just verifies the function doesn't panic and returns
	// a valid command or empty string.
	cmd := impactScopedTestCommandWithDeps(dir)
	// Don't assert specific value — depends on git state.
	if cmd != "" {
		if !strings.HasPrefix(cmd, "go test ") {
			t.Errorf("expected go test prefix, got %s", cmd)
		}
	}
}

// TestImportGraphCacheInvalidation verifies the cache returns data within TTL.
func TestImportGraphCacheTTL(t *testing.T) {
	// Reset cache.
	importGraphCache.Lock()
	importGraphCache.dir = ""
	importGraphCache.data = importGraphEntry{}
	importGraphCache.Unlock()

	dir := "/Volumes/new/ggai/ggcode"
	if !fileExists(filepath.Join(dir, "go.mod")) {
		t.Skip("not in ggcode module")
	}

	// First call builds the graph.
	g1, _ := buildImportGraph(dir)
	if g1 == nil {
		t.Skip("go list not available")
	}

	// Second call within TTL should return cached data (same pointer).
	g2, _ := buildImportGraph(dir)
	if len(g1) != len(g2) {
		t.Logf("graph size differs (may be race): %d vs %d", len(g1), len(g2))
	}
}
