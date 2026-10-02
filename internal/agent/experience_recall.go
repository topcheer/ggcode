package agent

import (
	"strings"

	"github.com/topcheer/ggcode/internal/memory"
)

// experienceRecallTopK caps how many past cases are retrieved per run.
// Memento (arXiv:2508.16153) retrieves a handful of cases per query; beyond
// ~5 the marginal guidance decays while prompt cost grows linearly.
const experienceRecallTopK = 3

// recallExperience retrieves the most relevant past experience cases for a
// task query (lexical IDF scoring over the project's case bank) and returns
// a formatted index block, or "" when the store is unavailable, empty, or
// nothing matches — callers skip injection entirely so cold projects pay
// nothing.
func (a *Agent) recallExperience(task string) string {
	workingDir := a.WorkingDir()
	if workingDir == "" {
		return ""
	}
	store := memory.NewProjectExperienceStore(workingDir)
	if store == nil {
		return ""
	}
	// FormatIndex returns "" when nothing is relevant; the empty result is
	// the caller's skip-injection signal (no error channel needed).
	return store.FormatIndex(task, experienceRecallTopK)
}

// experienceFailureRecallTopK stays leaner than the run-start 3: the
// decision-time block rides an already-long mid-run context, and one or two
// on-point cases are all the guidance a failing agent can absorb.
const experienceFailureRecallTopK = 2

// experienceFailureQueryMaxRunes caps how much error text feeds the query —
// error dumps are long and mostly stack noise past the first lines.
const experienceFailureQueryMaxRunes = 240

// maybeRecallExperienceOnFailure is the decision-time half of experience
// recall ("consolidation at decision time", agent-memory survey
// arXiv:2602.06052): when the error-strategy-loop detector fires, the
// failure moment is exactly when "how was this solved last time" helps
// most, yet run-start recall (agent.go run-loop prologue) cannot see the
// error. Queries the case bank with task + error excerpt. One shot per
// run (a.experienceFailureRecallFired); returns "" on repeat, missing
// store, or no match, so the failure path costs nothing on cold projects.
func (a *Agent) maybeRecallExperienceOnFailure(task, errContent string) string {
	if a.experienceFailureRecallFired {
		return ""
	}
	workingDir := a.WorkingDir()
	if workingDir == "" {
		return ""
	}
	store := memory.NewProjectExperienceStore(workingDir)
	if store == nil {
		return ""
	}
	if r := []rune(errContent); len(r) > experienceFailureQueryMaxRunes {
		errContent = string(r[:experienceFailureQueryMaxRunes])
	}
	query := task
	if strings.TrimSpace(errContent) != "" {
		query = task + "\n" + errContent
	}
	idx := store.FormatIndex(query, experienceFailureRecallTopK)
	if idx == "" {
		return ""
	}
	a.experienceFailureRecallFired = true
	return "## Past Experience for This Failure (decision-time recall)\nA similar failure pattern was seen in this project before. How it was resolved last time:\n" + idx
}
