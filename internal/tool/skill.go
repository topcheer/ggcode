package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/subagent"
)

type SkillLookup interface {
	Get(name string) (*commands.Command, bool)
}

// SkillNameLister provides all available skill names for fuzzy matching.
// Implemented by commands.Manager.
type SkillNameLister interface {
	SkillNames() []string
}

type skillUsageRecorder interface {
	RecordUsage(name string)
}

type SkillExecutionMode string

const (
	SkillExecutionModeInline SkillExecutionMode = "inline"
	SkillExecutionModeFork   SkillExecutionMode = "fork"
	SkillExecutionModeMCP    SkillExecutionMode = "mcp"
)

type SkillExecutionEvent struct {
	Name   string
	Ref    string
	Scope  string
	Mode   SkillExecutionMode
	Result Result
	Err    error
}

type SkillTool struct {
	Skills              SkillLookup
	NameLister          SkillNameLister
	Runtime             MCPRuntime
	Provider            provider.Provider
	Tools               *Registry
	AgentFactory        subagent.AgentFactory
	WorkingDir          string // working directory to propagate to sub-agent
	OnUsage             func(provider.TokenUsage)
	OnSkillUsed         func(ref string)                    // optional callback when a skill is loaded by the agent
	OnSkillCompleted    func(event SkillExecutionEvent)     // optional callback when execution finishes
	SystemPromptBuilder func(task, agentType string) string // builds rich system prompt with project context
}

func (t SkillTool) Name() string { return "skill" }

func (t SkillTool) Description() string {
	return "Load a reusable skill workflow or prompt, or search available skills by keyword. Use this when a listed skill clearly matches the user's task, then continue using the returned guidance. To discover skills by keyword, prefix the query with a question mark (e.g. skill: \"?deploy\"). Do not use for built-in CLI commands like /help or /clear."
}

func (t SkillTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"skill": {
			"type": "string",
			"description": "Skill name to load, or prefix with '?' to search skills by keyword (e.g. '?deploy' finds deployment-related skills). Must match a listed reusable skill; do not pass built-in CLI/slash commands."
		},
		"args": {
			"type": "string",
			"description": "Optional user arguments passed to the skill"
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"skill",
		"description"
	]
}`)
}

func (t SkillTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Skill string `json:"skill"`
		Args  string `json:"args"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if t.Skills == nil {
		return Result{IsError: true, Content: "skill system is unavailable"}, nil
	}
	// Skill search mode: prefix '?' triggers keyword search across all skills.
	if strings.HasPrefix(args.Skill, "?") {
		query := strings.TrimSpace(strings.TrimPrefix(args.Skill, "?"))
		return t.searchSkills(query), nil
	}
	cmd, ok := t.Skills.Get(args.Skill)
	if !ok {
		if strings.TrimSpace(args.Skill) != "" && t.Runtime != nil {
			if result, handled := t.executeMCPPromptSkill(ctx, strings.TrimSpace(args.Skill), strings.TrimSpace(args.Args)); handled {
				return result, nil
			}
		}
		return Result{IsError: true, Content: t.skillNotFoundMsg(args.Skill)}, nil
	}
	if !cmd.Enabled {
		return Result{IsError: true, Content: fmt.Sprintf("skill %q is disabled", cmd.Name)}, nil
	}
	if cmd.DisableModelInvocation {
		return Result{IsError: true, Content: fmt.Sprintf("skill %q is only available for direct user invocation", cmd.Name)}, nil
	}

	// Validate external CLI tool dependencies before execution.
	if missing := checkRequiredTools(cmd.RequiresTools); len(missing) > 0 {
		return Result{IsError: true, Content: fmt.Sprintf(
			"skill %q requires the following tools to be installed and on PATH: %s. Install them before invoking this skill.",
			cmd.Name, strings.Join(missing, ", "))}, nil
	}
	if recorder, ok := t.Skills.(skillUsageRecorder); ok {
		recorder.RecordUsage(cmd.Name)
	}
	ref := skillRefForCommand(cmd)
	if t.OnSkillUsed != nil {
		t.OnSkillUsed(ref)
	}
	if cmd.LoadedFrom == commands.LoadedFromMCP && t.Runtime != nil {
		result, _ := t.executeMCPPromptSkill(ctx, cmd.Name, strings.TrimSpace(args.Args))
		t.notifySkillCompleted(cmd, SkillExecutionModeMCP, result, nil)
		return result, nil
	}
	if strings.EqualFold(strings.TrimSpace(cmd.Context), "fork") {
		result, err := t.executeForkedSkill(ctx, cmd, strings.TrimSpace(args.Args))
		t.notifySkillCompleted(cmd, SkillExecutionModeFork, result, err)
		return result, err
	}
	workDir, _ := os.Getwd()
	content := cmd.Expand(map[string]string{
		"DIR":  workDir,
		"ARGS": strings.TrimSpace(args.Args),
	})
	content = strings.TrimSpace(content)

	// If the skill declares dependencies on other skills, advise the agent.
	if depHint := buildDependencyHint(cmd, t.Skills); depHint != "" {
		content = depHint + "\n\n" + content
	}

	// Return a brief confirmation + inject skill content as follow-up user message.
	// This forces the model to process and act on the skill instructions,
	// matching Claude Code's inline skill behavior.
	result := Result{
		Content: fmt.Sprintf("Skill %q loaded. Follow the instructions below to complete the task.", cmd.Name),
		FollowUpMessages: []provider.Message{
			{
				Role: "user",
				Content: []provider.ContentBlock{
					{Type: "text", Text: content},
				},
			},
		},
	}
	t.notifySkillCompleted(cmd, SkillExecutionModeInline, result, nil)
	return result, nil
}

func (t SkillTool) executeForkedSkill(ctx context.Context, cmd *commands.Command, args string) (Result, error) {
	if cmd == nil {
		return Result{IsError: true, Content: "skill is unavailable"}, nil
	}
	if t.Provider == nil || t.Tools == nil || t.AgentFactory == nil {
		return Result{IsError: true, Content: fmt.Sprintf("skill %q requires fork execution support, but it is unavailable", cmd.Name)}, nil
	}

	workDir, _ := os.Getwd()
	task := strings.TrimSpace(cmd.Expand(map[string]string{
		"DIR":  workDir,
		"ARGS": args,
	}))
	if task == "" {
		return Result{IsError: true, Content: fmt.Sprintf("skill %q has no executable content", cmd.Name)}, nil
	}

	mgr := subagent.NewManager(config.SubAgentConfig{MaxConcurrent: 1, Timeout: 5 * time.Minute})
	id := mgr.Spawn(cmd.Name, task, cmd.Name, cmd.AllowedTools, ctx)
	allToolInfo := make([]subagent.ToolInfo, 0, len(t.Tools.List()))
	for _, tl := range t.Tools.List() {
		allToolInfo = append(allToolInfo, tl)
	}
	safego.Go("tool.skill.subagent", func() {
		subagent.Run(ctx, subagent.RunnerConfig{
			Provider:            t.Provider,
			AllTools:            allToolInfo,
			Task:                task,
			AllowedTools:        cmd.AllowedTools,
			Manager:             mgr,
			SubAgentID:          id,
			AgentFactory:        t.AgentFactory,
			WorkingDir:          t.WorkingDir,
			OnUsage:             t.OnUsage,
			SystemPromptBuilder: t.SystemPromptBuilder,
			BuildToolSet: func(allowedTools []string, _ []subagent.ToolInfo) interface{} {
				// Clone the registry so each skill sub-agent gets its own tool
				// instances with independent WorkingDir fields.
				cloned := t.Tools.Clone()
				if len(allowedTools) > 0 {
					all := cloned.ToolNames()
					for _, name := range all {
						if !sliceContains(allowedTools, name) {
							cloned.Unregister(name)
						}
					}
				} else {
					cloned.Unregister("spawn_agent")
					cloned.Unregister("wait_agent")
					cloned.Unregister("list_agents")
				}
				return cloned
			},
		})
	})

	result, err := subagent.Wait(ctx, mgr, id)
	if err != nil {
		if strings.TrimSpace(result) != "" {
			return Result{IsError: true, Content: strings.TrimSpace(result)}, nil
		}
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: strings.TrimSpace(result)}, nil
}

func (t SkillTool) executeMCPPromptSkill(ctx context.Context, skillName, rawArgs string) (Result, bool) {
	server, promptName, ok := splitMCPSkillName(skillName)
	if !ok {
		return Result{}, false
	}
	args, err := parseMCPPromptArgs(rawArgs)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid MCP skill args: %v", err)}, true
	}
	result, err := t.Runtime.GetPrompt(ctx, server, promptName, args)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, true
	}
	_ = commands.RecordUsage(skillName)
	var sb strings.Builder
	if result.Description != "" {
		sb.WriteString(strings.TrimSpace(result.Description))
		sb.WriteString("\n\n")
	}
	for i, msg := range result.Messages {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("[")
		sb.WriteString(firstNonEmptyString(msg.Role, "message"))
		sb.WriteString("]\n")
		sb.WriteString(firstNonEmptyString(msg.Text, msg.Raw))
	}
	content := strings.TrimSpace(sb.String())
	return Result{
		Content: fmt.Sprintf("MCP skill %q loaded. Follow the instructions below.", skillName),
		FollowUpMessages: []provider.Message{
			{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: content}},
			},
		},
	}, true
}

func splitMCPSkillName(name string) (string, string, bool) {
	parts := strings.SplitN(strings.TrimSpace(name), ":", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

func parseMCPPromptArgs(raw string) (map[string]interface{}, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var obj map[string]interface{}
	if strings.HasPrefix(raw, "{") {
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			return nil, err
		}
		return obj, nil
	}
	return map[string]interface{}{"input": raw}, nil
}

func (t SkillTool) notifySkillCompleted(cmd *commands.Command, mode SkillExecutionMode, result Result, err error) {
	if t.OnSkillCompleted == nil {
		return
	}
	name := ""
	scope := ""
	ref := ""
	if cmd != nil {
		name = cmd.Name
		scope = skillScopeForCommand(cmd)
		ref = skillRefForCommand(cmd)
	}
	t.OnSkillCompleted(SkillExecutionEvent{
		Name:   name,
		Ref:    ref,
		Scope:  scope,
		Mode:   mode,
		Result: result,
		Err:    err,
	})
}

func skillRefForCommand(cmd *commands.Command) string {
	if cmd == nil {
		return ""
	}
	if scope := skillScopeForCommand(cmd); scope != "" {
		return scope + ":" + strings.TrimSpace(cmd.Name)
	}
	return strings.TrimSpace(cmd.Name)
}

func skillScopeForCommand(cmd *commands.Command) string {
	if cmd == nil {
		return ""
	}
	if cmd.Source == commands.SourceProject {
		return "project"
	}
	if cmd.LoadedFrom == commands.LoadedFromSkills && cmd.Source == commands.SourceUser {
		return "global"
	}
	return ""
}

// Clone returns an independent copy of SkillTool for use by a different agent.
// Skills, Runtime, Provider, Tools, AgentFactory, and callbacks are shared.
// Only WorkingDir is agent-specific.
func (t SkillTool) Clone() Tool {
	return SkillTool{
		Skills:              t.Skills,
		NameLister:          t.NameLister,
		Runtime:             t.Runtime,
		Provider:            t.Provider,
		Tools:               t.Tools,
		AgentFactory:        t.AgentFactory,
		WorkingDir:          t.WorkingDir,
		OnUsage:             t.OnUsage,
		OnSkillUsed:         t.OnSkillUsed,
		OnSkillCompleted:    t.OnSkillCompleted,
		SystemPromptBuilder: t.SystemPromptBuilder,
	}
}

// skillNotFoundMsg returns an error message for an unknown skill, augmented
// with fuzzy match suggestions when close matches exist.
func (t SkillTool) skillNotFoundMsg(query string) string {
	q := strings.TrimSpace(query)
	base := fmt.Sprintf("skill %q not found", q)
	if t.NameLister == nil {
		return base
	}
	suggestions := suggestSkills(q, t.NameLister.SkillNames())
	if len(suggestions) == 0 {
		return base
	}
	var sb strings.Builder
	sb.WriteString(base)
	sb.WriteString(". Did you mean: ")
	for i, s := range suggestions {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("%q", s))
	}
	sb.WriteString("?")
	return sb.String()
}

// searchSkills performs keyword search across all available skills and returns
// ranked results. Empty query returns all skills (for browsing/discovery).
func (t SkillTool) searchSkills(query string) Result {
	if t.NameLister == nil || t.Skills == nil {
		return Result{IsError: true, Content: "skill search is unavailable"}
	}
	names := t.NameLister.SkillNames()
	if len(names) == 0 {
		return Result{Content: "No skills are currently available."}
	}
	queryLower := strings.ToLower(strings.TrimSpace(query))
	matches := t.collectSkillMatches(names, queryLower)
	if len(matches) == 0 {
		return Result{Content: fmt.Sprintf("No skills found matching %q. Try a different keyword or browse all skills with skill: \"?\".", query)}
	}
	sortMatches(matches)
	total := len(matches)
	if len(matches) > maxSearchResults {
		matches = matches[:maxSearchResults]
	}
	return Result{Content: formatSkillSearchResults(matches, queryLower, query, total)}
}

type skillSearchMatch struct {
	name  string
	desc  string
	score int
}

// collectSkillMatches iterates all skills, scoring each against the query.
func (t SkillTool) collectSkillMatches(names []string, queryLower string) []skillSearchMatch {
	var matches []skillSearchMatch
	for _, name := range names {
		cmd, ok := t.Skills.Get(name)
		if !ok || cmd == nil {
			continue
		}
		score, desc := scoreSkill(name, cmd, queryLower)
		if score > 0 {
			matches = append(matches, skillSearchMatch{name: name, desc: desc, score: score})
		}
	}
	return matches
}

// scoreSkill returns a relevance score and display description for a skill.
func scoreSkill(name string, cmd *commands.Command, queryLower string) (int, string) {
	desc := strings.TrimSpace(cmd.Description)
	when := strings.TrimSpace(cmd.WhenToUse)
	if queryLower == "" {
		return 1, firstNonEmptyString(desc, when)
	}
	score := 0
	nameLower := strings.ToLower(name)
	if nameLower == queryLower {
		score += 20
	} else if strings.Contains(nameLower, queryLower) {
		score += 10
	}
	if desc != "" && strings.Contains(strings.ToLower(desc), queryLower) {
		score += 5
	}
	if when != "" && strings.Contains(strings.ToLower(when), queryLower) {
		score += 5
	}
	return score, firstNonEmptyString(desc, when)
}

func sortMatches(matches []skillSearchMatch) {
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].name < matches[j].name
	})
}

const maxSearchResults = 25

func formatSkillSearchResults(matches []skillSearchMatch, queryLower, query string, total int) string {
	var sb strings.Builder
	if queryLower == "" {
		sb.WriteString(fmt.Sprintf("Available skills (%d total):\n\n", total))
	} else {
		sb.WriteString(fmt.Sprintf("Skills matching %q (%d found):\n\n", query, total))
	}
	for _, m := range matches {
		sb.WriteString("- " + m.name)
		if m.desc != "" {
			const maxDesc = 120
			if len(m.desc) > maxDesc {
				sb.WriteString(": " + m.desc[:maxDesc-3] + "...")
			} else {
				sb.WriteString(": " + m.desc)
			}
		}
		sb.WriteString("\n")
	}
	if total > maxSearchResults {
		sb.WriteString(fmt.Sprintf("\n... and %d more. Refine your query to narrow results.\n", total-maxSearchResults))
	}
	sb.WriteString("\nUse the skill tool with the exact skill name to load one.")
	return sb.String()
}

// checkRequiredTools returns the subset of tools that are not found on PATH.
func checkRequiredTools(tools []string) []string {
	var missing []string
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		if _, err := exec.LookPath(tool); err != nil {
			missing = append(missing, tool)
		}
	}
	return missing
}

// buildDependencyHint returns a short advisory message if the skill declares
// prerequisite skills that should be loaded first. Missing dependencies are
// noted but do not block execution - the agent can still proceed.
func buildDependencyHint(cmd *commands.Command, lookup SkillLookup) string {
	if cmd == nil || len(cmd.Dependencies) == 0 {
		return ""
	}
	var available, missing []string
	for _, dep := range cmd.Dependencies {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			continue
		}
		if lookup != nil {
			if dc, ok := lookup.Get(dep); ok && dc != nil && dc.Enabled {
				available = append(available, dep)
				continue
			}
		}
		missing = append(missing, dep)
	}
	var parts []string
	if len(available) > 0 {
		parts = append(parts, fmt.Sprintf("Prerequisite skills: %s. Load them via the skill tool if needed.", strings.Join(available, ", ")))
	}
	if len(missing) > 0 {
		parts = append(parts, fmt.Sprintf("Note: declared dependencies not found or disabled: %s.", strings.Join(missing, ", ")))
	}
	return strings.Join(parts, " ")
}
