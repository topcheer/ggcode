package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	maxRegressionErrors = 5

	maxResolvedReport = 3
)

type verifyRegressionState struct {
	prevErrors map[string]bool

	hasBaseline bool

	prevScope string
}

func newVerifyRegressionState() *verifyRegressionState {
	return &verifyRegressionState{
		prevErrors: make(map[string]bool),
	}
}

func (v *verifyRegressionState) reset() {
	if v == nil {
		return
	}
	v.prevErrors = make(map[string]bool)
	v.hasBaseline = false
}

type errorTransition struct {
	newErrors        []string
	persistentErrors []string
	resolvedCount    int
}

func (v *verifyRegressionState) classifyErrors(errors []string) string {
	_, summary := v.classifyErrorsWithTransition(errors, "")
	return summary
}

func (v *verifyRegressionState) classifyErrorsWithTransition(errors []string, scope string) (errorTransition, string) {
	var tr errorTransition
	if v == nil {
		return tr, ""
	}

	if v.hasBaseline && scope != v.prevScope {
		debug.Log("verify", "regression baseline reseeded: scope changed (%q -> %q)", v.prevScope, scope)
		v.prevErrors = make(map[string]bool)
		v.hasBaseline = false
	}
	v.prevScope = scope
	if len(errors) == 0 {

		v.prevErrors = make(map[string]bool)
		v.hasBaseline = false
		return tr, ""
	}

	currentFPs := make(map[string]string, len(errors))
	currentSet := make(map[string]bool, len(errors))
	for _, e := range errors {
		fp := fingerprintSingleError(e)
		if fp == "" {

			fp = normalizeForFP(e)
		}
		currentFPs[fp] = e
		currentSet[fp] = true
	}

	if !v.hasBaseline {
		v.prevErrors = currentSet
		v.hasBaseline = true
		debug.Log("verify-regression", "baseline established: %d errors", len(currentSet))
		return tr, ""
	}

	var newErrors, persistentErrors []string
	resolvedCount := 0

	for fp, text := range currentFPs {
		if v.prevErrors[fp] {
			persistentErrors = append(persistentErrors, text)
		} else {
			newErrors = append(newErrors, text)
		}
	}

	for fp := range v.prevErrors {
		if !currentSet[fp] {
			resolvedCount++
		}
	}

	v.prevErrors = currentSet

	summary := buildRegressionSummary(newErrors, persistentErrors, resolvedCount)

	if len(newErrors) > 0 {
		debug.Log("verify-regression", "%d NEW errors (regressions), %d persistent, %d resolved",
			len(newErrors), len(persistentErrors), resolvedCount)
	}

	tr = errorTransition{
		newErrors:        newErrors,
		persistentErrors: persistentErrors,
		resolvedCount:    resolvedCount,
	}
	return tr, summary
}

func buildRegressionSummary(newErrors, persistentErrors []string, resolvedCount int) string {

	if len(newErrors) == 0 && resolvedCount == 0 {

		if len(persistentErrors) > 0 {
			return fmt.Sprintf("\n\n[PERSISTENT] All %d current error(s) are unchanged from the previous verification run. Your edits have not addressed them — consider a different approach.\n", len(persistentErrors))
		}
		return ""
	}

	var b strings.Builder
	b.WriteString("\n\n--- Verification Diff (vs previous run) ---\n")

	if len(newErrors) > 0 {
		b.WriteString(fmt.Sprintf("[REGRESSION] %d NEW error(s) introduced by your recent edits — fix these FIRST:\n", len(newErrors)))
		for i, e := range newErrors {
			if i >= maxRegressionErrors {
				b.WriteString(fmt.Sprintf("  ... and %d more new error(s)\n", len(newErrors)-maxRegressionErrors))
				break
			}
			b.WriteString(fmt.Sprintf("  [NEW] %s\n", e))
		}
	}

	if resolvedCount > 0 {
		b.WriteString(fmt.Sprintf("[PROGRESS] %d error(s) from the previous run are now RESOLVED — your fix is working for those.\n", resolvedCount))
	}

	if len(persistentErrors) > 0 {
		b.WriteString(fmt.Sprintf("[PERSISTENT] %d error(s) remain unfixed from before:\n", len(persistentErrors)))
		for i, e := range persistentErrors {
			if i >= maxRegressionErrors {
				b.WriteString(fmt.Sprintf("  ... and %d more persistent error(s)\n", len(persistentErrors)-maxRegressionErrors))
				break
			}
			b.WriteString(fmt.Sprintf("  [STILL] %s\n", e))
		}
	}

	b.WriteString("---\n")
	return b.String()
}

func fingerprintSingleError(errorLine string) string {
	normalized := normalizeErrorLine(errorLine)
	if normalized == "" {
		return ""
	}
	return normalized
}

func normalizeForFP(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = wsPattern.ReplaceAllString(s, " ")
	return s
}
