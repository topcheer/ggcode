package tui

// Missed-skill detection (r398, SAGE lineage — arXiv:2512.17102).
//
// Research basis: RL for Self-Improving Agent with Skill Library (SAGE)
// observes that skill libraries' universal weakness is not "missing
// skills" but inconsistent INVOCATION: "current skill library approaches
// rely primarily on LLM prompting, making consistent skill library
// implementation challenging" — the agent fails to route an existing
// skill at the moment it applies.
//
// Gap (sa-145, file:line verified): ggcode routes skills agent-side
// (internal/tool/skill.go:68 "Use this when a listed skill clearly
// matches"), and every existing post-run mechanism only CREATES new
// assets — suggestSkillFromRun suggests NEW skills (r319),
// distillSnippetsFromRun persists commands, usageScore ranks what was
// USED. Nothing looks back and asks "an installed skill matched this
// task's topic and was never invoked." The knight stale-pruner
// (governance.go skillLooksStale) even risks deleting good skills
// BECAUSE routing never hit them.
//
// V1 heuristic (deliberately conservative, zero LLM cost):
//   1. The run never touched the skill tool (ToolCalls["skill"]==0).
//   2. The user's prompt names a skill's topic explicitly: the
//      normalized skill name (>=5 chars, separators stripped) appears
//      as a substring of the normalized prompt. Free-text fuzzy
//      matching (skill_fuzzy.go suggestSkills) is deliberately NOT
//      reused — it is edit-distance name correction, and against a
//      200-char prompt it would fire on everything.
//   3. Match => record one line into the "missed-skills" project
//      memory key (same auto-injection channel as skill-suggestions),
//      deduped per skill name, capped. The NEXT session's agent sees
//      it and can invoke or explicitly dismiss the skill.
//
// Only user-authored skill dirs are scanned (project .ggcode/skills/
// and global ~/.ggcode/skills/): the ~90 bundled agent/skills names
// are mostly generic words ("debug", "verify") that would false-positive
// on any prompt.

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/memory"
)

const (
	// missedSkillMemoryKey is the project memory key where missed-skill
	// notices accumulate; it rides the auto-injection channel.
	missedSkillMemoryKey = "missed-skills"
	// missedSkillCap bounds the accumulated list.
	missedSkillCap = 10
	// missedSkillMinNameLen rejects short/generic normalized names ("debug",
	// "verify", "spec") that appear in almost any prompt.
	missedSkillMinNameLen = 6
)

// normMissedSkill lowercases and strips separators for name-vs-prompt
// comparison (local twin of internal/tool normalizeSkillName, unexported
// there; kept local to avoid an import cycle and to allow the length
// floor policy to evolve independently).
func normMissedSkill(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch r {
		case '-', '_', ' ', '/', '.':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// userSkillNames lists installed user-authored skill names (project dir
// first, then global). Missing dirs are fine.
func userSkillNames(workingDir string) []string {
	var names []string
	dirs := []string{
		filepath.Join(workingDir, ".ggcode", "skills"),
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, ".ggcode", "skills"))
	}
	seen := map[string]bool{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if seen[name] {
				continue
			}
			// A skill dir must hold a SKILL.md to be invocable.
			if _, err := os.Stat(filepath.Join(dir, name, "SKILL.md")); err != nil {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// detectMissedSkills records installed skills whose topic the user named
// in the prompt while the run never invoked the skill tool.
func detectMissedSkills(workingDir string, stats agent.RunStats) {
	if workingDir == "" || stats.UserPrompt == "" {
		return
	}
	if stats.ToolCalls["skill"] > 0 {
		return // the agent DID route skills this run
	}
	prompt := normMissedSkill(stats.UserPrompt)
	if prompt == "" {
		return
	}
	var hits []string
	for _, name := range userSkillNames(workingDir) {
		norm := normMissedSkill(name)
		if len(norm) < missedSkillMinNameLen {
			continue
		}
		if strings.Contains(prompt, norm) {
			hits = append(hits, name)
			if len(hits) >= 2 { // at most 2 notices per run
				break
			}
		}
	}
	if len(hits) == 0 {
		return
	}
	autoMem := memory.NewProjectAutoMemory(workingDir)
	if autoMem == nil {
		return
	}
	// #1388 discipline: single-key load, merge, save — never blind overwrite.
	existing, err := autoMem.LoadKey(missedSkillMemoryKey)
	if err != nil {
		debug.Log("tui", "missed-skill: failed to load existing, skipping save: %v", err)
		return
	}
	merged := mergeMissedSkills(existing, hits)
	if merged == strings.TrimSpace(existing) {
		return // nothing new (all already recorded)
	}
	if err := autoMem.SaveMemoryWithSource(missedSkillMemoryKey, merged, "missed-skill"); err != nil {
		debug.Log("tui", "missed-skill: save failed: %v", err)
		return
	}
	debug.Log("tui", "missed-skill: recorded %d missed-skill notice(s): %v", len(hits), hits)
}

// mergeMissedSkills appends hits not already present, deduping by
// normalized skill name and capping the list at missedSkillCap.
func mergeMissedSkills(existing string, hits []string) string {
	present := map[string]bool{}
	var kept []string
	if s := strings.TrimSpace(existing); s != "" {
		for _, l := range strings.Split(s, "\n") {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			kept = append(kept, l)
			present[normMissedSkill(missedSkillNameFromLine(l))] = true
		}
	}
	for _, h := range hits {
		if present[normMissedSkill(h)] {
			continue
		}
		kept = append(kept, "skill "+h+" matched a recent task's prompt but was not invoked - consider the skill tool or /skills list")
		present[normMissedSkill(h)] = true
	}
	if len(kept) > missedSkillCap {
		kept = kept[len(kept)-missedSkillCap:]
	}
	return strings.Join(kept, "\n")
}

// missedSkillNameFromLine extracts the skill name from a recorded notice
// line ("skill <name> matched ...").
func missedSkillNameFromLine(line string) string {
	s := strings.TrimPrefix(line, "skill ")
	if i := strings.Index(s, " matched"); i > 0 {
		return s[:i]
	}
	return s
}
