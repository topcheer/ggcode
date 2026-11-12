package agent

import (
	"path/filepath"
	"testing"
)

// #5524: changes inside .ggcode/worktrees/<name>/ must not be attributed to
// the main working tree (false "uncommitted changes" advisory in shared
// workspaces). The capture filter must skip worktree-scoped paths while
// keeping main-tree paths.
func TestIsWorktreeScopedPath(t *testing.T) {
	if !isWorktreeScopedPath(filepath.Join(".ggcode", "worktrees", "fix-5524", "internal", "agent", "x.go")) {
		t.Fatal("relative worktree path must be worktree-scoped")
	}
	abs := filepath.Join(string(filepath.Separator), "Volumes", "new", "ggai", "ggcode", ".ggcode", "worktrees", "fix-5524", "a.go")
	if !isWorktreeScopedPath(abs) {
		t.Fatal("absolute path containing worktrees component must be worktree-scoped")
	}
	for _, p := range []string{
		"internal/agent/x.go",
		filepath.Join(".ggcode", "skills", "deploy", "run.sh"),
		filepath.Join("vendor", "example.com", "worktrees", "x.go"),
	} {
		if isWorktreeScopedPath(p) {
			t.Fatalf("main-tree path %q must NOT be worktree-scoped", p)
		}
	}
}
