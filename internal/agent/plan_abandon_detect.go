package agent

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	planAbandonMinSteps = 3

	planAbandonMaxWarnings = 2

	planAbandonMaxStepsShown = 4
)

var planStepRe = regexp.MustCompile(`(?im)^\s*\d+[.)]\s+(.+)`)

var planFutureVerbRe = regexp.MustCompile(`(?i)(?:i(?:'ll|\s+will|\s+won't)(?:\s+need\s+to)?\s+|i\s+need\s+to\s+|i'm\s+going\s+to\s+|i\s+am\s+going\s+to\s+|next,?\s*i(?:'ll|\s+will)?\s+|then\s+i(?:'ll|\s+will)?\s+|finally,?\s*i(?:'ll|\s+will)?\s+|i\s+plan\s+to\s+|let\s+me\s+(?:also\s+)?(?:read|fix|update|check|run|create|add|remove|modify|verify|test|write|implement|refactor))`)

var planCompletionRe = regexp.MustCompile(`(?i)(?:^|[\n.])\s*(?:the\s+)?(?:task|work|fix|change|update|implementation|feature|bug)\s+(?:(?:is|has\s+been)\s+)?(?:now\s+)?(?:done|complete|finished|ready|applied|implemented|resolved)|^done[.!]|all\s+done|finished\s+(?:the\s+)?(?:task|work|fix|implementation)|that\s+(?:should\s+\w*\s*it|wraps?\s+up|completes?)|this\s+completes`)

var planStepEditVerbRe = regexp.MustCompile(`(?i)\b(?:fix(?:es|ed)?|updat(?:e|es|ed)|cre(?:ate|ates|ated)|add(?:s|ed)?|remov(?:e|es|ed)|modif(?:y|ies|ied)|writ(?:e|es|ing)|implement(?:s|ed)?|refactor(?:s|ed)?|renam(?:e|es|ed)|delet(?:e|es|ed)|patch(?:es|ed)?)\b`)

var planStepRunVerbRe = regexp.MustCompile(`(?i)\b(?:run(?:s|ning)?|ra\b|ran\b|test(?:s|ed|ing)?|verif(?:y|ies|ied)|build(?:s|ing)?|built\b|buil[dt]|check(?:s|ed|ing)?|execut(?:e|es|ed))\b`)

func planStepExcerpt(line string) string {
	line = strings.TrimSpace(line)
	if len(line) > 60 {
		return line[:60] + "..."
	}
	return line
}

func extractFuturePlanSteps(text string) []string {
	if len(text) == 0 {
		return nil
	}

	matches := planStepRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}

	var steps []string
	seen := make(map[string]bool)
	for _, m := range matches {
		line := strings.TrimSpace(m[1])
		if len(line) == 0 {
			continue
		}
		if !planFutureVerbRe.MatchString(line) {
			continue
		}
		excerpt := planStepExcerpt(line)
		if seen[excerpt] {
			continue
		}
		seen[excerpt] = true
		steps = append(steps, excerpt)
	}

	return steps
}

func hasCompletionSignal(text string) bool {
	if len(text) == 0 {
		return false
	}
	return planCompletionRe.MatchString(text)
}

type planAbandonState struct {
	declaredSteps []string
	planIter      int
	warnings      int
	curIter       int
}

func newPlanAbandonState() *planAbandonState {
	return &planAbandonState{planIter: -1}
}

func (s *planAbandonState) reset() {
	s.declaredSteps = nil
	s.planIter = -1
	s.warnings = 0
	s.curIter = 0
}

func planHasExecutionGap(steps []string, rs *RunStats) bool {
	if rs == nil {
		return true
	}
	hasEditStep := false
	hasRunStep := false
	for _, stp := range steps {
		if planStepEditVerbRe.MatchString(stp) {
			hasEditStep = true
		}
		if planStepRunVerbRe.MatchString(stp) {
			hasRunStep = true
		}
	}
	if !hasEditStep && !hasRunStep {

		return false
	}
	gap := false
	if hasEditStep && len(rs.FilesEdited) == 0 {
		gap = true
	}
	if hasRunStep && len(rs.CommandsRun) == 0 {
		gap = true
	}
	return gap
}

func (a *Agent) maybeWarnPlanAbandon(assistantText string, runStats *RunStats) string {
	if a.planAbandon == nil {
		return ""
	}
	if a.planAbandon.warnings >= planAbandonMaxWarnings {
		return ""
	}

	s := a.planAbandon
	s.curIter++

	newSteps := extractFuturePlanSteps(assistantText)
	if len(newSteps) >= planAbandonMinSteps {
		s.declaredSteps = newSteps
		s.planIter = s.curIter
	}

	if len(s.declaredSteps) < planAbandonMinSteps || s.planIter < 0 {
		return ""
	}

	if s.curIter <= s.planIter {
		return ""
	}

	if !hasCompletionSignal(assistantText) {
		return ""
	}

	if !planHasExecutionGap(s.declaredSteps, runStats) {
		return ""
	}

	stepCount := len(s.declaredSteps)
	s.warnings++

	var shown []string
	for si, stp := range s.declaredSteps {
		if si >= planAbandonMaxStepsShown {
			break
		}
		shown = append(shown, fmt.Sprintf("  %d. %s", si+1, stp))
	}
	if stepCount > planAbandonMaxStepsShown {
		shown = append(shown, fmt.Sprintf("  ... and %d more", stepCount-planAbandonMaxStepsShown))
	}

	s.declaredSteps = nil
	s.planIter = -1

	hint := "[plan-abandon] You declared " + fmt.Sprintf("%d", stepCount) +
		" plan step(s) in a prior turn and are now " +
		"claiming completion. Verify that ALL declared steps were actually executed " +
		"- agents frequently abandon plan steps (e.g., 'update all callers', " +
		"'run the tests', 'add error handling') while claiming the task is done. " +
		"If any step was skipped, complete it now or explicitly tell the user " +
		"what remains.\n" +
		"Declared steps:\n" + strings.Join(shown, "\n")

	return hint
}
