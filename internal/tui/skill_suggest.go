package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/memory"
)

const skillSuggestMemoryKey = "skill-suggestions"

const skillSuggestCap = 20

func skillWorthyRun(stats agent.RunStats) bool {
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
	artifacts := len(stats.FilesEdited) > 0 || len(stats.SuccessfulCommands) >= 2
	if stats.ErrorCount > 0 && !artifacts {
		return false
	}
	return artifacts
}

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
		fmt.Fprintf(&b, " (tools: %s%s)", tools, suggestionEvidenceTail(stats))
	} else {
		if tail := suggestionEvidenceTail(stats); tail != "" {
			fmt.Fprintf(&b, " (tools: -%s)", tail)
		}
	}
	if stats.ErrorCount > 0 {

		fmt.Fprintf(&b, " — recurring workflow recovered from %d error(s); consider create_skill and include the pitfall avoidance", stats.ErrorCount)
		return b.String()
	}
	b.WriteString(" — recurring workflow; consider create_skill to persist it as an invocable skill")
	return b.String()
}

func suggestionEvidenceTail(stats agent.RunStats) string {
	var parts []string
	if n := len(stats.FilesEdited); n > 0 {
		files := make([]string, 0, 3)
		for _, f := range stats.FilesEdited {
			if i := strings.LastIndex(f, "/"); i >= 0 {
				f = f[i+1:]
			}
			files = append(files, f)
			if len(files) == 3 {
				break
			}
		}
		parts = append(parts, "files: "+strings.Join(files, ","))
	}
	if n := len(stats.SuccessfulCommands); n > 0 {
		cmds := make([]string, 0, 2)
		for _, c := range stats.SuccessfulCommands {
			cmds = append(cmds, truncateSuggestion(strings.TrimSpace(c), 60))
			if len(cmds) == 2 {
				break
			}
		}
		parts = append(parts, "cmds: "+strings.Join(cmds, " | "))
	}
	return strings.Join(parts, "; ")
}

func truncateSuggestion(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

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
				continue
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

func suggestionTaskKey(line string) string {
	s := line
	if i := strings.Index(s, " (tools:"); i >= 0 {
		s = s[:i]
	} else if i := strings.Index(s, " — recurring"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)

	if strings.HasPrefix(s, "- [") {
		if j := strings.Index(s, "] "); j >= 0 {
			s = strings.TrimSpace(s[j+2:])
		}
	}
	return s
}
