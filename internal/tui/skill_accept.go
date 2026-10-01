package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/memory"

	tea "charm.land/bubbletea/v2"
)

// /skill — human-in-the-loop accept surface for recorded skill suggestions
// (r362; r353 residual 2. HITL approval pattern, 2026 production standard:
// agent output routed through an explicit review surface before persistence).
//
// Suggestion lifecycle: skill_suggest.go RECORDS recurring workflows into the
// "skill-suggestions" memory key; until now consuming them meant waiting for
// a future session's agent to notice the auto-injected note and hand-write a
// create_skill call. /skill makes the human the reviewer:
//
//	/skill suggest              → indexed list of recorded suggestions
//	/skill accept <idx>         → PREVIEW the SKILL.md that would be written
//	/skill accept <idx> confirm → write .ggcode/skills/<name>/SKILL.md
//
// Two-step confirm keeps the write behind an explicit human decision; the
// generated body is a scaffold (task + observed tools), the distillation
// itself stays the user's/agent's judgment (r319 conservative-gate decision).

func (m *Model) handleSkillCommand(parts []string) tea.Cmd {
	switch {
	case len(parts) == 1 || parts[1] == "suggest" || parts[1] == "suggestions":
		m.listSkillSuggestions()
	case parts[1] == "accept" && len(parts) >= 3:
		if len(parts) >= 4 && parts[3] == "draft" {
			return m.draftSkillSuggestion(parts[2], parts[4:])
		}
		m.acceptSkillSuggestion(parts[2], len(parts) >= 4 && parts[3] == "confirm", parts[4:])
	default:
		m.chatWriteSystem(nextSystemID(), "Usage: /skill suggest | /skill accept <idx> [confirm|draft] [name]")
	}
	return nil
}

// loadSkillSuggestions returns the non-empty suggestion lines from the
// project memory key, in recorded order.
func loadSkillSuggestions(workingDir string) []string {
	autoMem := memory.NewProjectAutoMemory(workingDir)
	if autoMem == nil {
		return nil
	}
	existing, err := autoMem.LoadKey(skillSuggestMemoryKey)
	if err != nil {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(existing, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	return lines
}

func (m *Model) listSkillSuggestions() {
	workDir, _ := os.Getwd()
	lines := loadSkillSuggestions(workDir)
	if len(lines) == 0 {
		m.chatWriteSystem(nextSystemID(), "No skill suggestions recorded. Suggestions appear after a workflow succeeds twice (skill_suggest gate).")
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Recorded skill suggestions (%d) — /skill accept <idx> to preview:\n", len(lines))
	for i, l := range lines {
		fmt.Fprintf(&b, "  [%d] %s\n", i+1, l)
	}
	m.chatWriteSystem(nextSystemID(), b.String())
}

func (m *Model) acceptSkillSuggestion(idxArg string, confirmed bool, nameArgs []string) {
	idx, err := strconv.Atoi(idxArg)
	if err != nil || idx < 1 {
		m.chatWriteSystem(nextSystemID(), "Invalid index: "+idxArg)
		return
	}
	workDir, _ := os.Getwd()
	lines := loadSkillSuggestions(workDir)
	if idx > len(lines) {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Index out of range: %d (have %d suggestions)", idx, len(lines)))
		return
	}
	line := lines[idx-1]
	name := skillNameFromArgs(nameArgs)
	if name == "" {
		name = slugifySkillName(suggestionTaskKey(line))
	}
	if name == "" {
		m.chatWriteSystem(nextSystemID(), "Could not derive a skill name; pass one: /skill accept <idx> confirm <name>")
		return
	}

	// Task description: strip date prefix and trailing hint tail.
	task := strings.TrimSpace(line)
	if i := strings.Index(task, "] "); i >= 0 {
		task = task[i+2:]
	}
	task = truncateSuggestion(task, 80)

	skillDir := filepath.Join(workDir, ".ggcode", "skills", name)
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if _, statErr := os.Stat(skillPath); statErr == nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Skill %q already exists at %s — remove it first if you want to overwrite.", name, skillPath))
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "---\nname: %s\ndescription: %s\nscope: project\ncreated_by: skill-accept\n---\n", name, task)
	fmt.Fprintf(&b, "# %s\n\n", name)
	fmt.Fprintf(&b, "## When to Use\n%s\n\n", task)
	b.WriteString("## Steps\n(Scaffold from a recurring workflow — refine with the exact steps you follow.\n")
	if i := strings.Index(line, "(tools: "); i >= 0 {
		tools := line[i+len("(tools: "):]
		if j := strings.Index(tools, ")"); j >= 0 {
			tools = tools[:j]
		}
		fmt.Fprintf(&b, "Observed tools: %s.)\n\n", tools)
	} else {
		b.WriteString(")\n\n")
	}
	b.WriteString("## When Not to Use\nAdjust for tasks that only superficially resemble the above.\n")

	if !confirmed {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf(
			"Preview of %s:\n\n%s\n\nTo write it: /skill accept %d confirm%s",
			skillPath, b.String(), idx, skillNameSuffix(nameArgs)))
		return
	}
	if mkErr := os.MkdirAll(skillDir, 0o755); mkErr != nil {
		debug.Log("tui", "skill-accept: mkdir failed: %v", mkErr)
		m.chatWriteSystem(nextSystemID(), "Failed to create skill dir: "+mkErr.Error())
		return
	}
	if wErr := os.WriteFile(skillPath, []byte(b.String()), 0o644); wErr != nil {
		debug.Log("tui", "skill-accept: write failed: %v", wErr)
		m.chatWriteSystem(nextSystemID(), "Failed to write skill: "+wErr.Error())
		return
	}

	// Consume the accepted line so the list stays actionable.
	remaining := make([]string, 0, len(lines)-1)
	for i, l := range lines {
		if i != idx-1 {
			remaining = append(remaining, l)
		}
	}
	if autoMem := memory.NewProjectAutoMemory(workDir); autoMem != nil {
		if sErr := autoMem.SaveMemoryWithSource(skillSuggestMemoryKey, strings.Join(remaining, "\n"), "skill-accept"); sErr != nil {
			debug.Log("tui", "skill-accept: consume-line save failed (skill written anyway): %v", sErr)
		}
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Skill %q written to %s. It loads on next session (or /skills panel).", name, skillPath))
}

// draftSkillSuggestion (r363 / r353 residual 3) hands distillation to the
// CURRENT session's agent: it injects a /init-style prompt carrying the
// recorded evidence line, the agent drafts the SKILL.md (it has full session
// context), writes it, and consumes the suggestion. The user watches the
// draft stream in the TUI - a stronger HITL surface than the static confirm
// scaffold, which remains as the offline fallback.
func (m *Model) draftSkillSuggestion(idxArg string, nameArgs []string) tea.Cmd {
	idx, err := strconv.Atoi(idxArg)
	if err != nil || idx < 1 {
		m.chatWriteSystem(nextSystemID(), "Invalid index: "+idxArg)
		return nil
	}
	workDir, _ := os.Getwd()
	lines := loadSkillSuggestions(workDir)
	if idx > len(lines) {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Index out of range: %d (have %d suggestions)", idx, len(lines)))
		return nil
	}
	name := skillNameFromArgs(nameArgs)
	if name == "" {
		name = slugifySkillName(suggestionTaskKey(lines[idx-1]))
	}
	if name == "" {
		m.chatWriteSystem(nextSystemID(), "Could not derive a skill name; pass one: /skill accept <idx> draft <name>")
		return nil
	}
	return m.submitHiddenText(skillDraftPrompt(lines[idx-1], idx, name))
}

// skillDraftPrompt is a pure function so tests can pin the contract: the
// agent must see the evidence, the pitfall instruction for recovered runs,
// the exact skill path, and the consume step.
func skillDraftPrompt(line string, idx int, name string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Distill the following recurring workflow into a reusable skill draft.\n\n")
	fmt.Fprintf(&b, "Evidence (from the skill-suggestions memory, entry #%d):\n%s\n\n", idx, line)
	if strings.Contains(line, "recovered from") {
		fmt.Fprintf(&b, "This workflow recovered from errors: include a pitfall-avoidance step in Steps derived from the recovery.\n\n")
	}
	fmt.Fprintf(&b, "Steps:\n"+
		"1. If the evidence line is thin, ask me for the fuller session context before drafting.\n"+
		"2. Draft a SKILL.md with YAML frontmatter (name: %s, description:, scope: project) and sections: When to Use, Steps (numbered, concrete), When Not to Use.\n"+
		"3. Write it to .ggcode/skills/%s/SKILL.md (create_skill tool or write_file).\n"+
		"4. Consume the suggestion: rewrite the 'skill-suggestions' project memory (save_memory, project scope) without entry #%d.\n", name, name, idx)
	fmt.Fprintf(&b, "Keep it concise and grounded in the evidence - do not invent steps that are not supported by it.")
	return b.String()
}

// skillNameFromArgs returns an explicit name passed after `confirm`.
func skillNameFromArgs(nameArgs []string) string {
	if len(nameArgs) == 0 {
		return ""
	}
	return slugifySkillName(strings.Join(nameArgs, "-"))
}

func skillNameSuffix(nameArgs []string) string {
	if n := skillNameFromArgs(nameArgs); n != "" {
		return " " + n
	}
	return ""
}

var skillSlugRe = regexp.MustCompile(`[^a-z0-9-]+`)

// slugifySkillName derives a filesystem-safe skill name from free text.
func slugifySkillName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	s = skillSlugRe.ReplaceAllString(s, "")
	s = strings.Trim(s, "-")
	for len(s) > 40 {
		if i := strings.LastIndex(s, "-"); i > 8 {
			s = s[:i]
		} else {
			s = s[:40]
		}
		s = strings.Trim(s, "-")
	}
	return s
}
