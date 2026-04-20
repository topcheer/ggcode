package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/subagent"
)

type SkillLookup interface {
	Get(name string) (*commands.Command, bool)
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
	Mode   SkillExecutionMode
	Result Result
	Err    error
}

type SkillTool struct {
	Skills           SkillLookup
	Runtime          MCPRuntime
	Provider         provider.Provider
	Tools            *Registry
	AgentFactory     subagent.AgentFactory
	OnSkillUsed      func(name string)               // optional callback when a skill is loaded by the agent
	OnSkillCompleted func(event SkillExecutionEvent) // optional callback when execution finishes
}

func (t SkillTool) Name() string { return "skill" }

func (t SkillTool) Description() string {
	return "Load a reusable skill workflow or prompt. Use this when a listed skill clearly matches the user's task, then continue the task using the returned guidance."
}

func (t SkillTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"skill": {
				"type": "string",
				"description": "Skill name to load"
			},
			"args": {
				"type": "string",
				"description": "Optional user arguments passed to the skill"
			}
		},
		"required": ["skill"]
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
	cmd, ok := t.Skills.Get(args.Skill)
	if !ok {
		if strings.TrimSpace(args.Skill) != "" && t.Runtime != nil {
			if result, handled := t.executeMCPPromptSkill(ctx, strings.TrimSpace(args.Skill), strings.TrimSpace(args.Args)); handled {
				return result, nil
			}
		}
		return Result{IsError: true, Content: fmt.Sprintf("skill %q not found", strings.TrimSpace(args.Skill))}, nil
	}
	if !cmd.Enabled {
		return Result{IsError: true, Content: fmt.Sprintf("skill %q is disabled", cmd.Name)}, nil
	}
	if cmd.DisableModelInvocation {
		return Result{IsError: true, Content: fmt.Sprintf("skill %q is only available for direct user invocation", cmd.Name)}, nil
	}
	if recorder, ok := t.Skills.(skillUsageRecorder); ok {
		recorder.RecordUsage(cmd.Name)
	}
	if t.OnSkillUsed != nil {
		t.OnSkillUsed(cmd.Name)
	}
	if cmd.LoadedFrom == commands.LoadedFromMCP && t.Runtime != nil {
		result, _ := t.executeMCPPromptSkill(ctx, cmd.Name, strings.TrimSpace(args.Args))
		t.notifySkillCompleted(cmd.Name, SkillExecutionModeMCP, result, nil)
		return result, nil
	}
	if strings.EqualFold(strings.TrimSpace(cmd.Context), "fork") {
		result, err := t.executeForkedSkill(ctx, cmd, strings.TrimSpace(args.Args))
		t.notifySkillCompleted(cmd.Name, SkillExecutionModeFork, result, err)
		return result, err
	}
	workDir, _ := os.Getwd()
	content := cmd.Expand(map[string]string{
		"DIR":  workDir,
		"ARGS": strings.TrimSpace(args.Args),
	})
	result := Result{Content: strings.TrimSpace(content)}
	t.notifySkillCompleted(cmd.Name, SkillExecutionModeInline, result, nil)
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
	id := mgr.Spawn(task, cmd.Name, cmd.AllowedTools, ctx)
	allToolInfo := make([]subagent.ToolInfo, 0, len(t.Tools.List()))
	for _, tl := range t.Tools.List() {
		allToolInfo = append(allToolInfo, tl)
	}
	go subagent.Run(ctx, subagent.RunnerConfig{
		Provider:     t.Provider,
		AllTools:     allToolInfo,
		Task:         task,
		AllowedTools: cmd.AllowedTools,
		Manager:      mgr,
		SubAgentID:   id,
		AgentFactory: t.AgentFactory,
		BuildToolSet: func(allowedTools []string, _ []subagent.ToolInfo) interface{} {
			subReg := NewRegistry()
			registerTool := func(name string) {
				if tl, ok := t.Tools.Get(name); ok {
					_ = subReg.Register(tl)
				}
			}
			if len(allowedTools) > 0 {
				for _, name := range allowedTools {
					registerTool(name)
				}
				return subReg
			}
			for _, tl := range t.Tools.List() {
				switch tl.Name() {
				case "spawn_agent", "wait_agent", "list_agents":
					continue
				default:
					registerTool(tl.Name())
				}
			}
			return subReg
		},
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
	return Result{Content: strings.TrimSpace(sb.String())}, true
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

func (t SkillTool) notifySkillCompleted(name string, mode SkillExecutionMode, result Result, err error) {
	if t.OnSkillCompleted == nil {
		return
	}
	t.OnSkillCompleted(SkillExecutionEvent{
		Name:   name,
		Mode:   mode,
		Result: result,
		Err:    err,
	})
}
