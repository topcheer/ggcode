package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/subagent"
)

var subAgentBlockedTools = []string{
	"ask_user",

	"compact_context",
	"spawn_agent",
	"best_of_n",

	"workflow_run",
	"wait_agent",
	"list_agents",
	"cancel_agent",
	"teammate_spawn",
	"teammate_shutdown",
	"team_create",
	"team_delete",

	"swarm_task_create",
	"swarm_task_claim",
	"swarm_task_complete",
	"swarm_task_list",
	"teammate_results",
	"teammate_list",
	"send_message",
	"lanchat",
	"a2a_remote",
	"a2a_send_task",
}

// DefaultSubAgentMaxToolCalls is the conservative turn budget applied when a
// spawn omits max_tool_calls. Before this default an omitted budget meant an
// unbounded agent loop; a looping sub-agent then burned tokens until manual
// cancel (33min / 290 tool calls, #5013 same-day recurrence sa-16). Spawns
// are bounded by default now; pass max_tool_calls explicitly for a different
// budget.
const DefaultSubAgentMaxToolCalls = 60

// normalizeMaxToolCalls maps an omitted or invalid budget to the conservative
// default so a spawn can never run unbounded by accident (#5013 recurrence).
func normalizeMaxToolCalls(n int) (effective int, defaulted bool) {
	if n > 0 {
		return n, false
	}
	return DefaultSubAgentMaxToolCalls, true
}

type SpawnAgentTool struct {
	Manager             *subagent.Manager
	Provider            provider.Provider
	ProviderGetter      func() provider.Provider
	AvailableModels     func() []string
	Tools               *Registry
	AgentFactory        subagent.AgentFactory
	WorkingDir          string
	OnUsage             func(provider.TokenUsage)
	OnMetric            func(metrics.MetricEvent)
	SystemPromptBuilder func(task, agentType string) string
	TrajBackflow        func(mainWorkingDir, worktreePath string)
}

func (t SpawnAgentTool) currentProvider() provider.Provider {
	if t.ProviderGetter != nil {
		return t.ProviderGetter()
	}
	return t.Provider
}

func (t SpawnAgentTool) Name() string { return "spawn_agent" }

func (t SpawnAgentTool) Description() string {
	return "Spawn a one-shot sub-agent run to work on an independent task. Put the full task and needed context in the initial request; do not assume the run will accept later work via send_message. Returns an agent_id. Use wait_agent or list_agents to poll status and retrieve the eventual result. At most 16 sub-agents may run concurrently (config: subagents.max_concurrent); spawns beyond the limit are rejected until earlier runs complete. A separate lifetime budget (config: subagents.max_total, default unlimited) bounds the TOTAL number of spawns per session and is not released when agents finish - once exhausted, no new sub-agents can be spawned. Delegation trade-off: a sub-agent re-reads context from scratch (context-copy token cost) and adds wall-clock latency, so do small in-scope work yourself; BUT delegation keeps that work's context OUT of yours - for a complex independent task (research, exploration, multi-file reads, or a NEW task unrelated to your current main thread), prefer spawn_agent to protect your main context."
}

func (t SpawnAgentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"task": {
			"type": "string",
			"description": "The complete task description for the one-shot sub-agent run. Structure it as a task contract: objective, input boundaries (files/scope), constraints, acceptance criteria (verifiable Done checks), and the evidence to return (test results / diff summary). Include all context the sub-agent will need."
		},
		"tools": {
			"type": "array",
			"items": {
				"type": "string"
			},
			"description": "Optional list of tool names the sub-agent can use (defaults to all parent tools except sub-agent tools)"
		},
		"context": {
			"type": "string",
			"description": "Optional additional context prepended to the sub-agent's initial task. Prefer including context here rather than trying to send follow-up work later."
		},
		"model": {
			"type": "string",
			"description": "Optional model name for the sub-agent. Must be one of the models listed in 'Sub-agent models' in the system prompt Environment section. Choose a cheaper/faster model for simple tasks, a stronger model for complex reasoning."
		},
		"subagent_type": {
			"type": "string",
			"description": "Optional type of specialized agent (e.g., 'Explore', 'Plan')"
		},
		"isolation": {
			"type": "string",
			"enum": ["none", "worktree"],
			"description": "Optional filesystem isolation for the run. 'worktree' creates a fresh git worktree from HEAD under .ggcode/worktrees/ and runs the sub-agent there, so its file edits never collide with the parent's working tree. The worktree path is returned in the spawn result and on wait_agent snapshots; the worktree and its branch are kept after the run for inspection or merging. Default: none (inherit the parent's working directory)."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
		},
		"max_tool_calls": {
			"type": "integer",
			"description": "Hard budget for the sub-agent's agent loop, in LLM turns (each turn may issue several tool calls). The run stops with its partial result when the budget is reached. If omitted (or <= 0), a conservative default of 60 LLM turns applies - unbounded runs are no longer possible. Raise it explicitly for tasks that legitimately need more turns."
		}
	},
	"required": [
		"task",
		"description"
	]
}`)
}

func (t SpawnAgentTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "spawn_agent: sub-agent manager not available"}, nil
	}
	var args struct {
		Task         string   `json:"task"`
		Tools        []string `json:"tools"`
		Context      string   `json:"context"`
		Model        string   `json:"model"`
		SubagentType string   `json:"subagent_type"`
		Isolation    string   `json:"isolation"`
		Description  string   `json:"description"`
		MaxToolCalls int      `json:"max_tool_calls"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.Task == "" {
		return Result{IsError: true, Content: "task is required"}, nil
	}

	isolation := strings.TrimSpace(args.Isolation)
	if isolation != "" && isolation != "none" && isolation != "worktree" {
		return Result{IsError: true, Content: fmt.Sprintf("invalid isolation %q: supported values are \"none\" (inherit the parent working directory) and \"worktree\" (run in a fresh git worktree from HEAD)", isolation)}, nil
	}

	if len(args.Tools) > 0 && t.Tools != nil {
		var unknown []string
		for _, name := range args.Tools {
			if _, ok := t.Tools.Get(name); !ok {
				unknown = append(unknown, name)
			}
		}
		if len(unknown) > 0 {
			return Result{IsError: true, Content: fmt.Sprintf("unknown tool(s) requested: %s. Use the tool registry to find valid tool names.", strings.Join(unknown, ", "))}, nil
		}
	}

	if model := strings.TrimSpace(args.Model); model != "" && t.AvailableModels != nil {
		available := t.AvailableModels()
		if len(available) > 0 {
			found := false
			for _, m := range available {
				if m == model {
					found = true
					break
				}
			}
			if !found {
				return Result{IsError: true, Content: fmt.Sprintf(
					"model %q is not available on the current endpoint. Available models: %s",
					model, strings.Join(available, ", "))}, nil
			}
		}
	}

	name := strings.TrimSpace(args.Description)
	if name == "" {
		name = "sub-agent"
	}

	displayTask := args.Task
	if args.Context != "" {
		args.Task = args.Context + "\n\n" + args.Task
	}

	args.Task += readBackNudge(args.Task)

	maxToolCalls, budgetDefaulted := normalizeMaxToolCalls(args.MaxToolCalls)

	id, worktreePath, err := t.Launch(ctx, LaunchOptions{
		Name:         name,
		Task:         args.Task,
		DisplayTask:  displayTask,
		Tools:        args.Tools,
		Model:        args.Model,
		AgentType:    args.SubagentType,
		Isolation:    isolation,
		MaxToolCalls: maxToolCalls,
	})
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("%v. Fix the git state or retry with isolation=none.", err)}, nil
	}

	budgetSource := "explicit max_tool_calls"
	if budgetDefaulted {
		budgetSource = "conservative default; pass max_tool_calls to change it"
	}
	content := fmt.Sprintf("Sub-agent spawned with ID: %s\nIt runs asynchronously - before waiting, continue with work that does not depend on this result; harvest it later via wait_agent or list_agents.\nBudget: %d LLM turns (%s). The run stops with its partial result when the budget is reached.", id, maxToolCalls, budgetSource)
	if worktreePath != "" {
		content += fmt.Sprintf("\nIsolated in git worktree: %s (branch: %s). Its edits stay off the parent working tree; inspect or merge from that path after the run completes.", worktreePath, filepath.Base(worktreePath))
	}
	return Result{Content: content}, nil
}

type LaunchOptions struct {
	Name        string
	Task        string
	DisplayTask string
	Tools       []string
	Model       string
	AgentType   string
	Isolation   string
	// MaxToolCalls is a deterministic hard budget for the sub-agent's
	// agent loop (max iterations, each iteration being one LLM turn that
	// may issue tool calls). Values <= 0 fall back to the conservative
	// DefaultSubAgentMaxToolCalls - an unbounded run is no longer possible
	// via this tool; N > 0 stops the run after N turns with the partial
	// result (#5013 and its same-day recurrence).
	MaxToolCalls int
}

func (t SpawnAgentTool) Launch(ctx context.Context, opts LaunchOptions) (string, string, error) {
	// Defense in depth: no Launch caller can produce an unbounded run by
	// passing 0 (#5013 recurrence - the loop only stops on manual cancel).
	opts.MaxToolCalls, _ = normalizeMaxToolCalls(opts.MaxToolCalls)

	id := t.Manager.Spawn(opts.Name, opts.Task, opts.DisplayTask, opts.Tools, ctx)

	displayModel := opts.Model
	if displayModel == "" {
		if prov := t.currentProvider(); prov != nil {
			if mp, ok := prov.(provider.ModelNameProvider); ok {
				displayModel = mp.ModelName()
			}
		}
	}
	if displayModel != "" {
		if sa, ok := t.Manager.Get(id); ok && sa != nil {

			sa.SetModel(displayModel)
		}
	}

	worktreePath, err := t.setupIsolationWorktree(ctx, opts.Isolation, id)
	if err != nil {
		return id, "", fmt.Errorf("isolation worktree creation failed: %w", err)
	}

	var allToolInfo []subagent.ToolInfo
	if t.Tools != nil {
		for _, ti := range t.Tools.List() {
			allToolInfo = append(allToolInfo, ti)
		}
	}

	tools := t.Tools
	runCtx := t.Manager.RootContext()
	model := strings.TrimSpace(opts.Model)

	runWorkDir := t.WorkingDir
	if worktreePath != "" {
		runWorkDir = worktreePath
	}

	prov := t.currentProvider()
	safego.Go("tool.spawnAgent.subagent", func() {

		if worktreePath != "" && t.TrajBackflow != nil {
			backflow := t.TrajBackflow
			mainWD := t.WorkingDir
			defer func() { backflow(mainWD, worktreePath) }()
		}
		subagent.Run(runCtx, subagent.RunnerConfig{
			Provider:            prov,
			AllTools:            allToolInfo,
			Task:                opts.Task,
			AllowedTools:        opts.Tools,
			Manager:             t.Manager,
			SubAgentID:          id,
			AgentFactory:        t.AgentFactory,
			Model:               model,
			AgentType:           opts.AgentType,
			MaxToolCalls:        opts.MaxToolCalls,
			WorkingDir:          runWorkDir,
			OnUsage:             t.OnUsage,
			OnMetric:            t.OnMetric,
			SystemPromptBuilder: t.SystemPromptBuilder,
			BuildToolSet: func(allowedTools []string, _ []subagent.ToolInfo) interface{} {

				cloned := tools.Clone()
				for _, name := range subAgentBlockedTools {
					cloned.Unregister(name)
				}
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
	return id, worktreePath, nil
}

func (t SpawnAgentTool) setupIsolationWorktree(ctx context.Context, isolation, id string) (string, error) {
	if isolation != "worktree" {
		return "", nil
	}

	if subagent.IsSyntheticID(id) {
		return "", nil
	}
	wtPath, _, err := createAgentWorktree(ctx, t.WorkingDir, id)
	if err != nil {
		t.Manager.Cancel(id)
		return "", fmt.Errorf("sub-agent %s cancelled: %w", id, err)
	}
	t.Manager.SetWorktree(id, wtPath)
	return wtPath, nil
}

func (t SpawnAgentTool) Clone() Tool {
	return SpawnAgentTool{
		Manager:             t.Manager,
		Provider:            t.Provider,
		ProviderGetter:      t.ProviderGetter,
		AvailableModels:     t.AvailableModels,
		Tools:               t.Tools,
		AgentFactory:        t.AgentFactory,
		WorkingDir:          t.WorkingDir,
		OnUsage:             t.OnUsage,
		OnMetric:            t.OnMetric,
		SystemPromptBuilder: t.SystemPromptBuilder,
		TrajBackflow:        t.TrajBackflow,
	}
}

func sliceContains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
