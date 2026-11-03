package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/subagent"
)

type BestOfNTool struct {
	Manager         *subagent.Manager
	Run             func(ctx context.Context, req BestOfNRequest) string
	AvailableModels func() []string
}

type BestOfNRequest struct {
	Task           string
	N              int
	Tools          []string
	Isolation      string
	Name           string
	Models         []string
	VerifierModels []string
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
			"description": "Number of parallel candidates. Clamped to 2..4 (default 3). Requires n free sub-agent slots (16-session budget shared with other runs). Ignored when models is set: one candidate runs per model."
		},
		"models": {
			"type": "array",
			"items": { "type": "string" },
			"minItems": 2,
			"maxItems": 4,
			"description": "Optional heterogeneous per-candidate models (2-4), each available on the current endpoint. One candidate per model, in order. Mixing model tiers (cheap + flagship) decorrelates candidate errors so consensus ranking gets independent votes instead of N copies of one model's failure modes; a small-model ensemble can match a single frontier model at lower cost. When omitted, all candidates run the parent's current model."
		},
		"verifier_models": {
			"type": "array",
			"items": { "type": "string" },
			"minItems": 1,
			"maxItems": 4,
			"description": "Optional model(s) for the execution tie-break discriminator used when candidates rank too close to separate by consensus (1-4, each available on the current endpoint). The first model not used by either tied candidate is picked, so the verdict comes from an independent model family - cross-family verification measurably beats same-model self-verification (arXiv:2512.02304). When omitted, the discriminator inherits the parent model."
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
		Task           string   `json:"task"`
		N              int      `json:"n"`
		Models         []string `json:"models"`
		VerifierModels []string `json:"verifier_models"`
		Tools          []string `json:"tools"`
		Isolation      string   `json:"isolation"`
		Description    string   `json:"description"`
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
	if len(args.Models) > 0 {
		var modelsErr string
		n, modelsErr = validateBestOfNModels(args.Models, args.N, t.AvailableModels)
		if modelsErr != "" {
			return Result{IsError: true, Content: modelsErr}, nil
		}
	} else if n == 0 {
		n = 3
	}
	if n < 2 || n > 4 {
		return Result{IsError: true, Content: fmt.Sprintf("invalid n=%d: candidates must be between 2 and 4", n)}, nil
	}
	if vErr := validateVerifierModels(args.VerifierModels, t.AvailableModels); vErr != "" {
		return Result{IsError: true, Content: vErr}, nil
	}
	report := t.Run(ctx, BestOfNRequest{Task: args.Task, N: n, Tools: args.Tools, Isolation: isolation, Name: args.Description, Models: args.Models, VerifierModels: args.VerifierModels})
	return Result{Content: report}, nil
}

func validateVerifierModels(models []string, availableModels func() []string) string {
	if len(models) == 0 {
		return ""
	}
	if len(models) > 4 {
		return fmt.Sprintf("invalid verifier_models: provide 1-4 discriminator models (got %d)", len(models))
	}
	if availableModels != nil {
		if available := availableModels(); len(available) > 0 {
			for _, m := range models {
				found := false
				for _, a := range available {
					if a == m {
						found = true
						break
					}
				}
				if !found {
					return fmt.Sprintf("verifier model %q is not available on the current endpoint. Available models: %s", m, strings.Join(available, ", "))
				}
			}
		}
	}
	return ""
}

func validateBestOfNModels(models []string, n int, availableModels func() []string) (int, string) {
	if len(models) == 0 {
		return n, ""
	}
	if len(models) < 2 || len(models) > 4 {
		return n, fmt.Sprintf("invalid models: provide 2-4 per-candidate models (got %d)", len(models))
	}
	if n != 0 && n != len(models) {
		return n, fmt.Sprintf("n=%d conflicts with models (got %d models): when models is set, one candidate runs per model; omit n", n, len(models))
	}
	if availableModels != nil {
		if available := availableModels(); len(available) > 0 {
			for _, m := range models {
				found := false
				for _, a := range available {
					if a == m {
						found = true
						break
					}
				}
				if !found {
					return n, fmt.Sprintf("model %q is not available on the current endpoint. Available models: %s", m, strings.Join(available, ", "))
				}
			}
		}
	}
	return len(models), ""
}

func (t BestOfNTool) Clone() Tool {
	return BestOfNTool{Manager: t.Manager, Run: t.Run, AvailableModels: t.AvailableModels}
}
