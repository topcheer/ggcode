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
// nothing matches - callers skip injection entirely so cold projects pay
// nothing.
//
// #3072: injected case IDs are recorded on the agent so the decision-time
// recall can exclude them (a re-query at failure time overlaps heavily with
// the run-start query and would duplicate the same cases mid-run).
func (a *Agent) recallExperience(task string) string {
	workingDir := a.WorkingDir()
	if workingDir == "" {
		return ""
	}
	store := memory.NewProjectExperienceStore(workingDir)
	if store == nil {
		return ""
	}
	// FormatIndexTracked returns "" when nothing is relevant; the empty
	// result is the caller's skip-injection signal (no error channel needed).
	idx, ids := store.FormatIndexTracked(task, experienceRecallTopK)
	if len(ids) > 0 {
		a.experienceInjectedCaseIDs = append(a.experienceInjectedCaseIDs, ids...)
	}
	return idx
}

// experienceFailureRecallTopK stays leaner than the run-start 3: the
// decision-time block rides an already-long mid-run context, and one or two
// on-point cases are all the guidance a failing agent can absorb.
const experienceFailureRecallTopK = 2

// experienceFailureQueryMaxRunes caps how much error text feeds the query.
// Go build/test output front-loads package paths and === RUN noise while the
// actionable error sits at the END, so the budget is split: a small head
// slice for the failing command line plus a larger tail slice for the real
// error lines (#3072 - head-only truncation biased retrieval to noise).
const (
	experienceFailureQueryHeadRunes = 80
	experienceFailureQueryTailRunes = 160
)

// truncateErrorForQuery keeps the head and tail of long error output within
// the combined budget, preferring the tail (where Go errors concentrate).
func truncateErrorForQuery(s string) string {
	r := []rune(s)
	total := experienceFailureQueryHeadRunes + experienceFailureQueryTailRunes
	if len(r) <= total {
		return s
	}
	return string(r[:experienceFailureQueryHeadRunes]) + "\n...\n" + string(r[len(r)-experienceFailureQueryTailRunes:])
}

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
	errContent = truncateErrorForQuery(errContent)
	query := task
	if strings.TrimSpace(errContent) != "" {
		query = task + "\n" + errContent
	}
	// #3072 V2: exclude cases already injected at run-start - the query
	// prefixes the full task, so without exclusion the lexical retrieval
	// would mostly re-surface the same top hits a second time in the same
	// run.
	var exclude map[string]bool
	if len(a.experienceInjectedCaseIDs) > 0 {
		exclude = make(map[string]bool, len(a.experienceInjectedCaseIDs))
		for _, id := range a.experienceInjectedCaseIDs {
			exclude[id] = true
		}
	}
	idx := store.FormatIndexExcluding(query, experienceFailureRecallTopK, exclude)
	if idx == "" {
		return ""
	}
	a.experienceFailureRecallFired = true
	// #3072 V1: the retrieval is lexical over task+error text - a hit is a
	// keyword match, NOT a confirmed "same failure pattern". Word it
	// conditionally so the model verifies relevance instead of trusting the
	// assertion and misdirecting error recovery.
	return "## Possibly Related Past Experience (decision-time recall)\nRetrieved by task/error keyword match - a similar failure pattern MAY have been seen in this project before. Check whether it actually applies to the current failure:\n" + idx
}
