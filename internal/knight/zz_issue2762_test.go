package knight

import (
	"strings"
	"testing"
)

// TestMutateSkillFrontmatterClosingDelimiterOwnLine (#2762):
// splitFrontmatter skips the newline right after the closing "---", so
// reassembly must restore it. Before the fix, Promote/MigrateLooseActive/
// Rollback write paths silently glued the body's first line onto the
// closing marker ("---# Skill Usage"), producing frontmatter that external
// tools (Claude Code, markdownlint, static site generators) reject.
func TestMutateSkillFrontmatterClosingDelimiterOwnLine(t *testing.T) {
	src := "---\nname: x\n---\n# Skill Usage\n\nBody here.\n"
	got, err := mutateSkillFrontmatter(src, func(fm map[string]interface{}) {
		fm["updated_at"] = "2026-09-25T00:00:00Z"
	})
	if err != nil {
		t.Fatalf("mutateSkillFrontmatter: %v", err)
	}
	if strings.Contains(got, "---#") {
		t.Fatalf("closing --- glued to body first line (invalid frontmatter):\n%q", got)
	}
	// The closing delimiter must be followed by a newline so it occupies
	// its own line.
	if !strings.Contains(got, "\n---\n") {
		t.Fatalf("closing --- must own its line (expected \\n---\\n in output):\n%q", got)
	}
	if !strings.Contains(got, "# Skill Usage") || !strings.Contains(got, "Body here.") {
		t.Fatalf("body content lost:\n%q", got)
	}
}

// TestMutateSkillFrontmatterIdempotentAcrossRewrites: a second mutate on
// already-mutated content must not degrade further and must keep the
// delimiter on its own line (previously the first corruption was stable
// but invalid; now it must simply stay valid).
func TestMutateSkillFrontmatterIdempotentAcrossRewrites(t *testing.T) {
	src := "---\nname: x\n---\n# Skill Usage\n"
	mut := func(fm map[string]interface{}) {
		fm["updated_at"] = "2026-09-25T00:00:00Z"
	}
	first, err := mutateSkillFrontmatter(src, mut)
	if err != nil {
		t.Fatalf("first mutate: %v", err)
	}
	second, err := mutateSkillFrontmatter(first, mut)
	if err != nil {
		t.Fatalf("second mutate (must still parse): %v", err)
	}
	if strings.Contains(second, "---#") {
		t.Fatalf("second mutate reintroduced glued delimiter:\n%q", second)
	}
	if !strings.Contains(second, "\n---\n") {
		t.Fatalf("second mutate lost newline after closing ---:\n%q", second)
	}
}

// TestMutateSkillFrontmatterEmptyBodyKeepsValid: content whose body is
// empty after the closing marker still reassembles to valid frontmatter
// (file then simply ends with the closing marker + newline).
func TestMutateSkillFrontmatterEmptyBodyKeepsValid(t *testing.T) {
	src := "---\nname: x\n---\n"
	got, err := mutateSkillFrontmatter(src, func(fm map[string]interface{}) {
		fm["created_by"] = "knight"
	})
	if err != nil {
		t.Fatalf("mutateSkillFrontmatter: %v", err)
	}
	if !strings.HasSuffix(got, "\n---\n") {
		t.Fatalf("empty-body output should end with closing --- + newline:\n%q", got)
	}
}
