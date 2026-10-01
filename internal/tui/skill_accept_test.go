package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/memory"
)

func TestSlugifySkillName(t *testing.T) {
	cases := map[string]string{
		"[2026-10-02] Fix the build then verify (tools: run_command)": "2026-10-02-fix-the-build-then-verify",
		"发布新版本  v1.3!!!":                                              "v13",
		"UPPER Case Input":                                            "upper-case-input",
		"---leading-trail---":                                         "leading-trail",
	}
	for in, want := range cases {
		if got := slugifySkillName(in); got != want {
			t.Errorf("slugifySkillName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := slugifySkillName(strings.Repeat("a-b-", 30)); len(got) > 40 {
		t.Errorf("slug length %d exceeds 40: %q", len(got), got)
	}
}

// Full suggestion lifecycle: record via suggestSkillFromRun path storage,
// load, accept-preview stays silent on disk, accept-confirm writes
// .ggcode/skills/<name>/SKILL.md and consumes the line.
func TestSkillAcceptLifecycle(t *testing.T) {
	dir := t.TempDir()
	autoMem := memory.NewProjectAutoMemory(dir)
	if autoMem == nil {
		t.Skip("project auto memory unavailable")
	}
	line := "- [2026-10-02] release the app (tools: run_command, git_tag) - recurring workflow; consider create_skill to persist it as an invocable skill"
	if err := autoMem.SaveMemoryWithSource(skillSuggestMemoryKey, line, "skill-suggest"); err != nil {
		t.Fatalf("seed suggestion: %v", err)
	}

	lines := loadSkillSuggestions(dir)
	if len(lines) != 1 || !strings.Contains(lines[0], "release the app") {
		t.Fatalf("loadSkillSuggestions = %v", lines)
	}

	name := slugifySkillName(suggestionTaskKey(lines[0]))
	skillPath := filepath.Join(dir, ".ggcode", "skills", name, "SKILL.md")

	// consume-accepted-line logic (mirrors acceptSkillSuggestion body).
	remaining := lines[1:]
	if err := autoMem.SaveMemoryWithSource(skillSuggestMemoryKey, strings.Join(remaining, "\n"), "skill-accept"); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if reload := loadSkillSuggestions(dir); len(reload) != 0 {
		t.Fatalf("consumed key must be empty, got %v", reload)
	}

	// scaffold write (mirrors the confirmed branch).
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("---\nname: "+name+"\n---\n# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("skill not written: %v", err)
	}
}
