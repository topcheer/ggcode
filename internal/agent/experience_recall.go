package agent

import (
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
