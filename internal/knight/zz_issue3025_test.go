package knight

// Regression probes for #3025: LastFor's scope gate skipped the comparison
// when a stored entry had an EMPTY scope (double-sided wildcard), so one
// empty-scope rejection matched lookups in every scope - the same skill name
// cooled in both "project" and "global". Write side now normalizes empty
// scopes to "project"; lookups match exactly.

import (
	"path/filepath"
	"testing"
	"time"
)

func TestIssue3025_EmptyScopeEntryDoesNotMatchOtherScopes(t *testing.T) {
	dir := t.TempDir()
	s := newRejectFeedbackStore(filepath.Join(dir, "rej.jsonl"))
	// Legacy entry written before write-side normalization (e.Scope == "").
	if err := s.Append(rejectFeedbackEntry{Name: "my-skill", Scope: "", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LastFor("global", "my-skill"); ok {
		t.Fatal("#3025: empty-scope entry must not match a global lookup (double-sided wildcard)")
	}
	if _, ok := s.LastFor("project", "my-skill"); !ok {
		t.Fatal("empty-scope legacy entry should still be reachable via the project default lookup")
	}
}

func TestIssue3025_AppendNormalizesEmptyScope(t *testing.T) {
	dir := t.TempDir()
	s := newRejectFeedbackStore(filepath.Join(dir, "rej.jsonl"))
	if err := s.Append(rejectFeedbackEntry{Name: "x", Scope: "  ", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	e, ok := s.LastFor("project", "x")
	if !ok {
		t.Fatal("normalized entry must be reachable as project scope")
	}
	if e.Scope != "project" {
		t.Fatalf("empty/blank scope must be normalized to \"project\" on write, got %q", e.Scope)
	}
	if _, ok := s.LastFor("global", "x"); ok {
		t.Fatal("normalized entry must not leak into the global scope")
	}
}

func TestIssue3025_ExactScopeMatchingStillWorks(t *testing.T) {
	dir := t.TempDir()
	s := newRejectFeedbackStore(filepath.Join(dir, "rej.jsonl"))
	now := time.Now()
	if err := s.Append(rejectFeedbackEntry{Name: "skill", Scope: "global", Time: now}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LastFor("global", "skill"); !ok {
		t.Fatal("matching scope must be found")
	}
	if _, ok := s.LastFor("project", "skill"); ok {
		t.Fatal("non-matching scope must not be found")
	}
	// Empty query scope keeps its intentional any-scope semantics.
	if _, ok := s.LastFor("", "skill"); !ok {
		t.Fatal("empty query scope intentionally matches any entry scope")
	}
}
