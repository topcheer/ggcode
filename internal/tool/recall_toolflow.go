package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/topcheer/ggcode/internal/memory"
)

const (
	// recallToolFlowDefaultMax / recallToolFlowMaxMax bound the pattern list:
	// workflows are advisory context, a long list is noise.
	recallToolFlowDefaultMax = 5
	recallToolFlowMaxMax     = 10
)

// RecallToolFlowTool lets the agent consult cross-session tool workflow
// statistics (r449, AWM-style workflow memory). The experience store answers
// "what happened in similar cases"; this answers "which multi-tool sequences
// does this user's actual usage keep repeating" - so when a prefix matches
// the current plan, the statistically dominant continuation can be treated
// as the established next step instead of re-derived from scratch.
// Read-only; mining is lexical n-gram counting over session JSONL (no LLM).
type RecallToolFlowTool struct {
	// sessionsDir overrides the default ~/.ggcode/sessions location
	// (injected by tests). Empty = resolve at Execute time.
	sessionsDir string
}

// NewRecallToolFlowTool creates a recall_toolflow tool. Read-only.
func NewRecallToolFlowTool() *RecallToolFlowTool {
	return &RecallToolFlowTool{}
}

// NewRecallToolFlowToolDir creates a recall_toolflow tool bound to an
// explicit sessions directory (test seam).
func NewRecallToolFlowToolDir(dir string) *RecallToolFlowTool {
	return &RecallToolFlowTool{sessionsDir: dir}
}

func (t *RecallToolFlowTool) Name() string { return "recall_toolflow" }

func (t *RecallToolFlowTool) Description() string {
	return "Mine recurring tool workflows (prefix -> dominant next step) from the user's recent sessions, ranked by how often each sequence repeats (AWM-style workflow memory). Use when planning multi-step work: if a high-confidence pattern's prefix matches your intended steps, its continuation is the user's established playbook order - follow it unless the task demands otherwise. Read-only statistical mining (support >= 5 sessions-occurrences, confidence >= 0.7). Empty result means no recurring workflow yet, not an error."
}

func (t *RecallToolFlowTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
			"type": "object",
			"properties": {
				"max_patterns": {
					"type": "integer",
					"description": "Max workflows to return (default 5, cap 10)."
				}
			}
		}`)
}

func (t *RecallToolFlowTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var params struct {
		MaxPatterns int `json:"max_patterns"`
	}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &params); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
		}
	}
	max := params.MaxPatterns
	if max == 0 {
		max = recallToolFlowDefaultMax
	}
	if max < 1 || max > recallToolFlowMaxMax {
		return Result{IsError: true, Content: fmt.Sprintf("invalid max_patterns=%d: must be between 1 and %d", max, recallToolFlowMaxMax)}, nil
	}

	dir := t.sessionsDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Result{Content: "cannot locate home directory for session storage; nothing to mine."}, nil
		}
		dir = filepath.Join(home, ".ggcode", "sessions")
	}
	pats, err := memory.AnalyzeToolFlows(dir, max)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tool flow mining failed: %v", err)}, nil
	}
	block := memory.FormatToolFlowPatterns(pats, time.Now())
	if block == "" {
		return Result{Content: "no recurring tool workflow above the support/confidence thresholds found in recent sessions."}, nil
	}
	return Result{Content: block}, nil
}
