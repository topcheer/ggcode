package tui

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/memory"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
)

func skillWorthyStats() agent.RunStats {
	return agent.RunStats{
		Success: true,
		ToolCalls: map[string]int{
			"read_file": 4, "edit_file": 3, "run_command": 2, "grep": 1,
		},
		FilesEdited:        []string{"a.go"},
		SuccessfulCommands: []string{"go test ./..."},
		UserPrompt:         "fix the flaky tunnel vision test",
	}
}

func TestSkillWorthyRun(t *testing.T) {
	if !skillWorthyRun(skillWorthyStats()) {
		t.Fatal("clean 10-call 4-tool run with edits should be skill-worthy")
	}

	s := skillWorthyStats()
	s.ErrorCount = 2
	if !skillWorthyRun(s) {
		t.Fatal("run that recovered from errors and still produced edits should be skill-worthy")
	}

	s = skillWorthyStats()
	s.ErrorCount = 1
	s.FilesEdited = nil
	s.SuccessfulCommands = nil
	if skillWorthyRun(s) {
		t.Fatal("run with errors and no artifacts must not be skill-worthy")
	}

	s = skillWorthyStats()
	s.ToolCalls = map[string]int{"read_file": 2, "edit_file": 2, "grep": 1}
	if skillWorthyRun(s) {
		t.Fatal("5-call run below threshold 8")
	}

	s = skillWorthyStats()
	s.ToolCalls = map[string]int{"run_command": 9}
	if skillWorthyRun(s) {
		t.Fatal("single-tool run is not a multi-tool workflow")
	}

	s = skillWorthyStats()
	s.FilesEdited = nil
	s.SuccessfulCommands = nil
	if skillWorthyRun(s) {
		t.Fatal("no edits and no verified commands is not a workflow")
	}

	s.SuccessfulCommands = []string{"go build ./...", "go test ./..."}
	if !skillWorthyRun(s) {
		t.Fatal("2+ verified commands should qualify as artifact")
	}
}

func TestMergeSkillSuggestionsDedupAndCap(t *testing.T) {
	base := skillSuggestionText(agent.RunStats{UserPrompt: "release the app", ToolCalls: map[string]int{"run_command": 5}})
	merged := mergeSkillSuggestions("", base)
	if !strings.Contains(merged, "release the app") {
		t.Fatalf("first merge lost the line: %q", merged)
	}

	fresh := skillSuggestionText(agent.RunStats{UserPrompt: "release the app", ToolCalls: map[string]int{"edit_file": 2}})
	merged = mergeSkillSuggestions(merged, fresh)
	if got := strings.Count(merged, "release the app"); got != 1 {
		t.Fatalf("same-task suggestion duplicated: %d occurrences in %q", got, merged)
	}

	other := skillSuggestionText(agent.RunStats{UserPrompt: "triage open issues"})
	merged = mergeSkillSuggestions(merged, other)
	if got := strings.Count(merged, "recurring workflow"); got != 2 {
		t.Fatalf("distinct task should append, got %d lines in %q", got, merged)
	}

	var flood string
	for i := 0; i < skillSuggestCap+5; i++ {
		flood = mergeSkillSuggestions(flood, skillSuggestionText(agent.RunStats{
			UserPrompt: strings.Repeat("x", i+1) + " unique task",
		}))
	}
	if got := strings.Count(flood, "recurring workflow"); got > skillSuggestCap {
		t.Fatalf("cap exceeded: %d lines", got)
	}
	if !strings.Contains(flood, "unique task") {
		t.Fatal("cap must keep the newest entries, not the oldest")
	}
}

func TestSkillSuggestionTextTruncates(t *testing.T) {
	long := strings.Repeat("长", 300)
	line := skillSuggestionText(agent.RunStats{UserPrompt: long})
	if got := len([]rune(line)); got > 400 {
		t.Fatalf("long prompt not truncated: %d runes", got)
	}
}

func TestSuggestSkillFromRunGatesOnRepeated(t *testing.T) {
	dir := t.TempDir()

	suggestSkillFromRun(dir, skillWorthyStats(), false)
	if got, _ := memLoadSkillKey(dir); got != "" {
		t.Fatalf("first occurrence must not record a suggestion, got %q", got)
	}

	suggestSkillFromRun(dir, skillWorthyStats(), true)
	got, _ := memLoadSkillKey(dir)
	if got == "" {
		t.Fatal("repeated occurrence should record a suggestion")
	}
	if !strings.Contains(got, "tunnel vision") {
		t.Fatalf("suggestion content unexpected: %q", got)
	}

	before, _ := memLoadSkillKey(dir)
	s := skillWorthyStats()
	s.ErrorCount = 3
	s.FilesEdited = nil
	s.SuccessfulCommands = nil
	suggestSkillFromRun(dir, s, true)
	after, _ := memLoadSkillKey(dir)
	if before != after {
		t.Fatal("unworthy run must not record")
	}
}

func memLoadSkillKey(dir string) (string, error) {
	m := memory.NewProjectAutoMemory(dir)
	if m == nil {
		return "", fmt.Errorf("no store")
	}
	return m.LoadKey(skillSuggestMemoryKey)
}

func TestSkillSuggestionTextFlagsRecovered(t *testing.T) {
	s := skillWorthyStats()
	s.ErrorCount = 2
	line := skillSuggestionText(s)
	if !strings.Contains(line, "recovered from 2 error(s)") || !strings.Contains(line, "pitfall avoidance") {
		t.Fatalf("recovered-run suggestion should flag pitfall avoidance, got: %s", line)
	}

	clean := skillSuggestionText(skillWorthyStats())
	if strings.Contains(clean, "recovered") {
		t.Fatalf("clean run should not carry recovery flag, got: %s", clean)
	}
}
