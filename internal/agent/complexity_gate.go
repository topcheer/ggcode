package agent

// Post-Completion Complexity Regression Gate
//
// Research basis: Modern AI coding agents (Devin's SICA, Cursor's code quality
// checks, Claude Code's "check your work") all perform some form of quality
// review before presenting results. However, the specific gap in ggcode is:
//
// The codehealth.Analyze() function exists as a user-callable tool but is NEVER
// automatically invoked during the agent's completion flow. The agent can edit
// a Go file and introduce functions with cyclomatic complexity > 20, deep
// nesting (> 5 levels), or excessive length (> 120 lines) - and nobody notices
// until a human reviews the code or runs the code_health tool manually.
//
// Existing gates do NOT cover this:
//   - syncVerify: checks compilation (build/test) - complex code compiles fine
//   - verify_lint: checks style (go vet) - vet doesn't flag complexity
//   - fulfillment_gate: checks request-vs-work matching - not code quality
//   - placeholder_check: checks for stubs - not overall complexity
//   - confidence scorer: tracks trajectory confidence - not code-level metrics
//
// This gate fills the gap by running codehealth.Analyze on edited .go files
// after build verification passes, and injecting targeted warnings for functions
// that exceed critical quality thresholds. The gate is:
//   - Zero-LLM-cost: uses deterministic AST analysis (go/parser + go/ast)
//   - Non-blocking: advisory warnings, doesn't prevent completion
//   - Scoped: only scans files the agent actually edited
//   - Diff-baselined (#1202): compares each hotspot against its metrics in the
//     last committed revision (git HEAD). Pre-existing legacy hotspots the agent
//     did not worsen are NOT reported - this prevents scope creep where a
//     one-line edit in a legacy file steers the agent into refactoring
//     unrelated pre-existing code. When no baseline is available (not a git
//     repo), the gate falls back to reporting all hotspots.
//   - Per-function dedup (#1202): already-reported hotspots are remembered by
//     file+function key, so the same advisory text is never re-injected, while
//     a NEW regression in a different file/function still fires later in the
//     session (the old global one-shot fired flag suppressed everything after
//     the first advisory). Bounded by maxComplexityGateFires per session.
//   - Threshold-based: only flags CRITICAL severity (complexity > 20, or
//     extreme length/nesting). Legacy files full of complexity-15 functions
//     must not trigger advisories that steer the agent into refactoring
//     unrelated pre-existing code (scope creep).

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/codehealth"
	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// maxComplexityGateWarnings caps the number of functions reported per
	// advisory to avoid flooding the agent's context with excessive output.
	maxComplexityGateWarnings = 5

	// maxComplexityGateFires caps how many separate advisories the gate may
	// inject over the whole session (#1202: per-function dedup replaced the
	// global fired flag; this cap keeps the total bounded).
	maxComplexityGateFires = 3

	// complexityGateThreshold is the minimum cyclomatic complexity that triggers
	// a warning. Set to 20 - codehealth "high" severity - so only genuinely
	// problematic functions are flagged. 15 ("medium") fired on ordinary
	// business logic and made the advisory near-constant in legacy files.
	complexityGateThreshold = 20

	// complexityGateMaxLength flags functions exceeding this line count.
	// 120 keeps normal long-but-linear functions (pervasive in this codebase)
	// out of the advisory path; only true monsters trigger.
	complexityGateMaxLength = 120

	// complexityGateMaxNesting flags functions exceeding this nesting depth.
	complexityGateMaxNesting = 5

	// complexityGateAnalyzeThreshold and complexityGateAnalyzeMaxFuncs control
	// the codehealth.Analyze call. The analyze threshold is set to 1 (and the
	// function cap very high) so TopFunctions returns ALL functions, letting
	// isComplexityHotspot apply the real three-threshold check below (#1202:
	// with the analyze threshold equal to the complexity threshold, the length
	// and nesting branches were dead code - buildReport pre-filters
	// TopFunctions by complexity before they could ever match).
	complexityGateAnalyzeThreshold = 1
	complexityGateAnalyzeMaxFuncs  = 1000
)

// complexityGateState dedupes already-reported hotspots. Deliberately
// SESSION-scoped: hotspots are remembered by file+function key for the agent's
// lifetime, so identical advisories are never re-injected, while genuinely new
// regressions elsewhere still fire (#1202).
type complexityGateState struct {
	// reported maps relPath + ":" + funcName of hotspots already named in a
	// previous advisory.
	reported map[string]bool
	// fires counts advisories injected this session.
	fires int
}

func newComplexityGateState() *complexityGateState {
	return &complexityGateState{}
}

// complexityBaselineFn resolves the pre-edit baseline metrics for a file.
// Returns the HEAD-revision function metrics keyed by function name and
// whether a baseline is available at all. Package-level so tests can inject.
var complexityBaselineFn = gitBaselineMetrics

// checkComplexityGate runs after build verification passes to detect quality
// regressions the agent introduced in edited Go files. Returns a non-empty
// message if critical complexity issues are found that warrant a quality
// advisory.
//
// The gate analyzes ONLY files in runStats.FilesEdited that have .go extension
// (excluding tests and generated files), ONLY reports functions exceeding
// severity thresholds, and only reports functions that are new or worsened
// relative to the git HEAD baseline. Each hotspot is reported at most once per
// session; the advisory count is capped at maxComplexityGateFires.
func (a *Agent) checkComplexityGate(runStats *RunStats) string {
	if a.complexityGate.fires >= maxComplexityGateFires {
		return ""
	}

	goFiles := filterGoSourceFiles(runStats.FilesEdited)
	if len(goFiles) == 0 {
		return ""
	}

	workingDir := a.WorkingDir()

	var allHotspots []codehealth.FuncMetrics
	var newKeys []string
	for _, relPath := range goFiles {
		absPath := relPath
		if !filepath.IsAbs(absPath) && workingDir != "" {
			absPath = filepath.Join(workingDir, relPath)
		}

		// Generated code is out of scope (#1202): advisories on generator
		// output (".pb.go", "Code generated" markers) cannot be acted on by
		// the agent and only add noise.
		if codehealth.IsGenerated(absPath) {
			debug.Log("complexity-gate", "skipping generated file %s", relPath)
			continue
		}

		// Analyze the single file - codehealth.Analyze supports file paths.
		opts := codehealth.DefaultOptions()
		opts.ThresholdComplexity = complexityGateAnalyzeThreshold
		opts.MaxFunctions = complexityGateAnalyzeMaxFuncs
		report, err := codehealth.Analyze(absPath, opts)
		if err != nil {
			debug.Log("complexity-gate", "failed to analyze %s: %v", relPath, err)
			continue
		}
		if report == nil {
			continue
		}

		// Resolve the pre-edit baseline: hotspots the agent did not introduce
		// or worsen must not be reported (scope-creep misattribution, #1202).
		baseline, hasBaseline := complexityBaselineFn(absPath, workingDir)

		for _, fn := range report.TopFunctions {
			if !isComplexityHotspot(fn) {
				continue
			}
			if hasBaseline {
				if base, ok := baseline[fn.Function]; ok && !hotspotWorsened(fn, base) {
					// Pre-existing legacy hotspot, unchanged by the agent.
					continue
				}
			}
			key := relPath + ":" + fn.Function
			if a.complexityGate.reported[key] {
				continue
			}
			// Use relative path for cleaner output.
			fn.File = relPath
			allHotspots = append(allHotspots, fn)
			newKeys = append(newKeys, key)
		}
	}

	if len(allHotspots) == 0 {
		debug.Log("complexity-gate", "passed: no new critical complexity in %d edited Go file(s)", len(goFiles))
		return ""
	}

	if a.complexityGate.reported == nil {
		a.complexityGate.reported = make(map[string]bool)
	}
	for _, key := range newKeys {
		a.complexityGate.reported[key] = true
	}
	a.complexityGate.fires++

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
	b.WriteString("\nThese are advisory - refactoring is recommended but not required for completion.")

	debug.Log("complexity-gate", "fired: %d hotspot(s) in %d file(s)", len(allHotspots), len(goFiles))
	return b.String()
}

// hotspotWorsened reports whether fn exceeds base on any threshold dimension.
// Equal-or-better metrics mean the agent did not introduce a regression.
func hotspotWorsened(fn, base codehealth.FuncMetrics) bool {
	return fn.Complexity > base.Complexity ||
		fn.Length > base.Length ||
		fn.NestingDepth > base.NestingDepth
}

// gitBaselineMetrics returns the function metrics of the file's last committed
// revision (git HEAD), keyed by function name. The second return is false when
// no baseline can be established (not a git repository, or git failure) - in
// that case the caller falls back to reporting all hotspots.
//
// A file present on disk but not in HEAD (newly created, untracked) yields an
// empty baseline with available=true: every function in it counts as new.
func gitBaselineMetrics(absPath, workingDir string) (map[string]codehealth.FuncMetrics, bool) {
	if workingDir == "" {
		return nil, false
	}
	out, err := exec.Command("git", "-C", workingDir, "rev-parse", "--is-inside-work-tree").Output()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		debug.Log("complexity-gate", "no git baseline for %s (not a repo: %v)", absPath, err)
		return nil, false
	}

	rel := absPath
	if r, rerr := filepath.Rel(workingDir, absPath); rerr == nil && !strings.HasPrefix(r, "..") {
		rel = r
	}
	// The "./" prefix makes git resolve the path relative to the -C directory
	// rather than the repository root.
	spec := "HEAD:./" + filepath.ToSlash(rel)
	var stderr bytes.Buffer
	cmd := exec.Command("git", "-C", workingDir, "show", spec)
	cmd.Stderr = &stderr
	content, err := cmd.Output()
	if err != nil {
		msg := stderr.String()
		if strings.Contains(msg, "does not exist in") || strings.Contains(msg, "exists on disk, but not in") {
			// Untracked/new file: empty baseline, everything is new.
			return map[string]codehealth.FuncMetrics{}, true
		}
		debug.Log("complexity-gate", "baseline unavailable for %s: %v (%s)", rel, err, strings.TrimSpace(msg))
		return nil, false
	}

	report, err := codehealth.AnalyzeSource(rel, content, codehealth.Options{
		ThresholdComplexity: complexityGateAnalyzeThreshold,
		MaxFunctions:        complexityGateAnalyzeMaxFuncs,
	})
	if err != nil {
		debug.Log("complexity-gate", "failed to parse HEAD revision of %s: %v", rel, err)
		return nil, false
	}
	baseline := make(map[string]codehealth.FuncMetrics, len(report.TopFunctions))
	for _, fn := range report.TopFunctions {
		baseline[fn.Function] = fn
	}
	return baseline, true
}

// isComplexityHotspot returns true if a function exceeds any quality threshold.
func isComplexityHotspot(fn codehealth.FuncMetrics) bool {
	return fn.Complexity >= complexityGateThreshold ||
		fn.Length > complexityGateMaxLength ||
		fn.NestingDepth > complexityGateMaxNesting
}

// filterGoSourceFiles returns paths from the list that are .go files, excluding
// test files (_test.go). Generated files are excluded separately in
// checkComplexityGate once an absolute path (and therefore filesystem access)
// is available.
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
