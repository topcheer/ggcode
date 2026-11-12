package agentruntime

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/knight"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tool"
)

type StartupAssets struct {
	AutoFiles             []string
	CommandManager        *commands.Manager
	ConsolidationFindings []string
}

// startupConsolidationSkipWindow: a boot re-scan within this window of the
// previous run is skipped entirely (#5519 - fleet boots stop re-scanning
// the same store 12x). Package var for testability.
var startupConsolidationSkipWindow = 6 * time.Hour

// shouldSkipStartupConsolidation decides whether the boot scan runs:
// skip when the last run is fresh within the window (zero time = never ran
// = scan).
func shouldSkipStartupConsolidation(last, now time.Time) (skip bool, since time.Duration) {
	if last.IsZero() {
		return false, 0
	}
	since = now.Sub(last)
	if since < 0 {
		since = 0
	}
	return since < startupConsolidationSkipWindow, since
}

func LoadInteractiveStartupAssets(
	workingDir string,
	autoMem *memory.AutoMemory,
) StartupAssets {
	var (
		autoFiles             []string
		commandMgr            *commands.Manager
		consolidationFindings []string
	)

	var wg sync.WaitGroup
	wg.Add(2)

	safego.Go("agentruntime.startup.autoMem", func() {
		defer wg.Done()
		start := time.Now()
		autoMem.GarbageCollect()

		if skip, since := shouldSkipStartupConsolidation(autoMem.LastConsolidationRun(), time.Now()); skip {
			debug.Log("memory", "startup consolidation skipped: last run %.1fh ago", since.Hours())
		} else if report := autoMem.Consolidate(workingDir); report.HasFindings() {
			debug.Log("memory", "startup consolidation: %s", report.String())
			for _, w := range report.Warnings {
				debug.Log("memory", "startup consolidation: %s", w)
				consolidationFindings = append(consolidationFindings, w)
			}
		}
		_, autoFiles, _ = autoMem.LoadIndex()
		debug.Log("agentruntime", "startup assets autoMem files=%d duration=%s", len(autoFiles), time.Since(start).Round(time.Millisecond))
	})

	safego.Go("agentruntime.startup.commands", func() {
		defer wg.Done()
		start := time.Now()
		commandMgr = commands.NewManager(workingDir)
		cmdCount := 0
		if commandMgr != nil {
			cmdCount = len(commandMgr.Commands())
		}
		debug.Log("agentruntime", "startup assets commands commands=%d duration=%s", cmdCount, time.Since(start).Round(time.Millisecond))
	})

	wg.Wait()
	if commandMgr == nil {
		commandMgr = commands.NewManager(workingDir)
	}

	return StartupAssets{
		AutoFiles:             autoFiles,
		CommandManager:        commandMgr,
		ConsolidationFindings: consolidationFindings,
	}
}

// skillPromptSeedLine renders one P4 discovery line (#5446): name + the
// first clause of when_to_use (preferred) or description. First clause is
// enough for the model to decide to run skill '?keyword' for the rest.
func skillPromptSeedLine(skill *commands.Command) string {
	name := strings.TrimSpace(skill.Name)
	seed := strings.TrimSpace(skill.WhenToUse)
	if seed == "" {
		seed = strings.TrimSpace(skill.Description)
	}
	seed = firstClause(seed, 90)
	if seed == "" {
		return "- " + name
	}
	return fmt.Sprintf("- %s: %s", name, seed)
}

// firstClause cuts the text at the first sentence/clause boundary within
// max runes (never splitting mid-rune).
func firstClause(s string, max int) string {
	cutset := ".;!;"
	idx := -1
	for _, c := range cutset {
		if j := strings.IndexByte(s, byte(c)); j > 8 && (idx == -1 || j < idx) {
			idx = j
		}
	}
	if idx > 0 {
		s = s[:idx]
	}
	if utf8.RuneCountInString(s) > max {
		s = truncateRunes(s, max, "...")
	}
	return strings.TrimSpace(s)
}

func BuildSkillsSystemPrompt(skills []*commands.Command) string {
	prompt, _ := BuildSkillsSystemPromptWithPromptRefs(skills)
	return prompt
}

func BuildSkillsSystemPromptWithPromptRefs(skills []*commands.Command) (string, []string) {
	var lines []string
	lines = append(lines,
		"Use the skill tool to load reusable workflows when they clearly match the user's task.",
		"To discover skills by keyword, use skill with a '?' prefix (e.g. \"?deploy\").",
		"To create a new reusable skill from a workflow you've established, use create_skill.",
		"",
		"When a listed skill is a close match, invoke the skill tool before continuing.",
		"Do not mention a skill without calling the skill tool.",
		"Do not use the skill tool for built-in CLI commands like /help or /clear.",
		"",
		"Available skills:",
	)
	const maxChars = 4000
	const maxDescChars = 180
	total := 0
	included := 0
	mcpSkillCount := 0
	mcpServers := make(map[string]struct{})
	var promptSkillRefs []string
	for _, skill := range prioritizedSkillsForPrompt(skills) {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			continue
		}
		if skill.LoadedFrom == commands.LoadedFromMCP || skill.Source == commands.SourceMCP {
			mcpSkillCount++
			if server, _, ok := strings.Cut(name, ":"); ok {
				server = strings.TrimSpace(server)
				if server != "" {
					mcpServers[server] = struct{}{}
				}
			}
			continue
		}
		// P4 (#5446): one line per skill = discovery seed only. Prefer the
		// when_to_use first clause (the matching signal); fall back to the
		// description first clause. With ~130 skills the 4K cap previously
		// surfaced only ~25 names - single-lining multiplies coverage per
		// char while full discovery stays on skill '?keyword'.
		line := skillPromptSeedLine(skill)
		if total+len(line)+1 > maxChars {
			break
		}
		lines = append(lines, line)
		total += len(line) + 1
		included++
		if ref := skillPromptExposureRef(skill); ref != "" {
			promptSkillRefs = append(promptSkillRefs, ref)
		}
	}
	if mcpSkillCount > 0 {
		servers := sortedStringKeys(mcpServers)
		summary := fmt.Sprintf("- MCP prompt-backed skills are also available from connected MCP servers (%d total", mcpSkillCount)
		if len(servers) > 0 {
			summary += "; servers: " + strings.Join(servers, ", ")
		}
		summary += ")."
		if total+len(summary)+1 <= maxChars {
			lines = append(lines, summary)
			total += len(summary) + 1
		}
	}
	if hidden := countModelVisibleSkills(skills) - included - mcpSkillCount; hidden > 0 {
		lines = append(lines, fmt.Sprintf("- ... and %d more skills. Use skill: \"?\" to browse all, or skill: \"?keyword\" to search by topic.", hidden))
	}
	return strings.Join(lines, "\n"), promptSkillRefs
}

func BuildMCPSkillCommands(snapshots []tool.MCPServerSnapshot) []*commands.Command {
	out := make([]*commands.Command, 0)
	for _, snap := range snapshots {
		for _, promptName := range snap.PromptNames {
			name := strings.TrimSpace(snap.Name + ":" + promptName)
			if name == ":" || strings.TrimSpace(promptName) == "" || strings.TrimSpace(snap.Name) == "" {
				continue
			}
			out = append(out, &commands.Command{
				Name:          name,
				Description:   fmt.Sprintf("MCP prompt from %s", snap.Name),
				WhenToUse:     fmt.Sprintf("Use when the %s MCP prompt %q matches the user's request.", snap.Name, promptName),
				Source:        commands.SourceMCP,
				LoadedFrom:    commands.LoadedFromMCP,
				UserInvocable: true,
			})
		}
	}
	return out
}

func skillPromptExposureRef(skill *commands.Command) string {
	if skill == nil || skill.LoadedFrom != commands.LoadedFromSkills {
		return ""
	}
	name := strings.TrimSpace(skill.Name)
	if name == "" {
		return ""
	}
	switch skill.Source {
	case commands.SourceProject:
		return knight.FormatSkillRefForDisplay("project", name)
	case commands.SourceUser:
		return knight.FormatSkillRefForDisplay("global", name)
	default:
		return ""
	}
}

func prioritizedSkillsForPrompt(skills []*commands.Command) []*commands.Command {
	out := make([]*commands.Command, 0, len(skills))
	for _, skill := range skills {
		if skill == nil || skill.DisableModelInvocation || !skill.Enabled || strings.TrimSpace(skill.Name) == "" {
			continue
		}
		out = append(out, skill)
	}
	sort.SliceStable(out, func(i, j int) bool {
		iBundled := out[i].LoadedFrom == commands.LoadedFromBundled || out[i].Source == commands.SourceBundled
		jBundled := out[j].LoadedFrom == commands.LoadedFromBundled || out[j].Source == commands.SourceBundled
		if iBundled != jBundled {
			return iBundled
		}
		iMCP := out[i].LoadedFrom == commands.LoadedFromMCP || out[i].Source == commands.SourceMCP
		jMCP := out[j].LoadedFrom == commands.LoadedFromMCP || out[j].Source == commands.SourceMCP
		if iMCP != jMCP {
			return !iMCP
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func sortedStringKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func countModelVisibleSkills(skills []*commands.Command) int {
	count := 0
	for _, skill := range skills {
		if skill != nil && !skill.DisableModelInvocation && skill.Enabled && strings.TrimSpace(skill.Name) != "" {
			count++
		}
	}
	return count
}
