package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/subagent"
)

type UseNamedAgentTool struct {
	Store               *subagent.TemplateStore
	Manager             *subagent.Manager
	Provider            provider.Provider
	ProviderGetter      func() provider.Provider
	Tools               *Registry
	AgentFactory        func(provider.Provider, interface{}, string, int) subagent.AgentRunner
	WorkingDir          string
	OnUsage             func(provider.TokenUsage)
	OnMetric            func(metrics.MetricEvent)
	SystemPromptBuilder func(task, agentType string) string
	AvailableModels     func() []string
}

func (t UseNamedAgentTool) currentProvider() provider.Provider {
	if t.ProviderGetter != nil {
		return t.ProviderGetter()
	}
	return t.Provider
}

func (t UseNamedAgentTool) Name() string { return "use_namedagent" }

func (t UseNamedAgentTool) Description() string {
	return `Invoke a named subagent template to execute a task. Returns an agent_id for use with wait_agent/list_agents. Each invocation is a fresh session with no persisted history. Supports an optional per-invocation model override (escalation lever for model-tiered cascades: re-run a failed cheap-tier run on a stronger model).`
}

func (t UseNamedAgentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {
				"type": "string",
				"description": "Name of the named subagent template to use"
			},
			"task": {
				"type": "string",
				"description": "The specific task for this invocation"
			},
			"context": {
				"type": "string",
				"description": "Optional additional context for the task"
			},
			"model": {
				"type": "string",
				"description": "Optional per-invocation model override. Takes precedence over the template's configured model. Must be one of the models listed in 'Sub-agent models' in the system prompt Environment section. Use it to escalate a failed cheap-tier run to a stronger model."
			}
		},
		"required": ["name", "task"]
	}`)
}

func (t UseNamedAgentTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Store == nil || t.Manager == nil {
		return Result{IsError: true, Content: "use_namedagent: not properly configured"}, nil
	}
	var args struct {
		Name    string `json:"name"`
		Task    string `json:"task"`
		Context string `json:"context"`
		Model   string `json:"model"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if strings.TrimSpace(args.Name) == "" {
		return Result{IsError: true, Content: "name is required"}, nil
	}
	if strings.TrimSpace(args.Task) == "" {
		return Result{IsError: true, Content: "task is required"}, nil
	}

	tmpl, err := t.Store.Load(strings.TrimSpace(args.Name))
	if err != nil {

		available, _ := t.Store.List()
		if len(available) > 0 {
			names := make([]string, len(available))
			for i, a := range available {
				names[i] = a.Name
			}
			return Result{IsError: true, Content: fmt.Sprintf("named subagent '%s' not found. Available: %s", args.Name, strings.Join(names, ", "))}, nil
		}
		return Result{IsError: true, Content: fmt.Sprintf("named subagent '%s' not found. Use create_namedagent to create one.", args.Name)}, nil
	}

	task := args.Task
	if args.Context != "" {
		task = args.Context + "\n\n" + args.Task
	}
	displayTask := args.Task

	allowedTools := tmpl.Tools
	if len(tmpl.BlockedTools) > 0 {

		if len(allowedTools) == 0 {
			if t.Tools != nil {
				for _, name := range t.Tools.ToolNames() {
					if !sliceContains(tmpl.BlockedTools, name) {
						allowedTools = append(allowedTools, name)
					}
				}
			}

		} else {

			filtered := make([]string, 0, len(allowedTools))
			for _, name := range allowedTools {
				if !sliceContains(tmpl.BlockedTools, name) {
					filtered = append(filtered, name)
				}
			}
			allowedTools = filtered
		}
	}

	if len(tmpl.Tools) > 0 && t.Tools != nil {
		available := t.Tools.ToolNames()
		var missing []string
		for _, declared := range tmpl.Tools {
			if !sliceContains(available, declared) {
				missing = append(missing, declared)
			}
		}
		if len(missing) > 0 {
			return Result{IsError: true, Content: fmt.Sprintf(
				"named subagent '%s' declares tool(s) %s which are not available in this session's toolset. Available tools: %s. Fix the template (create_namedagent tools=...) or register the tool before invoking it.",
				tmpl.Name, strings.Join(missing, ", "), strings.Join(available, ", "))}, nil
		}
	}

	effectiveModel := strings.TrimSpace(args.Model)
	if effectiveModel == "" {
		effectiveModel = strings.TrimSpace(tmpl.Model)
	}

	if effectiveModel != "" && t.AvailableModels != nil {
		available := t.AvailableModels()
		if len(available) > 0 && !sliceContains(available, effectiveModel) {
			return Result{IsError: true, Content: fmt.Sprintf(
				"named subagent '%s' requires model %q which is not available on the current endpoint. Available models: %s",
				tmpl.Name, effectiveModel, strings.Join(available, ", "))}, nil
		}
	}

	runProv := t.currentProvider()
	if effectiveModel != "" {
		if c, ok := runProv.(provider.ClonableWithModel); ok {
			runProv = c.CloneWithModel(effectiveModel)
		} else {
			return Result{IsError: true, Content: fmt.Sprintf(
				"named subagent '%s' requires model %q but the current provider (%T) does not support model overrides",
				tmpl.Name, effectiveModel, runProv)}, nil
		}
	}

	id := t.Manager.Spawn(tmpl.Name, task, displayTask, allowedTools, ctx)

	var allToolInfo []subagent.ToolInfo
	if t.Tools != nil {
		for _, ti := range t.Tools.List() {
			allToolInfo = append(allToolInfo, ti)
		}
	}

	tools := t.Tools
	runCtx := t.Manager.RootContext()

	customBuilder := func(task, agentType string) string {
		if tmpl.SystemPrompt != "" {
			return tmpl.SystemPrompt
		}
		if t.SystemPromptBuilder != nil {
			return t.SystemPromptBuilder(task, agentType)
		}
		return ""
	}

	safego.Go("tool.useNamedAgent", func() {
		subagent.Run(runCtx, subagent.RunnerConfig{
			Provider:            runProv,
			AllTools:            allToolInfo,
			Task:                task,
			AllowedTools:        allowedTools,
			Manager:             t.Manager,
			SubAgentID:          id,
			AgentFactory:        t.AgentFactory,
			Model:               effectiveModel,
			WorkingDir:          t.WorkingDir,
			OnUsage:             t.OnUsage,
			OnMetric:            t.OnMetric,
			SystemPromptBuilder: customBuilder,
			BuildToolSet: func(allowedTools []string, _ []subagent.ToolInfo) interface{} {
				cloned := tools.Clone()

				for _, name := range subAgentBlockedTools {
					cloned.Unregister(name)
				}

				cloned.Unregister("create_namedagent")
				cloned.Unregister("use_namedagent")
				cloned.Unregister("list_namedagent")
				if len(allowedTools) > 0 {
					all := cloned.ToolNames()
					for _, name := range all {
						if !sliceContains(allowedTools, name) {
							cloned.Unregister(name)
						}
					}
				}
				return cloned
			},
		})
	})

	return Result{Content: fmt.Sprintf("Named subagent '%s' started with ID: %s\nUse wait_agent or list_agents to monitor progress.", tmpl.Name, id)}, nil
}

func (t UseNamedAgentTool) Clone() Tool {
	return UseNamedAgentTool{
		Store:               t.Store,
		Manager:             t.Manager,
		Provider:            t.Provider,
		ProviderGetter:      t.ProviderGetter,
		Tools:               t.Tools,
		AgentFactory:        t.AgentFactory,
		WorkingDir:          t.WorkingDir,
		OnUsage:             t.OnUsage,
		OnMetric:            t.OnMetric,
		SystemPromptBuilder: t.SystemPromptBuilder,
		AvailableModels:     t.AvailableModels,
	}
}
