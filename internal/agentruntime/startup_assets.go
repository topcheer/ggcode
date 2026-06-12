package agentruntime

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/knight"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tool"
)

type StartupAssets struct {
	AutoContent        string
	AutoFiles          []string
	ProjectAutoContent string
	CommandManager     *commands.Manager
}

func LoadInteractiveStartupAssets(
	workingDir string,
	autoMem *memory.AutoMemory,
	projectAutoMem *memory.AutoMemory,
) StartupAssets {
	var (
		autoContent        string
		autoFiles          []string
		projectAutoContent string
		commandMgr         *commands.Manager
	)

	var wg sync.WaitGroup
	wg.Add(3)

	safego.Go("agentruntime.startup.autoMem", func() {
		defer wg.Done()
		start := time.Now()
		autoContent, autoFiles, _ = autoMem.LoadAll()
		debug.Log("agentruntime", "startup assets autoMem files=%d duration=%s", len(autoFiles), time.Since(start).Round(time.Millisecond))
	})

	safego.Go("agentruntime.startup.projectAutoMem", func() {
		defer wg.Done()
		start := time.Now()
		if projectAutoMem != nil {
			projectAutoContent, _, _ = projectAutoMem.LoadAll()
		}
		debug.Log("agentruntime", "startup assets projectAutoMem enabled=%v bytes=%d duration=%s", projectAutoMem != nil, len(projectAutoContent), time.Since(start).Round(time.Millisecond))
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
		AutoContent:        autoContent,
		AutoFiles:          autoFiles,
		ProjectAutoContent: projectAutoContent,
		CommandManager:     commandMgr,
	}
}

func BuildSkillsSystemPrompt(skills []*commands.Command) string {
	prompt, _ := BuildSkillsSystemPromptWithPromptRefs(skills)
	return prompt
}

func BuildSkillsSystemPromptWithPromptRefs(skills []*commands.Command) (string, []string) {
	var lines []string
	lines = append(lines,
		"Use the skill tool to load reusable workflows when they clearly match the user's task.",
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
		desc := strings.TrimSpace(skill.Description)
		if when := strings.TrimSpace(skill.WhenToUse); when != "" {
			if desc != "" {
				desc += " - "
			}
			desc += when
		}
		if len(desc) > maxDescChars {
			desc = desc[:maxDescChars-1] + "..."
		}
		line := fmt.Sprintf("- %s: %s", name, desc)
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
		lines = append(lines, fmt.Sprintf("- ... and %d more skills available via the skill tool and /skills", hidden))
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
