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

func TestHubPackageGuard_HubPackageFires(t *testing.T) {
	root := t.TempDir()

	writeHubGoFile(t, root, "go.mod", "module example.com/test\n\ngo 1.26\n")

	writeHubGoFile(t, root, "internal/hub/hub.go", "package hub\n\nfunc DoSomething() {}\n")

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

func TestHubPackageGuard_LeafPackageNoFire(t *testing.T) {
	root := t.TempDir()

	writeHubGoFile(t, root, "go.mod", "module example.com/test\n\ngo 1.26\n")
	writeHubGoFile(t, root, "internal/leaf/leaf.go", "package leaf\n\nfunc Leaf() {}\n")

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

func TestHubPackageGuard_LatchOnlyAfterKeyingSuccess(t *testing.T) {
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

	a.hubPackageGuard.ensureFanIn(root)

	nested := filepath.Join(root, "internal", "nested")
	if err := os.MkdirAll(nested, 0o0755); err != nil {
		t.Fatal(err)
	}
	nestedMod := filepath.Join(nested, "go.mod")
	if err := os.WriteFile(nestedMod, []byte("module example.com/test/internal/nested\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(nestedMod, 0o000); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(nested, "x.go")
	if err := os.WriteFile(target, []byte("package nested\n"), 0o644); err != nil {
		os.Chmod(nestedMod, 0o644)
		t.Fatal(err)
	}
	if hint := a.checkHubPackage(target); hint != "" {
		t.Fatalf("expected no hint while go.mod unreadable, got: %s", hint)
	}
	if n := len(a.hubPackageGuard.checked); n != 0 {
		t.Fatalf("expected checked to stay empty after transient keying failure, has %d entries", n)
	}

	if err := os.Chmod(nestedMod, 0o644); err != nil {
		t.Fatal(err)
	}
	if hint := a.checkHubPackage(target); hint != "" {
		t.Fatalf("expected no hint for non-hub nested file, got: %s", hint)
	}
	if n := len(a.hubPackageGuard.checked); n != 1 {
		t.Fatalf("expected 1 checked entry after retry keyed successfully, got %d", n)
	}
}

func TestHubNearestGoModStatIndeterminate(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: EACCES cannot be triggered via chmod")
	}
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "go.mod"), []byte("module example.com/nested\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(nested, 0o0600); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(nested, 0o0755)

	_, _, ok := hubNearestGoMod(filepath.Join(nested, "pkg"), root)
	if ok {
		t.Fatal("expected ok=false (indeterminate) for unstattable go.mod, got ok=true - would mis-key via root fallback")
	}
}

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

	a.hubPackageGuard.reset()

	hint2 := a.checkHubPackage(filepath.Join(root, "internal", "hub", "hub.go"))
	if hint2 == "" {
		t.Error("expected hint after reset (new run)")
	}
}

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

func TestHubPackageGuard_NonGoProjectSkipped(t *testing.T) {
	root := t.TempDir()

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

	hint1 := a.checkHubPackage(filepath.Join(root, "internal", "hub", "hub.go"))
	if hint1 == "" {
		t.Fatal("expected hint for hub package")
	}
	if !state.initialized {
		t.Error("expected fanIn to be initialized after first check")
	}

	hint2 := a.checkHubPackage(filepath.Join(root, "internal", "hub2", "hub2.go"))
	if hint2 == "" {
		t.Fatal("expected hint for hub2 package using cached fanIn")
	}
}

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

func TestHubGuardEmptyModulePathRetries(t *testing.T) {
	s := newHubPackageState()
	empty := t.TempDir()
	s.ensureFanIn(empty)
	if s.initialized {
		t.Fatal("empty modulePath latched initialized - lifetime disable")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.ensureFanIn(dir)
	if !s.initialized {
		t.Fatal("successful module lookup did not initialize")
	}
}
