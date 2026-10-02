package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/subagent"
)

// BestOfNTool implements the best_of_n tool: trajectory-level test-time
// scaling (arXiv 2506.12928). It launches N independent sub-agent
// candidates on the SAME task (each isolated in its own git worktree by
// default), waits for all to finish, and picks a winner by
// distilled-summary consensus; when no winner is distinguishable it
// degrades to a sequential-retry conditioning hint distilled from all
// rollouts.
//
// The orchestration itself lives in internal/agentruntime (it needs
// RankSubagentResults/DistillIntoPrompt, and internal/tool must not import
// internal/agentruntime — cycle). Registration sites inject the runner via
// the Run field.
type BestOfNTool struct {
	Manager *subagent.Manager
	Run     func(ctx context.Context, task string, n int, tools []string, isolation, name string) string
}

func (t BestOfNTool) Name() string { return "best_of_n" }

func (t BestOfNTool) Description() string {
	return "Launch N independent parallel sub-agent candidates on the SAME task (best-of-N trajectory sampling), wait for all, and report the consensus winner. Test-time scaling for genuinely hard tasks: N runs cost N x sub-agent tokens. Each candidate runs in its own git worktree by default (isolation=worktree) so file edits never collide; the winner's worktree path is reported for merging. A candidate instruction appended to the task requires each run to verify its work and state the outcome in its final message. Selection uses distilled per-rollout summaries (attempted/progress/failures/verdict) ranked by consensus; when no winner is distinguishable, the tool returns a sequential-retry conditioning block distilled from ALL rollouts. Only use for tasks where one attempt frequently fails (tricky refactor, elusive bug, ambiguous architecture); for ordinary tasks do it yourself or spawn a single sub-agent. Long-running: if the caller context expires, a partial report with live candidate IDs is returned (candidates keep running; poll via wait_agent/list_agents)."
}

func (t BestOfNTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"task": {
			"type": "string",
			"description": "The complete task contract, identical for every candidate: objective, input boundaries (files/scope), constraints, acceptance criteria (verifiable Done checks), and the evidence to return. Include all context the candidates will need."
		},
		"n": {
			"type": "integer",
			"description": "Number of parallel candidates. Clamped to 2..4 (default 3). Requires n free sub-agent slots (16-session budget shared with other runs)."
		},
		"tools": {
			"type": "array",
			"items": { "type": "string" },
			"description": "Optional tool whitelist shared by all candidates (defaults to all parent tools except sub-agent tools)."
		},
		"isolation": {
			"type": "string",
			"enum": ["worktree", "none"],
			"description": "Filesystem isolation per candidate. 'worktree' (default): each candidate edits in its own fresh git worktree. 'none': shared working directory — only safe for read-only/research tasks."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI (per-candidate labels are derived from it). Write in the user's language."
		}
	},
	"required": ["task", "description"]
}`)
}

func (t BestOfNTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "best_of_n: sub-agent manager not available"}, nil
	}
	if t.Run == nil {
		return Result{IsError: true, Content: "best_of_n: orchestrator not wired"}, nil
	}
	var args struct {
		Task        string   `json:"task"`
		N           int      `json:"n"`
		Tools       []string `json:"tools"`
		Isolation   string   `json:"isolation"`
		Description string   `json:"description"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if strings.TrimSpace(args.Task) == "" {
		return Result{IsError: true, Content: "task is required"}, nil
	}
	isolation := strings.TrimSpace(args.Isolation)
	if isolation != "" && isolation != "worktree" && isolation != "none" {
		return Result{IsError: true, Content: fmt.Sprintf("invalid isolation %q: supported values are \"worktree\" (each candidate in its own worktree) and \"none\" (shared cwd, read-only tasks only)", isolation)}, nil
	}
	n := args.N
	if n == 0 {
		n = 3
	}
	if n < 2 || n > 4 {
		return Result{IsError: true, Content: fmt.Sprintf("invalid n=%d: candidates must be between 2 and 4", n)}, nil
	}
	report := t.Run(ctx, args.Task, n, args.Tools, isolation, args.Description)
	return Result{Content: report}, nil
}

// Clone shares Manager and the injected orchestrator across agents.
func (t BestOfNTool) Clone() Tool {
	return BestOfNTool{Manager: t.Manager, Run: t.Run}
}
