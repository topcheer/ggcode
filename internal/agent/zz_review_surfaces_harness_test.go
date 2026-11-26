package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The adversarial evaluator's diff must not carry harness state: a
// .ggcode/ scope (an edit_file on .ggcode/memory/*.md) is dropped, the
// whole-tree fallback excludes the directory, and untracked .ggcode/ lines
// are not this run's output. Otherwise the memory curator's prune reached
// the evaluator as a wall of "deleted files" and the self-review as
// "92 deletions you must account for" (field report 2026-11-26).
func TestAdversarialDiffExcludesHarnessState(t *testing.T) {
	dir := t.TempDir()
	gitCmd5018(t, dir, "init", "-q")
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("mine.go", "package p\n\nconst A = 1\n")
	write(".ggcode/memory/tracked.md", "v1\n")
	gitCmd5018(t, dir, "add", "-f", ".")
	gitCmd5018(t, dir, "commit", "-q", "-m", "base")
	// This run edits mine.go and appends to a memory file; the curator prunes.
	write("mine.go", "package p\n\nconst A = 2\n")
	write(".ggcode/memory/tracked.md", "v2\n")
	write(".ggcode/memory/experience/untracked.md", "x\n")

	a := &Agent{}
	a.SetWorkingDir(dir)

	// 1. A .ggcode scope is dropped; the real scope still drives the diff.
	diff := a.collectAdversarialDiff([]string{"mine.go", filepath.Join(dir, ".ggcode", "memory", "tracked.md")})
	if !strings.Contains(diff, "const A = 2") {
		t.Fatalf("real edit missing:\n%s", diff)
	}
	if strings.Contains(diff, ".ggcode") {
		t.Fatalf(".ggcode scope / untracked line leaked into the scoped diff:\n%s", diff)
	}

	// 2. Only .ggcode scopes -> nothing of this run is reviewable: fall back
	//    to the whole tree WITHOUT the harness directory.
	diff = a.collectAdversarialDiff([]string{".ggcode/memory/tracked.md"})
	if strings.Contains(diff, ".ggcode") {
		t.Fatalf("harness-only scopes must not review harness state:\n%s", diff)
	}

	// 3. No scopes at all: whole-tree fallback excludes the harness dir.
	diff = a.collectAdversarialDiff(nil)
	if !strings.Contains(diff, "const A = 2") {
		t.Fatalf("whole-tree fallback must keep the real edit:\n%s", diff)
	}
	if strings.Contains(diff, ".ggcode") {
		t.Fatalf("whole-tree fallback must exclude harness state:\n%s", diff)
	}
}
