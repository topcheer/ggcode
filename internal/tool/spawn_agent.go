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

// subAgentBlockedTools lists tools that are unconditionally removed from every
// sub-agent's tool set, regardless of what allowedTools requests. Sub-agents
// must never interact with the user, spawn nested agents, or manage teams.
var subAgentBlockedTools = []string{
	"ask_user",
	// compact_context (CAT) requests compaction of the OWNING agent's
	// conversation; a one-shot sub-agent must not compact its parent.
	"compact_context",
	"spawn_agent",
	"best_of_n",
	// r436: a workflow is a fan-out of sub-agents; nested workflows inside
	// sub-agents would multiply spawn slots unboundedly (16-slot session
	// budget). One level of workflow nesting only, enforced here.
	"workflow_run",
	"wait_agent",
	"list_agents",
	"cancel_agent",
	"teammate_spawn",
	"teammate_shutdown",
	"team_create",
	"team_delete",
	// #864: swarm/team board + messaging tools were missing — a sub-agent
	// could inject tasks into the shared board, message arbitrary teammates,
	// and trigger other agents' approval loops (live-verified by re-review).
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

// SpawnAgentTool implements the spawn_agent tool.
type SpawnAgentTool struct {
	Manager             *subagent.Manager
	Provider            provider.Provider        // static fallback; prefer ProviderGetter if set
	ProviderGetter      func() provider.Provider // resolves the parent agent's live provider
	AvailableModels     func() []string          // resolves models on the current endpoint for validation
	Tools               *Registry
	AgentFactory        subagent.AgentFactory
	WorkingDir          string // working directory to propagate to sub-agent
	OnUsage             func(provider.TokenUsage)
	OnMetric            func(metrics.MetricEvent)           // forwarded to the sub-agent's Agent so its LLM/tool telemetry reaches the parent collector (sa-218)
	SystemPromptBuilder func(task, agentType string) string // builds rich system prompt with project context
	// TrajBackflow (r460, injected by the agent side to avoid a tool->agent
	// import cycle) folds a worktree-isolated sub-agent's extracted
	// learnings back into the main workspace store after its run.
	TrajBackflow func(mainWorkingDir, worktreePath string)
}

// currentProvider returns the live provider if ProviderGetter is set, otherwise
// falls back to the static Provider field.
func (t SpawnAgentTool) currentProvider() provider.Provider {
	if t.ProviderGetter != nil {
		return t.ProviderGetter()
	}
	return t.Provider
}

func (t SpawnAgentTool) Name() string { return "spawn_agent" }

func (t SpawnAgentTool) Description() string {
	return "Spawn a one-shot sub-agent run to work on an independent task. Put the full task and needed context in the initial request; do not assume the run will accept later work via send_message. Returns an agent_id. Use wait_agent or list_agents to poll status and retrieve the eventual result. At most 16 sub-agents may run concurrently (config: subagents.max_concurrent); spawns beyond the limit are rejected until earlier runs complete. A separate lifetime budget (config: subagents.max_total, default unlimited) bounds the TOTAL number of spawns per session and is not released when agents finish - once exhausted, no new sub-agents can be spawned. Delegation economics: a sub-agent re-reads context from scratch (context-copy token cost) and adds wall-clock latency; only fan out genuinely independent tasks, and for small or trivial work do it yourself with local tools."
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
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
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

	// Validate requested tool names against the registry. Warn (don't block)
	// if unknown tools are requested — the BuildToolSet filter will silently
	// drop them, so the sub-agent would run with fewer tools than expected.
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

	// Validate model override against available models on the current endpoint.
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
	// r453 read-back handshake: when the task carries acceptance criteria,
	// instruct the sub-agent to restate them (own words, per criterion)
	// before doing any work. The restatement is checked at wait time
	// (readBackReminder) - closes the front-end gap of the delegation
	// contract; r384 acceptance covers the back end.
	args.Task += readBackNudge(args.Task)

	id, worktreePath, err := t.Launch(ctx, LaunchOptions{
		Name:        name,
		Task:        args.Task,
		DisplayTask: displayTask,
		Tools:       args.Tools,
		Model:       args.Model,
		AgentType:   args.SubagentType,
		Isolation:   isolation,
	})
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("%v. Fix the git state or retry with isolation=none.", err)}, nil
	}

	content := fmt.Sprintf("Sub-agent spawned with ID: %s\nUse wait_agent or list_agents to monitor progress and retrieve the result.", id)
	if worktreePath != "" {
		content += fmt.Sprintf("\nIsolated in git worktree: %s (branch: %s). Its edits stay off the parent working tree; inspect or merge from that path after the run completes.", worktreePath, filepath.Base(worktreePath))
	}
	return Result{Content: content}, nil
}

// LaunchOptions carries the pre-validated inputs for one sub-agent launch.
// It is the seam the best_of_n orchestrator (internal/agentruntime) uses to
// launch parallel candidates through the exact same run path as spawn_agent.
type LaunchOptions struct {
	Name        string
	Task        string
	DisplayTask string
	Tools       []string
	Model       string
	AgentType   string
	Isolation   string
}

// Launch spawns one sub-agent and starts its run goroutine. Inputs are
// expected to be already validated by the caller (Execute or an
// orchestrator); Launch performs spawn, display-model resolution, optional
// worktree isolation, and the background run launch. Returns the sub-agent
// ID and the isolation worktree path ("" when not isolated).
func (t SpawnAgentTool) Launch(ctx context.Context, opts LaunchOptions) (string, string, error) {
	id := t.Manager.Spawn(opts.Name, opts.Task, opts.DisplayTask, opts.Tools, ctx)

	// Store the model name on the sub-agent for display purposes.
	// When no model override is specified, inherit the parent agent's runtime model.
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
			sa.Model = displayModel
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

	// Use the manager's lifecycle ctx, NOT the caller's per-call ctx, so the
	// sub-agent survives the parent turn ending (see locks.md S6).
	tools := t.Tools
	runCtx := t.Manager.RootContext()
	model := strings.TrimSpace(opts.Model)

	runWorkDir := t.WorkingDir
	if worktreePath != "" {
		runWorkDir = worktreePath
	}

	prov := t.currentProvider()
	safego.Go("tool.spawnAgent.subagent", func() {
		// r460: fold any learnings the isolated sub-agent extracted back
		// into the main workspace store before its run context ends.
		// Without this, worktree-isolated experience died with the
		// transient worktree (sub-agent WorkingDir = worktreePath, so its
		// post-run persist landed in .ggcode/worktrees/<id>/). Best effort.
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
			WorkingDir:          runWorkDir,
			OnUsage:             t.OnUsage,
			OnMetric:            t.OnMetric,
			SystemPromptBuilder: t.SystemPromptBuilder,
			BuildToolSet: func(allowedTools []string, _ []subagent.ToolInfo) interface{} {
				// Clone the registry so each sub-agent gets its own tool
				// instances with independent WorkingDir fields (data-race
				// safety across concurrent candidates in different worktrees).
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

// setupIsolationWorktree creates the isolation worktree for a spawned
// sub-agent when isolation="worktree". Synthetic IDs (sa-limit-*/sa-shutdown-*)
// are pre-failed registrations that never run, so no worktree is created for
// them. On creation failure the sub-agent is cancelled rather than silently
// downgraded to non-isolated, and the wrapped error is returned.
func (t SpawnAgentTool) setupIsolationWorktree(ctx context.Context, isolation, id string) (string, error) {
	if isolation != "worktree" {
		return "", nil
	}
	if strings.HasPrefix(id, "sa-limit-") || strings.HasPrefix(id, "sa-shutdown-") {
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

// Clone returns an independent copy of SpawnAgentTool for use by a different agent.
// Manager, Provider, AgentFactory, and Tools are intentionally shared across agents
// (they coordinate sub-agent lifecycle). Only WorkingDir is agent-specific.
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

// contains checks if a string slice contains a given string.
func sliceContains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
