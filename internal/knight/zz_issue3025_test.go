package knight

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestIssue3025_EmptyScopeEntryDoesNotWildcardAcrossScopes pins the exact-match
// scope semantics of LastFor (#3025): an entry stored with an empty scope must
// NOT cool down lookups for other scopes (previously `scope != "" && e.Scope != ""
// && e.Scope != scope` treated an empty stored scope as a wildcard), and Append
// normalizes an unset scope to "project" (the skill_validator default).
func TestIssue3025_EmptyScopeEntryDoesNotWildcardAcrossScopes(t *testing.T) {
	dir := t.TempDir()
	s := newRejectFeedbackStore(filepath.Join(dir, "rejects.jsonl"))

	// Legacy entry persisted with an empty scope (e.g. by an older build from
	// an LLM-undecided staging candidate). We inject it via direct entries to
	// simulate a pre-fix on-disk file rather than via Append (which now
	// normalizes).
	legacyTime := time.Now().Add(-time.Hour)
	s.mu.Lock()
	s.loaded = true // prevent load() from reading the nonexistent file
	s.entries = []rejectFeedbackEntry{{Time: legacyTime, Name: "MySkill", Scope: "", Action: "reject"}}
	s.mu.Unlock()

	// The global-scope lookup must NOT be cooled down by the empty-scope entry.
	if _, cooled := s.coolDownActive("global", "myskill", time.Now()); cooled {
		t.Fatalf("empty-scope legacy entry cooled down the global scope lookup: expected no match")
	}
	// Same for any non-project scope.
	if _, cooled := s.coolDownActive("other", "MySkill", time.Now()); cooled {
		t.Fatalf("empty-scope legacy entry cooled down the 'other' scope lookup: expected no match")
	}
}

// TestIssue3025_AppendNormalizesEmptyScopeToProject pins the write side: Append
// with an empty scope stores "project" so lookups keyed "project" keep matching
// while other scopes stay unaffected.
func TestIssue3025_AppendNormalizesEmptyScopeToProject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rejects.jsonl")
	s := newRejectFeedbackStore(path)

	if err := s.Append(rejectFeedbackEntry{Name: "foo", Scope: "", Action: "reject"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	last, ok := s.LastFor("project", "FOO")
	if !ok {
		t.Fatalf("project-scope lookup should match the normalized entry")
	}
	if last.Scope != "project" {
		t.Fatalf("stored scope = %q, want normalized \"project\"", last.Scope)
	}
	if _, cooled := s.coolDownActive("global", "foo", time.Now()); cooled {
		t.Fatalf("global scope must not be cooled down by a project-scoped reject")
	}
	// Persistence check: the on-disk line carries the normalized scope.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got := string(raw); !containsScopeProject(got) {
		t.Fatalf("on-disk entry missing normalized scope: %s", got)
	}
}

func containsScopeProject(line string) bool {
	// minimal substring check; the exact JSON field is "scope":"project"
	for i := 0; i+17 <= len(line); i++ {
		if line[i:i+17] == `"scope":"project"` {
			return true
		}
	}
	return false
}
