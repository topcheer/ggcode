package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/memory"
)

// Skill suggestion (r319 P1 gap, skillcam / SkillRL arXiv:2602.08234 lineage):
// a session whose workflow succeeded twice through the experience store is a
// repeatable workflow the user is redoing by hand. ggcode's skill library
// (.ggcode/skills/) is only ever written by explicit create_skill calls, so
// repeated workflows never bridge into invocable skills.
//
// Conservative gate (no auto-write, per r319 review): this detector only
// RECORDS a suggestion into the "skill-suggestions" project memory key —
// which rides the existing auto-injection channel so the agent sees it next
// session and can offer create_skill — instead of distilling and writing a
// SKILL.md itself. Quality of a distilled skill needs human judgment; the
// trigger heuristic below only decides WHEN to suggest, never WHAT to write.

// skillSuggestMemoryKey is the project memory key suggestions accumulate in.
const skillSuggestMemoryKey = "skill-suggestions"

// skillSuggestCap bounds the accumulated suggestion list.
const skillSuggestCap = 20

// skillWorthyRun reports whether this run's shape looks like a repeatable
// workflow worth a skill: clean, substantial (>=8 calls), multi-tool (>=3
// distinct), and producing artifacts (edits or verified commands). Pure
// function; unit-testable without an agent.
func skillWorthyRun(stats agent.RunStats) bool {
	if stats.ErrorCount > 0 {
		return false
	}
	total := 0
	distinct := 0
	for _, n := range stats.ToolCalls {
		total += n
		if n > 0 {
			distinct++
		}
	}
	if total < 8 || distinct < 3 {
		return false
	}
	return len(stats.FilesEdited) > 0 || len(stats.SuccessfulCommands) >= 2
}

// skillSuggestionText renders one suggestion line for a run.
func skillSuggestionText(stats agent.RunStats) string {
	task := strings.TrimSpace(stats.UserPrompt)
	if i := strings.IndexAny(task, "\n"); i >= 0 {
		task = task[:i]
	}
	task = truncateSuggestion(task, 120)
	tools := topToolCalls(stats.ToolCalls, 3)
	var b strings.Builder
	fmt.Fprintf(&b, "- [%s] %s", time.Now().UTC().Format("2006-01-02"), task)
	if tools != "" {
		fmt.Fprintf(&b, " (tools: %s)", tools)
	}
	b.WriteString(" — recurring workflow; consider create_skill to persist it as an invocable skill")
	return b.String()
}

// truncateSuggestion clamps a suggestion task line to max runes.
func truncateSuggestion(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// suggestSkillFromRun appends a suggestion when the run is skill-worthy AND
// the experience store just UPDATED an existing case (second+ success of the
// same task shape — the strongest repetition signal, already computed by
// recordExperienceCase). Deterministic, no LLM call; failures debug-log only
// and never disturb the session.
func suggestSkillFromRun(workingDir string, stats agent.RunStats, repeatedTask bool) {
	if !repeatedTask || !skillWorthyRun(stats) {
		return
	}
	if workingDir == "" {
		return
	}
	autoMem := memory.NewProjectAutoMemory(workingDir)
	if autoMem == nil {
		return
	}
	// #1388 discipline: single-key load, merge, save — never blind overwrite.
	existing, err := autoMem.LoadKey(skillSuggestMemoryKey)
	if err != nil {
		debug.Log("tui", "skill-suggest: failed to load existing, skipping save: %v", err)
		return
	}
	line := skillSuggestionText(stats)
	if strings.Contains(existing, line) {
		return
	}
	merged := mergeSkillSuggestions(existing, line)
	if err := autoMem.SaveMemoryWithSource(skillSuggestMemoryKey, merged, "skill-suggest"); err != nil {
		debug.Log("tui", "skill-suggest: save failed: %v", err)
		return
	}
	debug.Log("tui", "skill-suggest: recorded recurring-workflow suggestion")
}

// mergeSkillSuggestions appends line to existing, deduplicating by the task
// prefix (same workflow re-suggested on its third success updates in place)
// and capping the list at skillSuggestCap entries.
func mergeSkillSuggestions(existing, line string) string {
	taskKey := suggestionTaskKey(line)
	var kept []string
	if s := strings.TrimSpace(existing); s != "" {
		for _, l := range strings.Split(s, "\n") {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			if suggestionTaskKey(l) == taskKey {
				continue // replaced by the fresh line below
			}
			kept = append(kept, l)
		}
	}
	kept = append(kept, line)
	if len(kept) > skillSuggestCap {
		kept = kept[len(kept)-skillSuggestCap:]
	}
	return strings.Join(kept, "\n")
}

// suggestionTaskKey extracts the "[date] task" identity from a suggestion
// line, ignoring the trailing tool/hint tail so a changed tool mix still
// dedupes against the same workflow.
func suggestionTaskKey(line string) string {
	if i := strings.Index(line, " (tools:"); i >= 0 {
		return strings.TrimSpace(line[:i])
	}
	if i := strings.Index(line, " — recurring"); i >= 0 {
		return strings.TrimSpace(line[:i])
	}
	return strings.TrimSpace(line)
}
