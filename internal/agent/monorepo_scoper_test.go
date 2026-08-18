package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestMonorepoScoper_DetectByMarker(t *testing.T) {
	tmp := t.TempDir()
	// Create a pnpm-workspace.yaml marker
	os.WriteFile(filepath.Join(tmp, "pnpm-workspace.yaml"), []byte("packages:\n  - packages/*"), 0644)
	// Create packages
	for _, pkg := range []string{"pkg-a", "pkg-b", "pkg-c"} {
		dir := filepath.Join(tmp, pkg)
		os.MkdirAll(dir, 0755)
		os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0644)
	}

	s := newMonorepoScoperState()
	s.detectMonorepo(tmp)

	if !s.enabled {
		t.Fatal("expected monorepo detection via marker file")
	}
	if s.rootDir != tmp {
		t.Errorf("expected rootDir %s, got %s", tmp, s.rootDir)
	}
}

func TestMonorepoScoper_DetectByMultiplePackages(t *testing.T) {
	tmp := t.TempDir()
	// No marker file, but 3+ packages
	for _, pkg := range []string{"svc-a", "svc-b", "svc-c"} {
		dir := filepath.Join(tmp, pkg)
		os.MkdirAll(dir, 0755)
		os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/"+pkg), 0644)
	}

	s := newMonorepoScoperState()
	s.detectMonorepo(tmp)

	if !s.enabled {
		t.Fatal("expected monorepo detection via multiple packages")
	}
}

func TestMonorepoScoper_NotMonorepo(t *testing.T) {
	tmp := t.TempDir()
	// Only one package, no markers
	dir := filepath.Join(tmp, "single-pkg")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0644)

	s := newMonorepoScoperState()
	s.detectMonorepo(tmp)

	if s.enabled {
		t.Fatal("expected no monorepo detection for single package")
	}
}

func TestMonorepoScoper_ClassifyPackage(t *testing.T) {
	tmp := t.TempDir()
	for _, pkg := range []string{"users", "orders", "billing"} {
		dir := filepath.Join(tmp, pkg)
		os.MkdirAll(dir, 0755)
		os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0644)
	}

	s := newMonorepoScoperState()
	s.detectMonorepo(tmp)

	cases := []struct {
		path string
		want string
	}{
		{filepath.Join(tmp, "users", "src", "index.ts"), "users"},
		{filepath.Join(tmp, "orders", "lib", "util.go"), "orders"},
		{filepath.Join(tmp, "billing", "main.go"), "billing"},
		{filepath.Join(tmp, "shared", "types.ts"), ""}, // cross-cutting
		{filepath.Join(tmp, "internal", "foo.go"), ""}, // cross-cutting
		{filepath.Join(tmp, "README.md"), ""},          // root file
	}

	for _, tc := range cases {
		got := s.classifyPackage(tc.path)
		if got != tc.want {
			t.Errorf("classifyPackage(%s) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestMonorepoScoper_ScopeSprawlWarning(t *testing.T) {
	tmp := t.TempDir()
	for _, pkg := range []string{"users", "orders", "billing", "shipping", "auth"} {
		dir := filepath.Join(tmp, pkg)
		os.MkdirAll(dir, 0755)
		os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0644)
	}

	s := newMonorepoScoperState()
	s.detectMonorepo(tmp)

	if !s.enabled {
		t.Fatal("expected monorepo detection")
	}

	// Edit in 4 packages - should trigger warning
	for _, pkg := range []string{"users", "orders", "billing", "shipping"} {
		s.recordEdit(filepath.Join(tmp, pkg, "index.ts"))
	}

	hint := s.maybeWarnScopeSprawl()
	if hint == "" {
		t.Fatal("expected scope sprawl warning for 4 packages")
	}
	if !strings.Contains(hint, "monorepo-scope") {
		t.Errorf("hint should contain monorepo-scope tag: %s", hint)
	}

	// Should fire only once
	hint2 := s.maybeWarnScopeSprawl()
	if hint2 != "" {
		t.Fatal("expected no second warning (already fired)")
	}
}

func TestMonorepoScoper_NoSprawlForTwoPackages(t *testing.T) {
	tmp := t.TempDir()
	for _, pkg := range []string{"users", "orders", "billing"} {
		dir := filepath.Join(tmp, pkg)
		os.MkdirAll(dir, 0755)
		os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0644)
	}

	s := newMonorepoScoperState()
	s.detectMonorepo(tmp)

	// Edit in only 2 packages - should NOT trigger
	s.recordEdit(filepath.Join(tmp, "users", "index.ts"))
	s.recordEdit(filepath.Join(tmp, "orders", "index.ts"))

	hint := s.maybeWarnScopeSprawl()
	if hint != "" {
		t.Fatalf("expected no warning for 2 packages, got: %s", hint)
	}
}

func TestMonorepoScoper_Reset(t *testing.T) {
	s := &monorepoScoperState{
		enabled:     true,
		rootDir:     "/test",
		touchedDirs: map[string]int{"pkg-a": 3},
		fired:       true,
		crossPkg:    map[string]bool{"shared": true},
	}

	s.reset()

	if s.fired {
		t.Error("fired should be false after reset")
	}
	if len(s.touchedDirs) != 0 {
		t.Error("touchedDirs should be empty after reset")
	}
	// enabled, rootDir, packages should persist (workspace structure doesn't change)
	if !s.enabled {
		t.Error("enabled should persist after reset")
	}
}

func TestMonorepoScoper_CrossCuttingDirsNotCounted(t *testing.T) {
	tmp := t.TempDir()
	for _, pkg := range []string{"users", "orders", "billing", "auth", "api"} {
		dir := filepath.Join(tmp, pkg)
		os.MkdirAll(dir, 0755)
		os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0644)
	}
	// Also create cross-cutting dirs
	for _, d := range []string{"shared", "common", "lib"} {
		os.MkdirAll(filepath.Join(tmp, d), 0755)
	}

	s := newMonorepoScoperState()
	s.detectMonorepo(tmp)

	// Edit in 2 packages + 3 cross-cutting dirs
	s.recordEdit(filepath.Join(tmp, "users", "index.ts"))
	s.recordEdit(filepath.Join(tmp, "orders", "index.ts"))
	s.recordEdit(filepath.Join(tmp, "shared", "types.ts"))
	s.recordEdit(filepath.Join(tmp, "common", "util.ts"))
	s.recordEdit(filepath.Join(tmp, "lib", "helper.ts"))

	// Only 2 actual packages touched (cross-cutting dirs excluded)
	hint := s.maybeWarnScopeSprawl()
	if hint != "" {
		t.Fatalf("expected no sprawl warning (only 2 actual packages), got: %s", hint)
	}
}

// TestMonorepoScoper_MixedSeparatorsClassify guards the Windows fix: rootDir
// comes from filepath.Join (native separators) while edit paths arrive as the
// LLM's literal tool arguments (forward slashes). Before the ToSlash
// normalization, prefix matching and SplitN both failed on Windows and the
// detector went permanently silent there. This test pins the mixed form on
// every platform.
func TestMonorepoScoper_MixedSeparatorsClassify(t *testing.T) {
	s := &monorepoScoperState{
		enabled:  true,
		rootDir:  filepath.Join(`C:\work`, "repo"), // native-separator root, as detectMonorepo records it
		crossPkg: map[string]bool{},
	}
	root := `C:/work/repo` // same root, LLM-style forward slashes
	cases := []struct{ path, want string }{
		{root + "/users/index.ts", "users"},
		{root + "/orders/lib/util.go", "orders"},
		{root + "/shared/types.ts", ""}, // cross-cutting
		{root + "/README.md", ""},       // root file
		// Native-separator paths must keep working too (same form as rootDir).
		{filepath.Join(root, "users", "index.ts"), "users"},
	}
	for _, tc := range cases {
		if got := s.classifyPackage(tc.path); got != tc.want {
			t.Errorf("classifyPackage(%s) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestItoa(t *testing.T) {
	// Verify strconv.Itoa produces expected output (used by formatScopeSprawlHint).
	cases := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{5, "5"},
		{10, "10"},
		{42, "42"},
		{100, "100"},
	}
	for _, tc := range cases {
		got := strconv.Itoa(tc.input)
		if got != tc.want {
			t.Errorf("Itoa(%d) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
