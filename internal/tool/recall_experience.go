package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/memory"
)

const (
	recallExperienceDefaultMax = 3

	recallExperienceMaxMax = 5
)

type RecallExperienceTool struct {
	workingDir string
}

func NewRecallExperienceTool(workingDir string) *RecallExperienceTool {
	return &RecallExperienceTool{workingDir: workingDir}
}

func (t *RecallExperienceTool) Name() string { return "recall_experience" }

func (t *RecallExperienceTool) Description() string {
	return "Query the project experience store for past cases similar to a task or error (proactive memory recall). Use at any decision moment where prior art would help: choosing between approaches, mid-run debugging, unfamiliar errors - not just after a failure. Returns distilled cases with task/approach/outcome. Read-only; cases are recorded automatically at run completion. Empty result means no relevant past experience, not an error."
}

func (t *RecallExperienceTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {
					"type": "string",
					"description": "What to look for: the task shape, error text, or technique in question (e.g. 'flaky test wall time', 'release version bump flow')."
				},
				"max": {
					"type": "integer",
					"description": "Max cases to return (default 3, cap 5)."
				}
			},
			"required": ["query"]
		}`)
}

func (t *RecallExperienceTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var params struct {
		Query string `json:"query"`
		Max   int    `json:"max"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if strings.TrimSpace(params.Query) == "" {
		return Result{IsError: true, Content: "query is required"}, nil
	}
	max := params.Max
	if max == 0 {
		max = recallExperienceDefaultMax
	}
	if max < 1 || max > recallExperienceMaxMax {
		return Result{IsError: true, Content: fmt.Sprintf("invalid max=%d: must be between 1 and %d", max, recallExperienceMaxMax)}, nil
	}

	store := memory.NewProjectExperienceStore(t.workingDir)
	if store == nil {
		return Result{Content: "experience store not available for this working directory (project memory disabled here); nothing to recall."}, nil
	}
	block := store.FormatIndex(params.Query, max)
	if block == "" {
		return Result{Content: "no relevant past experience found for this query."}, nil
	}
	return Result{Content: block}, nil
}
