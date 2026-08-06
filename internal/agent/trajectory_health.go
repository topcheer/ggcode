package agent

// Trajectory Health Synthesizer
//
// Research basis:
//   - Xu, Jiexi. "Agentic Metacognition: Designing a Self-Aware Low-Code
//     Agent for Failure Prediction and Human Handoff." arXiv:2509.19783
//     (Sept 2025).
//     Proposes a metacognitive layer that monitors the primary agent's
//     trajectory using multiple signals (repetition, duration/latency,
//     complexity). The key insight: individual signals may stay below
//     their own thresholds, but their accumulation is a powerful predictor
//     of imminent failure.
//   - Cox, M. T. et al. "Computational Metacognition." arXiv:2201.12885
//     (2022).
//     Meta-knowledge about how the agent is performing - not just task
//     knowledge - is essential for self-aware failure prevention.
//   - "Combining Cost-Constrained Runtime Monitors for AI Safety"
//     arXiv:2507.15886 (2025).
//     Multiple lightweight runtime monitors combined outperform single
//     monitors; their joint signal has higher predictive value than any
//     individual component.
//
// Problem: ggcode has 27+ independent detectors, each with its own
// threshold. But a trajectory can be failing without any single detector
// crossing its threshold:
//
//  1. Moderate edit inefficiency (2x retries) + moderate error rate
//     (2 tool failures) + moderate verbosity (1.5x expected tokens) -
//     no individual detector fires, but the trajectory is clearly degrading.
//  2. Each detector fires once at most; the agent receives fragmented
//     guidance but never a holistic "your overall trajectory is unhealthy"
//     signal.
//  3. The agent has no concept of accumulating risk - signals are
//     independent and ephemeral.
//
// Gap: No metacognitive layer synthesizes multiple orthogonal signals
// into a holistic trajectory health assessment. This detector addresses
// that gap by tracking five orthogonal health dimensions and firing
// when their combined degradation exceeds a threshold, even if no
// individual dimension would trigger its own detector.
//
// Design:
//   - Tracks 5 orthogonal health dimensions across iterations:
//     1. editEfficiency: edits/iteration ratio (high retries = risk)
//     2. errorAccumulation: tool errors per iteration
//     3. toolSuccessRate: failed tool calls / total tool calls
//     4. explorationStagnation: read-only tools vs productive tools
//     5. assumptionDensity: assumption language frequency
//   - Each dimension contributes 0-2 points to a composite health score
//   - Threshold: 5+ points (multiple sub-threshold signals accumulating)
//   - Fires at most 2 times per run (advisory, non-blocking)
//   - Zero LLM cost - pure deterministic computation from existing state
//   - Designed to complement, not replace, individual detectors

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

const (
	// trajectoryHealthThreshold: composite score at which we warn.
	// With 5 dimensions contributing 0-2 points each, max is 10.
	// Threshold 5 means "multiple dimensions are degraded" - this catches
	// cases where no single dimension hits its own detector threshold.
	trajectoryHealthThreshold = 5

	// trajectoryHealthMaxWarnings: max warnings per run.
	trajectoryHealthMaxWarnings = 2

	// trajectoryHealthMinIterations: don't assess before this many iterations.
	trajectoryHealthMinIterations = 5
)

// healthDimension represents one orthogonal axis of trajectory health.
type healthDimension struct {
	name        string
	score       int // 0 = healthy, 1 = degraded, 2 = critical
	description string
}

// trajectoryHealthState tracks composite trajectory health across a run.
type trajectoryHealthState struct {
	warnings       int
	totalEdits     int
	totalErrors    int
	totalTools     int
	totalReadTools int
	iterations     int
	assumptionHits int
}

func newTrajectoryHealthState() *trajectoryHealthState {
	return &trajectoryHealthState{}
}

func (s *trajectoryHealthState) reset() {
	*s = trajectoryHealthState{}
}

// recordIteration updates health state based on this iteration's tool activity.
// editCount is the number of file-editing tool calls (edit_file, write_file,
// multi_edit_file, multi_file_edit).
// errorCount is the number of tool calls that returned errors.
// toolCount is the total number of tool calls in this iteration.
// readCount is the number of read-only tool calls (read_file, grep, search_files,
// glob, list_directory, etc.).
// assumptionCount is the number of assumption-language hits detected in this
// iteration's assistant text.
func (s *trajectoryHealthState) recordIteration(editCount, errorCount, toolCount, readCount, assumptionCount int) {
	s.iterations++
	s.totalEdits += editCount
	s.totalErrors += errorCount
	s.totalTools += toolCount
	s.totalReadTools += readCount
	s.assumptionHits += assumptionCount
}

// assess computes the composite health score from accumulated state.
// Returns the list of degraded dimensions and the total score.
func (s *trajectoryHealthState) assess() ([]healthDimension, int) {
	var dims []healthDimension

	// Dimension 1: Edit efficiency - ratio of edits to iterations.
	// High edit count per iteration means many edits crammed in = high
	// churn risk.
	if s.iterations >= 3 {
		editRatio := float64(s.totalEdits) / float64(s.iterations)
		if editRatio > 3.0 {
			dims = append(dims, healthDimension{"edit-volume", 2,
				fmt.Sprintf("high edit volume (%d edits / %d iter = %.1fx)", s.totalEdits, s.iterations, editRatio)})
		} else if editRatio > 2.0 {
			dims = append(dims, healthDimension{"edit-volume", 1,
				fmt.Sprintf("elevated edit volume (%d edits / %d iter = %.1fx)", s.totalEdits, s.iterations, editRatio)})
		}
	}

	// Dimension 2: Error accumulation - tool errors per iteration.
	// Even if errors don't trigger the cascade detector individually,
	// sustained error accumulation signals a degrading trajectory.
	if s.iterations >= 3 {
		errRatio := float64(s.totalErrors) / float64(s.iterations)
		if errRatio > 1.5 {
			dims = append(dims, healthDimension{"error-load", 2,
				fmt.Sprintf("high error load (%d errors / %d iter = %.1f)", s.totalErrors, s.iterations, errRatio)})
		} else if errRatio > 0.7 {
			dims = append(dims, healthDimension{"error-load", 1,
				fmt.Sprintf("moderate error load (%d errors / %d iter = %.1f)", s.totalErrors, s.iterations, errRatio)})
		}
	}

	// Dimension 3: Tool success rate - failed calls / total calls.
	// A low success rate means the agent is fighting its tools.
	if s.iterations >= 3 && s.totalTools >= 6 {
		failRate := float64(s.totalErrors) / float64(s.totalTools)
		if failRate > 0.3 {
			dims = append(dims, healthDimension{"tool-failure", 2,
				fmt.Sprintf("high tool failure rate (%d/%d = %.0f%%)", s.totalErrors, s.totalTools, failRate*100)})
		} else if failRate > 0.15 {
			dims = append(dims, healthDimension{"tool-failure", 1,
				fmt.Sprintf("moderate tool failure rate (%d/%d = %.0f%%)", s.totalErrors, s.totalTools, failRate*100)})
		}
	}

	// Dimension 4: Exploration stagnation - read-only tools dominate
	// without proportional productive output.
	if s.iterations >= 3 && s.totalTools >= 6 {
		readRatio := float64(s.totalReadTools) / float64(s.totalTools)
		if readRatio > 0.7 {
			dims = append(dims, healthDimension{"explore-heavy", 2,
				fmt.Sprintf("exploration-heavy trajectory (%d/%d = %.0f%% read-only tools)", s.totalReadTools, s.totalTools, readRatio*100)})
		} else if readRatio > 0.5 {
			dims = append(dims, healthDimension{"explore-heavy", 1,
				fmt.Sprintf("moderate exploration bias (%d/%d = %.0f%% read-only tools)", s.totalReadTools, s.totalTools, readRatio*100)})
		}
	}

	// Dimension 5: Assumption density - accumulated unverified assumptions.
	// Even if assumption tracker hasn't fired its own threshold, accumulation
	// across iterations compounds risk.
	if s.iterations >= 3 {
		assumeRatio := float64(s.assumptionHits) / float64(s.iterations)
		if assumeRatio > 1.0 {
			dims = append(dims, healthDimension{"assumption-load", 2,
				fmt.Sprintf("high assumption density (%d signals / %d iter = %.1f)", s.assumptionHits, s.iterations, assumeRatio)})
		} else if assumeRatio > 0.5 {
			dims = append(dims, healthDimension{"assumption-load", 1,
				fmt.Sprintf("moderate assumption density (%d signals / %d iter = %.1f)", s.assumptionHits, s.iterations, assumeRatio)})
		}
	}

	total := 0
	for _, d := range dims {
		total += d.score
	}
	return dims, total
}

// countToolTypes categorizes tool calls into edit and read-only counts.
// editCount = file-editing tools (edit_file, write_file, multi_edit_file, multi_file_edit)
// readCount = read-only exploration tools
func countToolTypes(toolCalls []provider.ToolCallDelta) (editCount, readCount int) {
	for _, tc := range toolCalls {
		switch tc.Name {
		case "edit_file", "write_file", "multi_edit_file", "multi_file_edit":
			editCount++
		case "read_file", "multi_file_read", "grep", "search_files", "glob",
			"list_directory", "code_search", "lsp_hover", "lsp_definition",
			"lsp_references", "lsp_symbols", "lsp_workspace_symbols",
			"lsp_diagnostics", "git_log", "git_status", "git_diff", "git_blame":
			readCount++
		}
	}
	return
}

// maybeWarnTrajectoryHealth checks the composite trajectory health score
// and returns a guidance message if the threshold is exceeded.
// Returns empty string if no warning is needed.
func (a *Agent) maybeWarnTrajectoryHealth() string {
	if a.trajectoryHealth == nil {
		return ""
	}
	if a.trajectoryHealth.warnings >= trajectoryHealthMaxWarnings {
		return ""
	}
	if a.trajectoryHealth.iterations < trajectoryHealthMinIterations {
		return ""
	}

	dims, total := a.trajectoryHealth.assess()
	if total < trajectoryHealthThreshold {
		return ""
	}

	a.trajectoryHealth.warnings++

	var lines []string
	for _, d := range dims {
		severity := "degraded"
		if d.score >= 2 {
			severity = "critical"
		}
		lines = append(lines, fmt.Sprintf("  [%s/%s] %s", d.name, severity, d.description))
	}

	header := fmt.Sprintf(
		"[trajectory-health] Composite health score %d/10 - multiple trajectory "+
			"dimensions are degrading simultaneously. While no individual issue may "+
			"be severe enough to trigger its own alert, their accumulation signals an "+
			"unhealthy trajectory that is likely to result in wasted iterations or "+
			"failed completion. Consider: (1) stepping back to reassess your overall "+
			"approach, (2) reducing edit volume by planning before acting, "+
			"(3) verifying tool calls succeed before building on them, "+
			"(4) converting exploration into concrete action.\n",
		total,
	)
	return header + "Degraded dimensions:\n" + strings.Join(lines, "\n")
}
