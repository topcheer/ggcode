package knight

import (
	"strings"
	"testing"
)

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

	if !strings.Contains(got, "\n---\n") {
		t.Fatalf("closing --- must own its line (expected \\n---\\n in output):\n%q", got)
	}
	if !strings.Contains(got, "# Skill Usage") || !strings.Contains(got, "Body here.") {
		t.Fatalf("body content lost:\n%q", got)
	}
}

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
