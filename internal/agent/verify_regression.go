package agent

// Verification Regression Detection — Cross-run error transition tracking
//
// Research basis: "Agent self-correction has 3 distinct failure modes"
// (rotation-frontier-papers-2026-07-14.md, citing AgentMarketCap 2026):
//
//  1. False convergence: agent thinks it fixed the problem but only suppressed
//     the symptom.
//  2. Correction-induced regression: fix for error A introduces error B.
//     The correction loop oscillates indefinitely without holistic diff analysis.
//  3. Context collapse on long repair chains: after 8-10 corrections, models
//     lose original task context.
//
// This module directly addresses failure mode #2. Existing ggcode systems:
//
//   - recurring_error.go: detects when the SAME error persists across edit cycles
//     (root-cause detection). But it cannot tell when a fix INTRODUCES a new error.
//   - diagnostic_baseline.go: captures per-file LSP diagnostics before/after a
//     single edit. But it doesn't track build/test errors across iterations.
//   - verify.go: runs build/test and injects errors as a flat list. No comparison
//     with previous runs.
//
// The gap: when verification fails, the agent sees ALL current errors but has no
// idea which ones are NEWLY INTRODUCED by its recent edits vs. pre-existing.
// This means:
//
//   - Agent fixes error A → introduces error B → sees "build failed" with B
//   - Agent doesn't realize B is NEW (caused by its fix for A)
//   - Agent may fix B in a way that reintroduces A → oscillation
//   - Agent wastes iterations without understanding the causal chain
//
// Solution: track individual error fingerprints across verification runs and
// categorize each error as NEW (regression), RESOLVED (progress), or PERSISTENT
// (still broken). This gives the agent actionable intelligence:
//
//   - NEW errors: highest priority — these are regressions caused by recent edits
//   - RESOLVED errors: positive signal — the approach is working
//   - PERSISTENT errors: may need a different strategy
//
// All operations are pure computation (normalization + set operations) with zero
// LLM cost and zero blocking I/O.

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// maxRegressionErrors limits how many errors per category we report.
	// This prevents context flooding when a large refactor touches many files.
	maxRegressionErrors = 5

	// maxResolvedReport limits how many resolved errors we mention (positive
	// reinforcement is useful but shouldn't dominate the context).
	maxResolvedReport = 3
)

// verifyRegressionState tracks build/test error fingerprints across verification
// runs to detect correction-induced regressions.
type verifyRegressionState struct {
	// prevErrors is the set of normalized error fingerprints from the previous
	// verification run. Empty on first run.
	prevErrors map[string]bool

	// hasBaseline is true after the first verification run completes.
	// Before this, all errors are "pre-existing" and we don't categorize.
	hasBaseline bool
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

// classifyErrors takes the current verification errors and returns an annotated
// summary that categorizes each error relative to the previous run.
//
// The returned string replaces the flat error list in the verification failure
// message injected into the agent's context. When no baseline exists (first run),
// it returns empty string and simply records the baseline.
func (v *verifyRegressionState) classifyErrors(errors []string) string {
	if v == nil {
		return ""
	}
	if len(errors) == 0 {
		// Verification passed — reset to "no baseline" so the next failure
		// starts fresh rather than comparing against a stale empty set.
		v.prevErrors = make(map[string]bool)
		v.hasBaseline = false
		return ""
	}

	// Fingerprint each current error individually.
	currentFPs := make(map[string]string, len(errors)) // fingerprint → original text
	currentSet := make(map[string]bool, len(errors))
	for _, e := range errors {
		fp := fingerprintSingleError(e)
		if fp == "" {
			// Use the raw text as its own fingerprint if normalization fails.
			fp = normalizeForFP(e)
		}
		currentFPs[fp] = e
		currentSet[fp] = true
	}

	// First run — no baseline to compare against.
	if !v.hasBaseline {
		v.prevErrors = currentSet
		v.hasBaseline = true
		debug.Log("verify-regression", "baseline established: %d errors", len(currentSet))
		return ""
	}

	// Categorize each error.
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

	// Update baseline for next run.
	v.prevErrors = currentSet

	// Build the annotated summary.
	summary := buildRegressionSummary(newErrors, persistentErrors, resolvedCount)

	if len(newErrors) > 0 {
		debug.Log("verify-regression", "%d NEW errors (regressions), %d persistent, %d resolved",
			len(newErrors), len(persistentErrors), resolvedCount)
	}

	return summary
}

// buildRegressionSummary constructs the human-readable annotation injected into
// the agent's context alongside the error list.
func buildRegressionSummary(newErrors, persistentErrors []string, resolvedCount int) string {
	// Only annotate when there's something meaningful to say.
	if len(newErrors) == 0 && resolvedCount == 0 {
		// All errors are persistent — no regression, no progress.
		// Brief note so the agent knows these are pre-existing.
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

// fingerprintSingleError normalizes a single error line into a stable fingerprint.
// Reuses the normalization logic from recurring_error.go (normalizeErrorLine)
// but operates on individual error strings rather than the full build output.
func fingerprintSingleError(errorLine string) string {
	normalized := normalizeErrorLine(errorLine)
	if normalized == "" {
		return ""
	}
	return normalized
}

// normalizeForFP is a fallback when normalizeErrorLine produces empty output.
// It lowercases and collapses whitespace to produce a minimal stable key.
func normalizeForFP(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = wsPattern.ReplaceAllString(s, " ")
	return s
}
