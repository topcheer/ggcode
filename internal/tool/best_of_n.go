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
	Run     func(ctx context.Context, req BestOfNRequest) string
	// AvailableModels resolves the current endpoint's model list for
	// per-candidate model validation (r380); nil disables validation.
	AvailableModels func() []string
}

// BestOfNRequest is the validated input for one best-of-N orchestration.
type BestOfNRequest struct {
	Task      string
	N         int
	Tools     []string
	Isolation string
	Name      string
	// Models optionally overrides the model per candidate (r380
	// cross-model ensemble). Empty = every candidate inherits the parent
	// runtime model (same-model sampling).
	Models []string
	// VerifierModels optionally routes the r439 tie-break discriminator
	// to a model different from the tied candidates (heterogeneous
	// verification, arXiv:2512.02304: cross-family verification beats
	// self-verification, and the benefit shrinks as solver and verifier
	// converge). The first entry not used by either tied candidate is
	// picked. Empty = discriminator inherits the parent model (r439).
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
	// r380: a heterogeneous ensemble runs one candidate per model; the
	// explicit model list is the source of truth for n (mixing n+models with
	// mismatched lengths would silently drop candidates). Validated against
	// the RAW args.N so an explicit conflicting n is refused while an omitted
	// n never collides with the default.
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

// validateVerifierModels checks the optional discriminator model list
// (1-4 entries, each available on the current endpoint). Unlike the
// candidate list there is no count coupling with n.
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

// validateBestOfNModels checks the optional per-candidate model list and
// returns the effective candidate count. Empty models leaves n untouched.
// A non-empty error string means the request must be refused.
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

// Clone shares Manager and the injected orchestrator across agents.
func (t BestOfNTool) Clone() Tool {
	return BestOfNTool{Manager: t.Manager, Run: t.Run, AvailableModels: t.AvailableModels}
}
