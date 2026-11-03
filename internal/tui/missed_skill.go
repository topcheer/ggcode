package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/memory"
)

const (
	missedSkillMemoryKey = "missed-skills"

	missedSkillCap = 10

	missedSkillMinNameLen = 6

	skillLibrarySizeWarn = 40

	skillRoutingHealthMinNotices = 6
)

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

			if _, err := os.Stat(filepath.Join(dir, name, "SKILL.md")); err != nil {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

func detectMissedSkills(workingDir string, stats agent.RunStats) {
	if workingDir == "" || stats.UserPrompt == "" {
		return
	}
	if stats.ToolCalls["skill"] > 0 {
		return
	}
	prompt := normMissedSkill(stats.UserPrompt)
	if prompt == "" {
		return
	}
	var hits []string
	names := userSkillNames(workingDir)
	for _, name := range names {
		norm := normMissedSkill(name)
		if len(norm) < missedSkillMinNameLen {
			continue
		}
		if strings.Contains(prompt, norm) {
			hits = append(hits, name)
			if len(hits) >= 2 {
				break
			}
		}
	}
	if len(hits) == 0 {

		maybeStripStaleRoutingHealth(workingDir, len(names))
		return
	}
	autoMem := memory.NewProjectAutoMemory(workingDir)
	if autoMem == nil {
		return
	}

	existing, err := autoMem.LoadKey(missedSkillMemoryKey)
	if err != nil {
		debug.Log("tui", "missed-skill: failed to load existing, skipping save: %v", err)
		return
	}
	merged := mergeMissedSkills(existing, hits)
	if notice := skillRoutingHealthNotice(len(names), strings.Count(merged, "\n")+1); notice != "" && len(merged) > 0 {
		merged = strings.TrimSpace(merged) + "\n" + notice
	}
	if merged == strings.TrimSpace(existing) {
		return
	}
	if err := autoMem.SaveMemoryWithSource(missedSkillMemoryKey, merged, "missed-skill"); err != nil {
		debug.Log("tui", "missed-skill: save failed: %v", err)
		return
	}
	debug.Log("tui", "missed-skill: recorded %d missed-skill notice(s): %v", len(hits), hits)
}

func maybeStripStaleRoutingHealth(workingDir string, librarySize int) {
	if librarySize >= skillLibrarySizeWarn {
		return
	}
	autoMem := memory.NewProjectAutoMemory(workingDir)
	if autoMem == nil {
		return
	}
	existing, err := autoMem.LoadKey(missedSkillMemoryKey)
	if err != nil {
		return
	}
	var kept []string
	changed := false
	for _, l := range strings.Split(existing, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "# routing-health:") {
			changed = true
			continue
		}
		kept = append(kept, l)
	}
	if !changed {
		return
	}
	merged := strings.TrimSpace(strings.Join(kept, "\n"))
	if err := autoMem.SaveMemoryWithSource(missedSkillMemoryKey, merged, "missed-skill"); err != nil {
		debug.Log("tui", "missed-skill: stale routing-health strip failed: %v", err)
		return
	}
	debug.Log("tui", "missed-skill: stripped stale routing-health sentinel (library now %d)", librarySize)
}

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

func missedSkillNameFromLine(line string) string {
	s := strings.TrimPrefix(line, "skill ")
	if i := strings.Index(s, " matched"); i > 0 {
		return s[:i]
	}
	return s
}

func skillRoutingHealthNotice(librarySize, recordedNotices int) string {
	if librarySize < skillLibrarySizeWarn || recordedNotices < skillRoutingHealthMinNotices {
		return ""
	}
	return "# routing-health: " + fmt.Sprintf("%d user skills, %d missed-skill notices - routing degrades with library size; consider consolidating or disabling low-signal skills (see /skills list)", librarySize, recordedNotices)
}
