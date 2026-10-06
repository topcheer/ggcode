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
func TestSkillDraftPrompt(t *testing.T) {
	recovered := "- [2026-10-02] release app (tools: run_command, git_tag; files: Makefile; cmds: make verify-ci) - recurring workflow recovered from 2 error(s); consider create_skill"
	p := skillDraftPrompt(recovered, 3, "release-app")
	for _, want := range []string{
		"Evidence", recovered, // full evidence line reaches the agent
		"pitfall-avoidance", // recovered run must instruct pitfall capture
		"name: release-app", ".ggcode/skills/release-app/SKILL.md",
		"When to Use", "Steps", "When Not to Use",
		"entry #3", // consume step carries the index
	} {
		if !strings.Contains(p, want) {
			t.Errorf("draft prompt missing %q", want)
		}
	}
	clean := "- [2026-10-02] tidy imports (tools: edit_file) - recurring workflow"
	if p2 := skillDraftPrompt(clean, 1, "tidy-imports"); strings.Contains(p2, "pitfall") {
		t.Error("non-recovered line must not request pitfall avoidance")
	}
}

func TestSuggestionEvidenceTail(t *testing.T) {
	stats := skillWorthyStats()
	stats.FilesEdited = []string{"/a/b/main.go", "/a/b/util.go", "/c/d.go", "/e/f.go"}
	stats.SuccessfulCommands = []string{"go build ./...", "go test ./util/ -count=1"}
	tail := suggestionEvidenceTail(stats)
	if !strings.Contains(tail, "files: main.go,util.go,d.go") {
		t.Errorf("files tail wrong: %q", tail)
	}
	if !strings.Contains(tail, "cmds: go build ./...") {
		t.Errorf("cmds tail wrong: %q", tail)
	}
	// task-key identity: the tail lives inside the " (tools: ...)" parenthesis,
	// so suggestionTaskKey truncation is unaffected.
	line := "- [2026-10-02] tidy (tools: edit_file; " + tail + ") - recurring workflow"
	if k := suggestionTaskKey(line); k != "- [2026-10-02] tidy" {
		t.Errorf("task key drifted: %q", k)
	}
}

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
