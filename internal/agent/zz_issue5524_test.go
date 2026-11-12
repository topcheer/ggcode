package agent

import (
	"strings"
	"testing"
)

// TestTurnImpactRoutesWorktreePaths pins the #5524 acceptance: files edited
// inside .ggcode/worktrees/<name>/ are attributed to that worktree (named),
// never listed as main-tree files, while main-tree files keep the original
// rendering.
func TestTurnImpactRoutesWorktreePaths(t *testing.T) {
	r := newTurnImpactRecorder()
	r.recordFileEdit("edit_file", "/repo/internal/b.go")
	r.recordFileEdit("write_file", "/repo/.ggcode/worktrees/fix-x/internal/a.go")

	txt := r.report().render()
	for _, want := range []string{
		"Files touched in worktree fix-x",
		"/repo/.ggcode/worktrees/fix-x/internal/a.go (via write_file)",
		"- /repo/internal/b.go (via edit_file)",
	} {
		if !strings.Contains(txt, want) {
			t.Fatalf("render missing %q:\n%s", want, txt)
		}
	}
	// The main-tree "Files touched (N):" section must not contain worktree
	// paths (zero mis-attribution).
	if strings.Contains(txt, "Files touched (2):\n- /repo/.ggcode/worktrees") {
		t.Fatalf("worktree path leaked into main-tree section:\n%s", txt)
	}
}

// TestTurnImpactWorktreeOnlyZeroMainTreeAttribution: a worktree-only run
// produces no main-tree "Files touched (N):" header at all.
func TestTurnImpactWorktreeOnlyZeroMainTreeAttribution(t *testing.T) {
	r := newTurnImpactRecorder()
	r.recordFileEdit("write_file", "/repo/.ggcode/worktrees/fix-y/internal/agent/x.go")
	txt := r.report().render()
	if strings.Contains(txt, "Files touched (") && !strings.Contains(txt, "Files touched in worktree") {
		t.Fatalf("worktree-only run must not emit a main-tree files header:\n%s", txt)
	}
	if strings.Count(txt, "Files touched in worktree fix-y") != 1 {
		t.Fatalf("worktree must be named exactly once:\n%s", txt)
	}
	if strings.Contains(txt, "Files touched (1):") {
		t.Fatalf("no main-tree attribution expected:\n%s", txt)
	}
}

// TestTurnImpactMainTreeOnlyUnchanged: main-tree-only reports keep the
// legacy byte format (existing tests and downstream parsing stay valid).
func TestTurnImpactMainTreeOnlyUnchanged(t *testing.T) {
	r := newTurnImpactRecorder()
	r.recordFileEdit("edit_file", "/repo/internal/b.go")
	txt := r.report().render()
	if !strings.Contains(txt, "Files touched (1):\n- /repo/internal/b.go (via edit_file)\n") {
		t.Fatalf("main-tree format must be unchanged:\n%s", txt)
	}
	if strings.Contains(txt, "Worktree") {
		t.Fatalf("no worktree section expected for main-tree-only run:\n%s", txt)
	}
}

// TestResolveImpactPath: relative edit paths anchor against the session
// working directory so worktree sessions keep provenance (#5524).
func TestResolveImpactPath(t *testing.T) {
	cases := []struct {
		wd, in, want string
	}{
		{"/repo/.ggcode/worktrees/w", "internal/a.go", "/repo/.ggcode/worktrees/w/internal/a.go"},
		{"/repo", "/abs/path/x.go", "/abs/path/x.go"},
		{"", "rel.go", "rel.go"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := resolveImpactPath(c.wd, c.in); got != c.want {
			t.Errorf("resolveImpactPath(%q, %q) = %q, want %q", c.wd, c.in, got, c.want)
		}
	}
}

func TestWorktreeSegment(t *testing.T) {
	name, _, ok := worktreeSegment("/repo/.ggcode/worktrees/fix-x/internal/a.go")
	if !ok || name != "fix-x" {
		t.Fatalf("worktreeSegment(worktree path) = %q, %v; want fix-x, true", name, ok)
	}
	if _, _, ok := worktreeSegment("/repo/internal/a.go"); ok {
		t.Fatal("main-tree path must not route to a worktree")
	}
	if _, _, ok := worktreeSegment("/repo/.ggcode/worktrees"); ok {
		t.Fatal("worktrees root without a name/file must not route")
	}
	if _, _, ok := worktreeSegment("/repo/.ggcode/worktrees/fix-x"); ok {
		t.Fatal("worktree root itself (no file) must not route")
	}
}
