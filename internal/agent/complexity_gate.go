package agent

// Post-Completion Complexity Regression Gate
//
// Research basis: Modern AI coding agents (Devin's SICA, Cursor's code quality
// checks, Claude Code's "check your work") all perform some form of quality
// review before presenting results. However, the specific gap in ggcode is:
//
// The codehealth.Analyze() function exists as a user-callable tool but is NEVER
// automatically invoked during the agent's completion flow. The agent can edit
// a Go file and introduce functions with cyclomatic complexity > 30, deep
// nesting (> 5 levels), or excessive length (> 100 lines) — and nobody notices
// until a human reviews the code or runs the code_health tool manually.
//
// Existing gates do NOT cover this:
//   - syncVerify: checks compilation (build/test) — complex code compiles fine
//   - verify_lint: checks style (go vet) — vet doesn't flag complexity
//   - fulfillment_gate: checks request-vs-work matching — not code quality
//   - placeholder_check: checks for stubs — not overall complexity
//   - confidence scorer: tracks trajectory confidence — not code-level metrics
//
// This gate fills the gap by running codehealth.Analyze on edited .go files
// after build verification passes, and injecting targeted warnings for functions
// that exceed critical quality thresholds. The gate is:
//   - Zero-LLM-cost: uses deterministic AST analysis (go/parser + go/ast)
//   - Non-blocking: advisory warnings, doesn't prevent completion
//   - Scoped: only scans files the agent actually edited
//   - Bounded: fires at most once per run, caps reported functions at 5
//   - Threshold-based: only flags CRITICAL severity (complexity > 20)

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/codehealth"
	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// maxComplexityGateWarnings caps the number of functions reported to avoid
	// flooding the agent's context with excessive output.
	maxComplexityGateWarnings = 5

	// complexityGateThreshold is the minimum cyclomatic complexity that triggers
	// a warning. Set to 15 — this catches genuinely problematic functions
	// (codehealth "medium" severity starts at 11, "high" at 20) while avoiding
	// false positives on moderately complex business logic.
	complexityGateThreshold = 15

	// complexityGateMaxLength flags functions exceeding this line count.
	complexityGateMaxLength = 80

	// complexityGateMaxNesting flags functions exceeding this nesting depth.
	complexityGateMaxNesting = 5
)

// complexityGateState tracks whether the gate has already fired this run.
type complexityGateState struct {
	fired bool
}

func newComplexityGateState() *complexityGateState {
	return &complexityGateState{}
}

func (g *complexityGateState) reset() {
	g.fired = false
}

// checkComplexityGate runs after build verification passes to detect quality
// regressions in edited Go files. Returns a non-empty message if critical
// complexity issues are found that warrant a quality advisory.
//
// The gate analyzes ONLY files in runStats.FilesEdited that have .go extension,
// and ONLY reports functions exceeding severity thresholds. It fires at most
// once per run.
func (a *Agent) checkComplexityGate(runStats *RunStats) string {
	if a.complexityGate.fired {
		return ""
	}

	goFiles := filterGoSourceFiles(runStats.FilesEdited)
	if len(goFiles) == 0 {
		return ""
	}

	workingDir := a.WorkingDir()

	var allHotspots []codehealth.FuncMetrics
	for _, relPath := range goFiles {
		absPath := relPath
		if !filepath.IsAbs(absPath) && workingDir != "" {
			absPath = filepath.Join(workingDir, relPath)
		}

		// Analyze the single file — codehealth.Analyze supports file paths.
		opts := codehealth.DefaultOptions()
		opts.ThresholdComplexity = complexityGateThreshold
		report, err := codehealth.Analyze(absPath, opts)
		if err != nil {
			debug.Log("complexity-gate", "failed to analyze %s: %v", relPath, err)
			continue
		}
		if report == nil {
			continue
		}

		for _, fn := range report.TopFunctions {
			if isComplexityHotspot(fn) {
				// Use relative path for cleaner output.
				fn.File = relPath
				allHotspots = append(allHotspots, fn)
			}
		}
	}

	if len(allHotspots) == 0 {
		debug.Log("complexity-gate", "passed: no critical complexity in %d edited Go file(s)", len(goFiles))
		return ""
	}

	a.complexityGate.fired = true

	// Cap the number of warnings.
	reported := allHotspots
	if len(reported) > maxComplexityGateWarnings {
		reported = reported[:maxComplexityGateWarnings]
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"Code quality advisory: %d function(s) in your edited files have high complexity. "+
			"Consider refactoring before finalizing:\n",
		len(allHotspots),
	))
	for _, fn := range reported {
		parts := []string{fmt.Sprintf("complexity=%d", fn.Complexity)}
		if fn.Length > complexityGateMaxLength {
			parts = append(parts, fmt.Sprintf("length=%d lines", fn.Length))
		}
		if fn.NestingDepth > complexityGateMaxNesting {
			parts = append(parts, fmt.Sprintf("nesting=%d", fn.NestingDepth))
		}
		b.WriteString(fmt.Sprintf("- %s (%s): %s\n", fn.Function, filepath.Base(fn.File), strings.Join(parts, ", ")))
	}
	if len(allHotspots) > len(reported) {
		b.WriteString(fmt.Sprintf("... and %d more\n", len(allHotspots)-len(reported)))
	}
	b.WriteString("\nThese are advisory — refactoring is recommended but not required for completion.")

	debug.Log("complexity-gate", "fired: %d hotspot(s) in %d file(s)", len(allHotspots), len(goFiles))
	return b.String()
}

// isComplexityHotspot returns true if a function exceeds any quality threshold.
func isComplexityHotspot(fn codehealth.FuncMetrics) bool {
	return fn.Complexity >= complexityGateThreshold ||
		fn.Length > complexityGateMaxLength ||
		fn.NestingDepth > complexityGateMaxNesting
}

// filterGoSourceFiles returns paths from the list that are .go files, excluding
// test files (_test.go) and generated files.
func filterGoSourceFiles(paths []string) []string {
	var result []string
	for _, p := range paths {
		if !strings.HasSuffix(p, ".go") {
			continue
		}
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		result = append(result, p)
	}
	return result
}
