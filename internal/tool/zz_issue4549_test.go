//go:build goolm

package tool

// Regression tests for GitHub issue #4549:
//   A) multi-** patterns silently returned a wrong subset: the second **
//      inside the remainder was treated as a plain single-segment glob by
//      the tail-aligned filepath.Match loop. '**/docs/**/*.md' missed files
//      directly under docs/; '**/**/*.go' missed depth-1 files entirely.
//   B) walkGlob stored filepath.Abs(path), so with a default directory of
//      "." the caller's Rel(".", abs) errored and absolute paths leaked,
//      diverging from filepath.Glob's relative-path output contract.

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func mkIssue4549Tree(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	for _, p := range []string{
		"src/docs/z.md",
		"a/docs/x.md",
		"a/docs/deep/y.md",
		"a/other/w.md",
		"c.go",
		"a/b.go",
		"a/deep/d.go",
	} {
		full := filepath.Join(base, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return base
}

// Issue repro A1: '**/docs/**/*.md' must match every .md under any docs/
// directory at any depth (z.md and x.md were silently missed before).
func TestIssue4549_MultiStarStarDocsMd(t *testing.T) {
	base := mkIssue4549Tree(t)
	got := walkGlob(base, "**/docs/**/*.md", nil)
	rel := make([]string, 0, len(got))
	for _, m := range got {
		r, err := filepath.Rel(base, m)
		if err != nil {
			t.Fatalf("Rel failed for %q: %v", m, err)
		}
		rel = append(rel, filepath.ToSlash(r))
	}
	sort.Strings(rel)
	want := []string{"a/docs/deep/y.md", "a/docs/x.md", "src/docs/z.md"}
	if len(rel) != len(want) {
		t.Fatalf("got %v, want %v", rel, want)
	}
	for i := range want {
		if rel[i] != want[i] {
			t.Fatalf("got %v, want %v", rel, want)
		}
	}
}

// Issue repro A2: '**/**/*.go' must match depth-1 files (c.go was missed
// before because the tail alignment required len(rSegs) >= len(sSegs)).
func TestIssue4549_MultiStarStarDepthOne(t *testing.T) {
	base := mkIssue4549Tree(t)
	got := walkGlob(base, "**/**/*.go", nil)
	if len(got) != 3 { // c.go, a/b.go, a/deep/d.go — incl. depth-1 c.go
		rels := make([]string, 0, len(got))
		for _, m := range got {
			r, _ := filepath.Rel(base, m)
			rels = append(rels, filepath.ToSlash(r))
		}
		t.Fatalf("got %v (len %d), want 3 files incl. depth-1 c.go", rels, len(got))
	}
}

// B: results must be walk-relative paths, not absolute, so the caller's
// Rel(args.Directory, m) with directory "." yields relative output.
func TestIssue4549_RelativePathContract(t *testing.T) {
	base := mkIssue4549Tree(t)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the caller: directory defaults to ".".
	if err := os.Chdir(base); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd) //nolint:errcheck
	got := walkGlob(".", "**/*.go", nil)
	if len(got) == 0 {
		t.Fatal("expected matches")
	}
	for _, m := range got {
		if filepath.IsAbs(m) {
			t.Fatalf("result %q is absolute; ** output must stay relative like filepath.Glob", m)
		}
		rel, err := filepath.Rel(".", m)
		if err != nil {
			t.Fatalf("Rel(\".\", %q) failed: %v — output contract broken", m, err)
		}
		if rel != m {
			t.Fatalf("Rel(\".\", %q) = %q, expected identity", m, rel)
		}
	}
}

// Guard: single-** semantics from #829 must be unchanged.
func TestIssue4549_SingleStarStarRegression(t *testing.T) {
	if !matchStarStarSuffix("*.go", "sub/nested.go") {
		t.Fatal("single-segment wildcard suffix must match base at any depth (#829)")
	}
	if !matchStarStarSuffix("docs/*.md", "x/docs/a.md") {
		t.Fatal("multi-segment suffix must still align at the tail")
	}
	if matchStarStarSuffix("docs/*.md", "x/docs/deep/a.md") {
		t.Fatal("single-level docs/*.md must NOT match a deeper file")
	}
}
