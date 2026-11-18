package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #4797 (S-28): an old_text anchor taken from an adjacent read still
// trips the content-hash guard when a concurrent formatter (gofmt, perl
// -pi, lint --fix, another seat on a shared worktree) rewrites the file
// between the read and the edit. The patrol ruling (issue comment
// 2026-11-13) keeps the guard's pass/fail direction as a product
// decision and blesses exactly the copy-face compromise pinned here:
// the mismatch warning must name the likely benign cause instead of a
// generic mismatch, while the conservative re-read guidance, the
// Note/Warning grading, and the warn-once bound all stay intact.

func TestIssue4797_MismatchHintNamesConcurrentRewrite(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "wire.go")

	// gofmt-shaped rewrite: same tokens, reformatted whitespace/tabs.
	before := "package main\nfunc A( ) {\nx:=1\n_ = x\n}\n"
	after := "package main\n\nfunc A() {\n\tx := 1\n\t_ = x\n}\n"
	if err := os.WriteFile(f, []byte(before), 0644); err != nil {
		t.Fatal(err)
	}

	tr := newReadHashTracker()
	tr.recordReadHash(f)

	// Concurrent formatter lands between the read and the edit.
	if err := os.WriteFile(f, []byte(after), 0644); err != nil {
		t.Fatal(err)
	}

	hint := tr.validateContentAtEdit(f, 100)
	if hint == "" {
		t.Fatal("expected mismatch warning to fire - conservative direction must be preserved")
	}
	if !strings.Contains(hint, "concurrent formatter or editor write") {
		t.Fatalf("mismatch warning must name the likely cause (S-28: generic mismatches train users to ignore real conflicts), got: %q", hint)
	}
	if !strings.Contains(hint, "shared worktree") {
		t.Fatalf("mismatch warning should mention the shared-worktree seat scenario, got: %q", hint)
	}
	if !strings.Contains(hint, "Re-read the file with read_file") {
		t.Fatalf("conservative re-read guidance must survive the copy change, got: %q", hint)
	}
}

func TestIssue4797_SmallEditBranchAlsoCarriesCause(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "small.go")

	before := "package main\nfunc A( ) {\nx:=1\n_ = x\n}\n"
	after := "package main\n\nfunc A() {\n\tx := 1\n\t_ = x\n}\n"
	if err := os.WriteFile(f, []byte(before), 0644); err != nil {
		t.Fatal(err)
	}

	tr := newReadHashTracker()
	tr.recordReadHash(f)

	if err := os.WriteFile(f, []byte(after), 0644); err != nil {
		t.Fatal(err)
	}

	hint := tr.validateContentAtEdit(f, 10)
	if hint == "" {
		t.Fatal("expected small-edit soft hint to fire")
	}
	if !strings.Contains(hint, "For this small edit") {
		t.Fatalf("small-edit soft grading must be preserved, got: %q", hint)
	}
	if !strings.Contains(hint, "concurrent formatter or editor write") {
		t.Fatalf("small-edit branch must also carry the concurrent-rewrite cause, got: %q", hint)
	}
}

func TestIssue4797_WarnOnceStillBoundsFrequency(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "svc.go")

	if err := os.WriteFile(f, []byte("old content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tr := newReadHashTracker()
	tr.recordReadHash(f)

	// Formatter storms rewrite repeatedly; the guard must not nag per edit.
	if err := os.WriteFile(f, []byte("new content\n\n"), 0644); err != nil {
		t.Fatal(err)
	}
	hint1 := tr.validateContentAtEdit(f, 100)
	if hint1 == "" {
		t.Fatal("expected first mismatch warning")
	}
	if err := os.WriteFile(f, []byte("new content\n\n\n"), 0644); err != nil {
		t.Fatal(err)
	}
	hint2 := tr.validateContentAtEdit(f, 100)
	if hint2 != "" {
		t.Fatalf("warn-once bound must cap false alarms at one per file per session, got second hint: %q", hint2)
	}
}

func TestIssue4797_NoHintWhenContentUnchanged(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "stable.go")

	if err := os.WriteFile(f, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tr := newReadHashTracker()
	tr.recordReadHash(f)

	hint := tr.validateContentAtEdit(f, 100)
	if hint != "" {
		t.Fatalf("unchanged file must not warn (no false alarm on the happy path), got: %q", hint)
	}
}
