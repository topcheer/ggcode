package agent

import (
	"strings"

	"github.com/topcheer/ggcode/internal/memory"
)

const experienceRecallTopK = 3

func (a *Agent) recallExperience(task string) string {
	workingDir := a.WorkingDir()
	if workingDir == "" {
		return ""
	}
	store := memory.NewProjectExperienceStore(workingDir)
	if store == nil {
		return ""
	}

	idx, ids := store.FormatIndexTracked(task, experienceRecallTopK)
	if len(ids) > 0 {
		a.experienceInjectedCaseIDs = append(a.experienceInjectedCaseIDs, ids...)
	}
	return idx
}

const experienceFailureRecallTopK = 2

const (
	experienceFailureQueryHeadRunes = 80
	experienceFailureQueryTailRunes = 160
)

func truncateErrorForQuery(s string) string {
	r := []rune(s)
	total := experienceFailureQueryHeadRunes + experienceFailureQueryTailRunes
	if len(r) <= total {
		return s
	}
	return string(r[:experienceFailureQueryHeadRunes]) + "\n...\n" + string(r[len(r)-experienceFailureQueryTailRunes:])
}

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

	return "## Possibly Related Past Experience (decision-time recall)\nRetrieved by task/error keyword match - a similar failure pattern MAY have been seen in this project before. Check whether it actually applies to the current failure:\n" + idx
}
