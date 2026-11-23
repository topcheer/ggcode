package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustWriteTreeCR(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteCR(t, full, content)
}

// The self-review summary must not list harness state (.ggcode/) or anything
// a .gitignore rule matches - even when those files are TRACKED (git diff
// lists tracked files and gitignore does not apply to them).
func TestDiffSummaryGate_ExcludesHarnessStateAndIgnoredPaths(t *testing.T) {
	dir := initGitRepo(t)
	mustWriteCR(t, filepath.Join(dir, ".gitignore"), "build/\n*.gen.txt\n")
	for _, p := range []string{"main.go", "util.go", ".ggcode/state.json", "build/gen.go", "notes.gen.txt"} {
		mustWriteTreeCR(t, dir, p, "v1\n")
	}
	runGitCR(t, dir, "add", "-f", ".")
	runGitCR(t, dir, "commit", "-q", "-m", "init")

	// every tracked file now differs from HEAD
	for _, p := range []string{"main.go", "util.go", ".ggcode/state.json", "build/gen.go", "notes.gen.txt"} {
		mustWriteTreeCR(t, dir, p, "v2\n")
	}

	a := NewAgent(nil, nil, "sys", 5)
	a.SetWorkingDir(dir)
	stats := &RunStats{ToolCalls: map[string]int{"edit_file": 1}}

	out := a.checkDiffSummaryGate(stats)
	for _, want := range []string{"main.go", "util.go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary must list real change %s, got:\n%s", want, out)
		}
	}
	for _, noise := range []string{".ggcode/state.json", "state.json", "build/gen.go", "notes.gen.txt"} {
		if strings.Contains(out, noise) {
			t.Fatalf("summary must not list %q, got:\n%s", noise, out)
		}
	}
}

// If filtering leaves fewer than the minimum, the gate stays silent instead
// of nagging about harness files only.
func TestDiffSummaryGate_SilentWhenOnlyNoiseChanged(t *testing.T) {
	dir := initGitRepo(t)
	mustWriteCR(t, filepath.Join(dir, ".gitignore"), "build/\n")
	for _, p := range []string{"main.go", ".ggcode/a.json", ".ggcode/b.json", "build/gen.go"} {
		mustWriteTreeCR(t, dir, p, "v1\n")
	}
	runGitCR(t, dir, "add", "-f", ".")
	runGitCR(t, dir, "commit", "-q", "-m", "init")
	for _, p := range []string{".ggcode/a.json", ".ggcode/b.json", "build/gen.go"} {
		mustWriteTreeCR(t, dir, p, "v2\n")
	}

	a := NewAgent(nil, nil, "sys", 5)
	a.SetWorkingDir(dir)
	if out := a.checkDiffSummaryGate(&RunStats{ToolCalls: map[string]int{"edit_file": 1}}); out != "" {
		t.Fatalf("only harness/ignored files changed - gate must stay silent, got:\n%s", out)
	}
}

func TestIsHarnessStatePath(t *testing.T) {
	for _, p := range []string{".ggcode", ".ggcode/x.json", "./.ggcode/deep/y.json", ".ggcode\\win.json"} {
		if !isHarnessStatePath(p) {
			t.Errorf("isHarnessStatePath(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"ggcode.json", "src/.ggcode-like/x.go", "internal/tui/model.go"} {
		if isHarnessStatePath(p) {
			t.Errorf("isHarnessStatePath(%q) = true, want false", p)
		}
	}
}
