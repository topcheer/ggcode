package tool

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewFileGuard_Defaults(t *testing.T) {
	g := NewFileGuard(nil)
	pats := g.Patterns()
	if len(pats) != 3 {
		t.Fatalf("expected 3 default patterns, got %d: %v", len(pats), pats)
	}
	// Defaults should include .env and .git/
	found := map[string]bool{}
	for _, p := range pats {
		found[p] = true
	}
	if !found[".env"] {
		t.Error("expected .env in defaults")
	}
	if !found[".env.*"] {
		t.Error("expected .env.* in defaults")
	}
	if !found[".git/"] {
		t.Error("expected .git/ in defaults")
	}
}

func TestNewFileGuard_Explicit(t *testing.T) {
	custom := []string{"*.lock", "secrets/**"}
	g := NewFileGuard(custom)
	pats := g.Patterns()
	if len(pats) != 2 {
		t.Fatalf("expected 2 custom patterns, got %d: %v", len(pats), pats)
	}
}

func TestFileGuard_IsProtected(t *testing.T) {
	dir := t.TempDir()
	g := NewFileGuard(nil)

	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{"env file at root", filepath.Join(dir, ".env"), true},
		{"env.local", filepath.Join(dir, ".env.local"), true},
		{"git config", filepath.Join(dir, ".git", "config"), true},
		{"git HEAD", filepath.Join(dir, ".git", "HEAD"), true},
		{"normal file", filepath.Join(dir, "main.go"), false},
		{"nested normal file", filepath.Join(dir, "src", "app.go"), false},
		{"nested env", filepath.Join(dir, "config", ".env"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			abs, _ := filepath.Abs(tt.path)
			protected, _ := g.IsProtected(abs, dir)
			if protected != tt.expected {
				t.Errorf("IsProtected(%q) = %v, want %v", tt.path, protected, tt.expected)
			}
		})
	}
}

func TestFileGuard_CustomPatterns(t *testing.T) {
	dir := t.TempDir()
	g := NewFileGuard([]string{"*.lock", "src/secrets/**"})

	// *.lock should match lock files
	protected, _ := g.IsProtected(filepath.Join(dir, "package-lock.json"), dir)
	// *.lock glob matches "package-lock.json" via filepath.Match? No - *.lock matches filenames ending in .lock
	// "package-lock.json" does NOT end in .lock, so it should NOT match
	if protected {
		t.Error("package-lock.json should not match *.lock")
	}

	// yarn.lock should match *.lock
	protected, _ = g.IsProtected(filepath.Join(dir, "yarn.lock"), dir)
	if !protected {
		t.Error("yarn.lock should match *.lock")
	}
}

func TestFileGuard_CheckWritePath(t *testing.T) {
	dir := t.TempDir()
	g := NewFileGuard(nil)

	// Protected path
	msg := g.CheckWritePath(filepath.Join(dir, ".env"), dir)
	if msg == "" {
		t.Error("expected protection message for .env")
	}

	// Normal path
	msg = g.CheckWritePath(filepath.Join(dir, "main.go"), dir)
	if msg != "" {
		t.Errorf("expected empty message for main.go, got: %s", msg)
	}
}

func TestFileGuard_NilGuard_AllowsAll(t *testing.T) {
	var nilGuard *FileGuard
	checker := MergeFileGuards(nil, nilGuard, "")

	if checker != nil {
		t.Error("MergeFileGuards with nil guard should return original checker")
	}
}

func TestMergeFileGuards_ProtectedPath(t *testing.T) {
	dir := t.TempDir()
	g := NewFileGuard(nil)

	// Wrap with MergeFileGuards (no base sandbox)
	checker := MergeFileGuards(nil, g, dir)
	if checker == nil {
		t.Fatal("expected non-nil checker")
	}

	// Protected path should be blocked
	if checker(filepath.Join(dir, ".env")) {
		t.Error("expected .env to be blocked by file guard")
	}

	// Normal path should be allowed
	if !checker(filepath.Join(dir, "main.go")) {
		t.Error("expected main.go to be allowed")
	}
}

func TestLoadProtectedPatternsFromFile(t *testing.T) {
	dir := t.TempDir()

	// Non-existent file returns nil
	pats := LoadProtectedPatternsFromFile(dir)
	if pats != nil {
		t.Errorf("expected nil for non-existent file, got %v", pats)
	}

	// Create file
	ggcodeDir := filepath.Join(dir, ".ggcode")
	os.MkdirAll(ggcodeDir, 0755)
	content := "# Comment\n*.lock\n\n.env\n# another comment\nsecrets/**\n"
	os.WriteFile(filepath.Join(ggcodeDir, "protect"), []byte(content), 0644)

	pats = LoadProtectedPatternsFromFile(dir)
	if len(pats) != 3 {
		t.Fatalf("expected 3 patterns, got %d: %v", len(pats), pats)
	}
	if pats[0] != "*.lock" || pats[1] != ".env" || pats[2] != "secrets/**" {
		t.Errorf("unexpected patterns: %v", pats)
	}
}

func TestFileGuard_SymlinkEnv(t *testing.T) {
	// .env should be blocked regardless of working directory
	dir := t.TempDir()
	g := NewFileGuard(nil)

	// Create .env file
	envPath := filepath.Join(dir, ".env")
	abs, _ := filepath.Abs(envPath)
	protected, pattern := g.IsProtected(abs, dir)
	if !protected {
		t.Error("expected .env to be protected")
	}
	if pattern != ".env" && pattern != ".env.*" {
		t.Errorf("unexpected pattern: %s", pattern)
	}
}

func TestFileGuard_DeepNestedPath(t *testing.T) {
	dir := t.TempDir()
	g := NewFileGuard(nil)

	// .git in a deep path
	deepGit := filepath.Join(dir, "subdir", ".git", "refs", "heads", "main")
	abs, _ := filepath.Abs(deepGit)
	protected, _ := g.IsProtected(abs, dir)
	if !protected {
		t.Error("expected deep .git path to be protected")
	}
}
