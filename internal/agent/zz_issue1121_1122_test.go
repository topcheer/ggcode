package agent

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const rangeGoroutineCaptureSrc = `package main

func process(items []int) {
	for _, item := range items {
		go func() {
			println(item)
		}()
	}
}
`

// --- Issue #1121 ---

// registrySrc1121 mirrors the registry pattern from #1121: a package-level
// hintless map accumulated across many register() calls must never trigger
// the single-loop size-hint warning.
const registrySrc1121 = `package main
type Handler struct{ Name string }

var handlers = make(map[string]Handler)

func register(items []Handler) {
	for _, it := range items {
		handlers[it.Name] = it
	}
}
`

// TestIssue1121_PackageLevelRegistryNoWarning guards #1121: loop writes into
// a package-level hintless map must produce zero warnings (the decl is
// excluded from candidate binds when assembling each function unit).
func TestIssue1121_PackageLevelRegistryNoWarning(t *testing.T) {
	warnings := checkMapPrealloc("registry.go", "", registrySrc1121)
	if len(warnings) != 0 {
		t.Fatalf("#1121: package-level registry map must not warn, got: %v", warnings)
	}
}

// TestIssue1121_LocalMapStillWarnedInMixedFunc guards that excluding package
// level maps did not over-suppress: in one function that also builds a
// function-local hintless map, exactly that local map is still warned.
func TestIssue1121_LocalMapStillWarnedInMixedFunc(t *testing.T) {
	code := `package main
type Handler struct{ Name string }
type Item struct{ Key string; H Handler }

var handlers = make(map[string]Handler)

func register(items []Item) map[string]bool {
	seen := make(map[string]bool)
	for _, it := range items {
		handlers[it.Key] = it.H
		seen[it.Key] = true
	}
	return seen
}
`
	warnings := checkMapPrealloc("mixed.go", "", code)
	if len(warnings) != 1 {
		t.Fatalf("#1121: expected exactly 1 warning for local 'seen', got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], `"seen"`) {
		t.Fatalf("#1121: expected the warning to name the local 'seen', got: %s", warnings[0])
	}
}

// --- Issue #1122 ---

// TestIssue1122_VersionResolvedFromEditedFileDir guards defect (a): the Go
// version used for the downgrade decision comes from the edited file's own
// module, not the process cwd. Chdir into a go 1.21 parent module while
// editing inside its go 1.22 submodule: the capture warning must still be
// downgraded to per-iteration phrasing.
func TestIssue1122_VersionResolvedFromEditedFileDir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "submod")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module parent\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "go.mod"), []byte("module submod\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWD) }()
	// cwd now sits in the parent's go 1.21 module (#1122 pre-fix behavior
	// would have resolved the version from here).
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(sub, "worker.go")
	warnings := checkLoopVarCapture(target, "", rangeGoroutineCaptureSrc)
	if len(warnings) == 0 {
		t.Fatalf("#1122: expected at least one goroutine-capture warning")
	}
	for _, w := range warnings {
		if strings.Contains(w, "classic Go gotcha") {
			t.Fatalf("#1122: edited file lives in a go 1.22 submodule but got pre-1.22 semantics (cwd-based lookup?): %s", w)
		}
		if !strings.Contains(w, "may still be a bug") {
			t.Fatalf("#1122: expected downgraded 1.22+ phrasing, got: %s", w)
		}
	}
}

// TestIssue1122_Pre122ModuleKeepsClassicGotcha guards the reverse direction:
// editing a file whose module declares go 1.21 keeps the full classic-gotcha
// warning regardless of where the process cwd happens to be.
func TestIssue1122_Pre122ModuleKeepsClassicGotcha(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module legacy\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "legacy.go")
	warnings := checkLoopVarCapture(target, "", rangeGoroutineCaptureSrc)
	if len(warnings) == 0 {
		t.Fatalf("#1122: expected at least one warning for a go 1.21 module")
	}
	foundClassic := false
	for _, w := range warnings {
		if strings.Contains(w, "classic Go gotcha") {
			foundClassic = true
		}
	}
	if !foundClassic {
		t.Fatalf("#1122: expected classic-gotcha phrasing in a go 1.21 module, got: %v", warnings)
	}
}

// TestIssue1122_ConcurrentFirstLookupNoRace guards defect (c): concurrent
// first-time resolution of the same directory's go.mod must be race-free.
// Run under -race to verify.
func TestIssue1122_ConcurrentFirstLookupNoRace(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module m\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "a.go")

	const workers = 16
	results := make([]bool, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = go122PlusFor(file)
		}(i)
	}
	wg.Wait()
	for _, got := range results {
		if !got {
			t.Fatalf("#1122: concurrent lookups disagree; want true for a go 1.22 module")
		}
	}
}
