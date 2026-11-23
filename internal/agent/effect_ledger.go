package agent

import (
	"encoding/json"
	"strings"

	"github.com/topcheer/ggcode/internal/tool"
)

// Effect classification for run_command results. The ledger used to also
// inject a "[Effect Ledger] this exact command already ran..." hint on
// retries; that guidance injection was removed as noise, so what remains is
// the classification plus the build-idempotency side effect.
type effectOutcome int

const (
	effectFailed effectOutcome = iota
	effectUncertain
)

var neverExecutedMarkers = []string{
	"Permission denied for tool",
	"invalid input:",
	"Command blocked:",
	"failed to resolve shell",
	"command job manager not available",
	"failed to start command job",
}

var uncertainMarkers = []string{
	"timed out after",
	"context deadline exceeded",
	"signal: killed",
	"context canceled",
}

func classifyEffectOutcome(res tool.Result) (effectOutcome, bool) {
	if !res.IsError {
		return 0, false
	}
	c := res.Content
	for _, m := range neverExecutedMarkers {
		if strings.Contains(c, m) {
			return 0, false
		}
	}
	for _, m := range uncertainMarkers {
		if strings.Contains(c, m) {
			return effectUncertain, true
		}
	}
	return effectFailed, true
}

// recordEffectAttempt classifies a run_command result and clears the
// build-idempotency cache when a build/test command actually failed. It never
// returns guidance text.
func (a *Agent) recordEffectAttempt(name string, args []byte, res tool.Result) {
	if name != "run_command" {
		return
	}
	var parsed struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil || parsed.Command == "" {
		return
	}
	outcome, executed := classifyEffectOutcome(res)
	if executed && outcome == effectFailed {
		a.buildIdempot.markBuildFailed(parsed.Command)
	}
}
