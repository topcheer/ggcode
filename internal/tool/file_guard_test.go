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

	protected, _ := g.IsProtected(filepath.Join(dir, "package-lock.json"), dir)

	if protected {
		t.Error("package-lock.json should not match *.lock")
	}

	protected, _ = g.IsProtected(filepath.Join(dir, "yarn.lock"), dir)
	if !protected {
		t.Error("yarn.lock should match *.lock")
	}
}

func TestFileGuard_CheckWritePath(t *testing.T) {
	dir := t.TempDir()
	g := NewFileGuard(nil)

	msg := g.CheckWritePath(filepath.Join(dir, ".env"), dir)
	if msg == "" {
		t.Error("expected protection message for .env")
	}

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

	checker := MergeFileGuards(nil, g, dir)
	if checker == nil {
		t.Fatal("expected non-nil checker")
	}

	if checker(filepath.Join(dir, ".env")) {
		t.Error("expected .env to be blocked by file guard")
	}

	if !checker(filepath.Join(dir, "main.go")) {
		t.Error("expected main.go to be allowed")
	}
}

func TestFileGuard_SymlinkEnv(t *testing.T) {

	dir := t.TempDir()
	g := NewFileGuard(nil)

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

	deepGit := filepath.Join(dir, "subdir", ".git", "refs", "heads", "main")
	abs, _ := filepath.Abs(deepGit)
	protected, _ := g.IsProtected(abs, dir)
	if !protected {
		t.Error("expected deep .git path to be protected")
	}
}

func TestFileGuardSymlinkBypass(t *testing.T) {

	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("k=v"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}

	g := NewFileGuard(nil)

	if err := os.Symlink(filepath.Join(root, ".git"), filepath.Join(ws, "lnk")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	if msg := g.CheckWritePath(filepath.Join(ws, "lnk", "config"), ws); msg == "" {
		t.Error("write via symlink into .git must be blocked")
	}

	if err := os.Symlink(filepath.Join(root, ".env"), filepath.Join(ws, "lnk2")); err != nil {
		t.Fatal(err)
	}
	if msg := g.CheckWritePath(filepath.Join(ws, "lnk2"), ws); msg == "" {
		t.Error("write via symlink to .env must be blocked")
	}

	if msg := g.CheckWritePath(filepath.Join(ws, "normal.go"), ws); msg != "" {
		t.Errorf("normal write blocked: %s", msg)
	}
}

func TestFileGuard_DirPrefixOutsideWorkingDir(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	g := NewFileGuard(nil)

	outside := filepath.Join(root, "other", ".git", "config")
	protected, _ := g.IsProtected(outside, ws)
	if !protected {
		t.Error("expected .git/ component outside working dir to stay protected")
	}

	plain := filepath.Join(root, "other", "notes.txt")
	if protected, _ := g.IsProtected(plain, ws); protected {
		t.Error("plain outside path must not be protected")
	}
}

func TestProtectedPatternFoldsOnCaseInsensitiveFS(t *testing.T) {
	if !fsCaseInsensitive() {
		t.Skip("host FS is case-sensitive - fold path not compiled in")
	}
	if !matchProtectedPattern(".GIT/config", ".GIT/config", ".GIT/config") {
		t.Fatal(".GIT dir segment must fold to the protected .git pattern on this FS")
	}
	if !matchProtectedPattern(".ENV", ".ENV", ".ENV") {
		t.Fatal(".ENV glob must fold to the protected .env pattern on this FS")
	}
}
