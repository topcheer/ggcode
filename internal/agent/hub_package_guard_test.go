package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeHubGoFile(t *testing.T, dir, name, content string) {
	t.Helper()
	fullPath := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestHubPackageGuard_HubPackageFires verifies that editing a file in a widely
// imported package triggers the awareness hint.
func TestHubPackageGuard_HubPackageFires(t *testing.T) {
	root := t.TempDir()

	// go.mod
	writeHubGoFile(t, root, "go.mod", "module example.com/test\n\ngo 1.26\n")

	// hub package with exported function
	writeHubGoFile(t, root, "internal/hub/hub.go", "package hub\n\nfunc DoSomething() {}\n")

	// 6 packages that import the hub (fan-in = 6 >= threshold of 5)
	for i := 0; i < 6; i++ {
		dir := filepath.Join(root, "internal", "consumer"+string(rune('a'+i)))
		writeHubGoFile(t, dir, "main.go",
			"package consumer"+string(rune('a'+i))+"\n\nimport \"example.com/test/internal/hub\"\n\nfunc Use() { hub.DoSomething() }\n")
	}

	a := &Agent{
		hubPackageGuard: newHubPackageState(),
		workingDir:      root,
	}

	hint := a.checkHubPackage(filepath.Join(root, "internal", "hub", "hub.go"))
	if hint == "" {
		t.Error("expected hub package hint for high fan-in package, got empty")
	}
	if !strings.Contains(hint, "imported by 6") {
		t.Errorf("hint should mention importer count, got: %s", hint)
	}
}

// TestHubPackageGuard_LeafPackageNoFire verifies that editing a file in a
// low fan-in package does NOT trigger the hint.
func TestHubPackageGuard_LeafPackageNoFire(t *testing.T) {
	root := t.TempDir()

	writeHubGoFile(t, root, "go.mod", "module example.com/test\n\ngo 1.26\n")
	writeHubGoFile(t, root, "internal/leaf/leaf.go", "package leaf\n\nfunc Leaf() {}\n")

	// Only 2 importers (below threshold of 5)
	for i := 0; i < 2; i++ {
		dir := filepath.Join(root, "internal", "user"+string(rune('a'+i)))
		writeHubGoFile(t, dir, "main.go",
			"package user"+string(rune('a'+i))+"\n\nimport \"example.com/test/internal/leaf\"\n\nfunc Use() { leaf.Leaf() }\n")
	}

	a := &Agent{
		hubPackageGuard: newHubPackageState(),
		workingDir:      root,
	}

	hint := a.checkHubPackage(filepath.Join(root, "internal", "leaf", "leaf.go"))
	if hint != "" {
		t.Errorf("expected no hint for low fan-in package, got: %s", hint)
	}
}

// TestHubPackageGuard_FiresOncePerFile verifies that repeated edits to the same
// file only trigger the hint once per run.
func TestHubPackageGuard_FiresOncePerFile(t *testing.T) {
	root := t.TempDir()

	writeHubGoFile(t, root, "go.mod", "module example.com/test\n\ngo 1.26\n")
	writeHubGoFile(t, root, "internal/hub/hub.go", "package hub\n\nfunc DoSomething() {}\n")

	for i := 0; i < 6; i++ {
		dir := filepath.Join(root, "internal", "c"+string(rune('a'+i)))
		writeHubGoFile(t, dir, "main.go",
			"package c"+string(rune('a'+i))+"\n\nimport \"example.com/test/internal/hub\"\n\nfunc Use() { hub.DoSomething() }\n")
	}

	a := &Agent{
		hubPackageGuard: newHubPackageState(),
		workingDir:      root,
	}

	hint1 := a.checkHubPackage(filepath.Join(root, "internal", "hub", "hub.go"))
	if hint1 == "" {
		t.Fatal("expected hint on first edit")
	}

	hint2 := a.checkHubPackage(filepath.Join(root, "internal", "hub", "hub.go"))
	if hint2 != "" {
		t.Error("expected no hint on second edit to same file")
	}
}

// TestHubPackageGuard_ResetClearsChecked verifies that reset() clears the
// checked set so hints fire again on a new run.
func TestHubPackageGuard_ResetClearsChecked(t *testing.T) {
	root := t.TempDir()

	writeHubGoFile(t, root, "go.mod", "module example.com/test\n\ngo 1.26\n")
	writeHubGoFile(t, root, "internal/hub/hub.go", "package hub\n\nfunc DoSomething() {}\n")

	for i := 0; i < 6; i++ {
		dir := filepath.Join(root, "internal", "c"+string(rune('a'+i)))
		writeHubGoFile(t, dir, "main.go",
			"package c"+string(rune('a'+i))+"\n\nimport \"example.com/test/internal/hub\"\n\nfunc Use() { hub.DoSomething() }\n")
	}

	a := &Agent{
		hubPackageGuard: newHubPackageState(),
		workingDir:      root,
	}

	hint1 := a.checkHubPackage(filepath.Join(root, "internal", "hub", "hub.go"))
	if hint1 == "" {
		t.Fatal("expected hint on first edit")
	}

	// Reset for a new run — checked set cleared, but fan-in cache preserved
	a.hubPackageGuard.reset()

	hint2 := a.checkHubPackage(filepath.Join(root, "internal", "hub", "hub.go"))
	if hint2 == "" {
		t.Error("expected hint after reset (new run)")
	}
}

// TestHubPackageGuard_TestFileSkipped verifies that test files don't trigger
// the hint.
func TestHubPackageGuard_TestFileSkipped(t *testing.T) {
	root := t.TempDir()

	writeHubGoFile(t, root, "go.mod", "module example.com/test\n\ngo 1.26\n")
	writeHubGoFile(t, root, "internal/hub/hub.go", "package hub\n\nfunc DoSomething() {}\n")

	for i := 0; i < 6; i++ {
		dir := filepath.Join(root, "internal", "c"+string(rune('a'+i)))
		writeHubGoFile(t, dir, "main.go",
			"package c"+string(rune('a'+i))+"\n\nimport \"example.com/test/internal/hub\"\n\nfunc Use() { hub.DoSomething() }\n")
	}

	a := &Agent{
		hubPackageGuard: newHubPackageState(),
		workingDir:      root,
	}

	hint := a.checkHubPackage(filepath.Join(root, "internal", "hub", "hub_test.go"))
	if hint != "" {
		t.Errorf("expected no hint for test file, got: %s", hint)
	}
}

// TestHubPackageGuard_NonGoFileSkipped verifies that non-Go files don't trigger.
func TestHubPackageGuard_NonGoFileSkipped(t *testing.T) {
	root := t.TempDir()

	writeHubGoFile(t, root, "go.mod", "module example.com/test\n\ngo 1.26\n")
	writeHubGoFile(t, root, "internal/hub/hub.go", "package hub\n\nfunc DoSomething() {}\n")

	a := &Agent{
		hubPackageGuard: newHubPackageState(),
		workingDir:      root,
	}

	hint := a.checkHubPackage(filepath.Join(root, "internal", "hub", "README.md"))
	if hint != "" {
		t.Errorf("expected no hint for non-Go file, got: %s", hint)
	}
}

// TestHubPackageGuard_NonGoProjectSkipped verifies that non-Go projects produce
// no hints.
func TestHubPackageGuard_NonGoProjectSkipped(t *testing.T) {
	root := t.TempDir()
	// No go.mod
	writeHubGoFile(t, root, "src/main.py", "print('hello')\n")

	a := &Agent{
		hubPackageGuard: newHubPackageState(),
		workingDir:      root,
	}

	hint := a.checkHubPackage(filepath.Join(root, "src", "main.py"))
	if hint != "" {
		t.Errorf("expected no hint for non-Go project, got: %s", hint)
	}
}

// TestHubPackageGuard_LazyInitialization verifies that fan-in is only computed
// once and cached for subsequent checks.
func TestHubPackageGuard_LazyInitialization(t *testing.T) {
	root := t.TempDir()

	writeHubGoFile(t, root, "go.mod", "module example.com/test\n\ngo 1.26\n")
	writeHubGoFile(t, root, "internal/hub/hub.go", "package hub\n\nfunc DoSomething() {}\n")
	writeHubGoFile(t, root, "internal/hub2/hub2.go", "package hub2\n\nfunc DoSomethingElse() {}\n")

	for i := 0; i < 6; i++ {
		dir := filepath.Join(root, "internal", "c"+string(rune('a'+i)))
		writeHubGoFile(t, dir, "main.go",
			"package c"+string(rune('a'+i))+"\n\nimport (\n\"example.com/test/internal/hub\"\n\"example.com/test/internal/hub2\"\n)\n\nfunc Use() { hub.DoSomething(); hub2.DoSomethingElse() }\n")
	}

	state := newHubPackageState()
	a := &Agent{
		hubPackageGuard: state,
		workingDir:      root,
	}

	// First call triggers lazy computation
	hint1 := a.checkHubPackage(filepath.Join(root, "internal", "hub", "hub.go"))
	if hint1 == "" {
		t.Fatal("expected hint for hub package")
	}
	if !state.initialized {
		t.Error("expected fanIn to be initialized after first check")
	}

	// Second call to a DIFFERENT package in same session should use cached map
	hint2 := a.checkHubPackage(filepath.Join(root, "internal", "hub2", "hub2.go"))
	if hint2 == "" {
		t.Fatal("expected hint for hub2 package using cached fanIn")
	}
}

// TestHubReadModulePath verifies module path extraction from go.mod.
func TestHubReadModulePath(t *testing.T) {
	tests := []struct {
		name  string
		goMod string
		want  string
	}{
		{"standard", "module github.com/foo/bar\n\ngo 1.26\n", "github.com/foo/bar"},
		{"with comment", "// comment\nmodule example.com/test\n\ngo 1.26\n", "example.com/test"},
		{"no module line", "go 1.26\n", ""},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.goMod != "" {
				writeHubGoFile(t, root, "go.mod", tc.goMod)
			}
			got := hubReadModulePath(root)
			if got != tc.want {
				t.Errorf("hubReadModulePath() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestHubFileToImportPath verifies conversion of file paths to import paths.
func TestHubFileToImportPath(t *testing.T) {
	tests := []struct {
		filePath string
		want     string
	}{
		{"internal/config/config.go", "example.com/test/internal/config"},
		{"main.go", "example.com/test"},
		{"cmd/ggcode/main.go", "example.com/test/cmd/ggcode"},
	}
	root := t.TempDir()
	for _, tc := range tests {
		got := hubFileToImportPath(root, tc.filePath, "example.com/test")
		if got != tc.want {
			t.Errorf("hubFileToImportPath(%q) = %q, want %q", tc.filePath, got, tc.want)
		}
	}
}

// TestHubComputeFanIn verifies that fan-in counting is accurate.
func TestHubComputeFanIn(t *testing.T) {
	root := t.TempDir()

	writeHubGoFile(t, root, "go.mod", "module example.com/test\n\ngo 1.26\n")
	writeHubGoFile(t, root, "pkg/a/a.go", "package a\nfunc A() {}\n")
	writeHubGoFile(t, root, "pkg/b/b.go", "package b\nimport \"example.com/test/pkg/a\"\nfunc B() { a.A() }\n")
	writeHubGoFile(t, root, "pkg/c/c.go", "package c\nimport \"example.com/test/pkg/a\"\nfunc C() { a.A() }\n")

	fanIn := hubComputeFanIn(root, "example.com/test")
	if fanIn["example.com/test/pkg/a"] != 2 {
		t.Errorf("pkg/a fan-in = %d, want 2", fanIn["example.com/test/pkg/a"])
	}
	if fanIn["example.com/test/pkg/b"] != 0 {
		t.Errorf("pkg/b fan-in = %d, want 0", fanIn["example.com/test/pkg/b"])
	}
}
