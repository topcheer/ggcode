package agent

// Uncertainty Propagation Tracker (UProp)
//
// Research basis:
//   - UProp: Investigating the Uncertainty Propagation of LLMs in Multi-Step
//     Agentic Decision-Making (Duan et al., arXiv:2506.17419, 2025)
//   - "Calibrating Long-Horizon Agents" (Zhang, 2026)
//     Decomposes step uncertainty into intrinsic + extrinsic (inherited) terms
//
// Problem: ggcode tracks overall trajectory quality (confidence.go) but does not
// explicitly model how uncertainty propagates through the trajectory. An early
// mistake (e.g., wrong assumption, misread file) "poisons" all subsequent steps
// because the agent reasons from that incorrect context.
//
// Gap: No uncertainty propagation tracking -- the system doesn't distinguish
// between:
//   1. Errors that are local and recoverable (intrinsic)
//   2. Errors that corrupt the trajectory's foundation and need radical correction
//      (inherited/extrinsic)
//
// Design:
//   - Decomposes step uncertainty into intrinsic (H(d_t|h_t)) and extrinsic
//     (I(d_t; d_{<t})) components
//   - Extrinsic term = mutual information with past decisions → how much this
//     step depends on potentially flawed history
//   - Tracks "weakest link" trajectory health (min product of confidences)
//   - Provides propagation-aware guidance when inherited uncertainty dominates
//   - Zero LLM cost - deterministic heuristics
//   - Complements confidence.go (holistic scoring) by adding temporal dynamics

import (
	"fmt"
	"strings"
	"sync"
)

const (
	// uPropMinCalls: minimum calls before propagation analysis
	uPropMinCalls = 4

	// uPropHighPropagationThreshold: when extrinsic/total ratio exceeds this,
	// the step is dominated by inherited uncertainty → critical warning
	uPropHighPropagationThreshold = 0.6

	// uPropWeakestLinkThreshold: when weakest-link product drops below this,
	// trajectory has a "poison pill" step that compromises everything downstream
	uPropWeakestLinkThreshold = 0.3
)

// uncertaintyComponent represents one dimension of uncertainty at a step.
type uncertaintyComponent struct {
	intrinsic  float64 // H(d_t|h_t) - uncertainty from current step alone
	extrinsic  float64 // I(d_t; d_{<t}) - uncertainty inherited from history
	total      float64 // intrinsic + extrinsic
	cumulative float64 // running product of step confidences (weakest-link)
}

// uPropState tracks uncertainty propagation across the trajectory.
type uPropState struct {
	mu sync.Mutex

	steps            []uncertaintyComponent // per-step uncertainty decomposition
	weakestLink      float64                // min cumulative confidence
	weakestLinkStep  int                    // step index where weakest-link occurred
	totalUncertainty float64                // sum of total uncertainties (for normalization)

	guidanceGiven bool // fire at most once per run
}

func newUPropState() *uPropState {
	return &uPropState{
		steps:       make([]uncertaintyComponent, 0),
		weakestLink: 1.0, // starts at perfect confidence
	}
}

func (u *uPropState) reset() {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.steps = u.steps[:0]
	u.weakestLink = 1.0
	u.weakestLinkStep = 0
	u.totalUncertainty = 0
	u.guidanceGiven = false
}

// recordStep updates uncertainty tracking after a tool call.
//
// intrinsicUncertainty: 0.0-1.0, uncertainty from this step alone
//   - Low (0.0-0.2): tool succeeded with clear, definitive output
//   - High (0.7-1.0): tool failed, returned error, or output is ambiguous
//
// dependsOnHistory: 0.0-1.0, how much this step relies on prior decisions
//   - Low (0.0-0.2): independent operation (e.g., read_file on fresh file)
//   - High (0.7-1.0): builds directly on previous outputs (e.g., edit_file after
//     grep, or multi-step reasoning chain)
//
// This function implements the UProp decomposition:
//
//	U(d_t) = H(d_t|h_t) [intrinsic] + I(d_t; d_{<t}) [extrinsic/inherited]
func (u *uPropState) recordStep(intrinsicUncertainty, dependsOnHistory float64) {
	u.mu.Lock()
	defer u.mu.Unlock()

	step := len(u.steps)

	// Clamp inputs to [0,1]
	if intrinsicUncertainty < 0 {
		intrinsicUncertainty = 0
	}
	if intrinsicUncertainty > 1 {
		intrinsicUncertainty = 1
	}
	if dependsOnHistory < 0 {
		dependsOnHistory = 0
	}
	if dependsOnHistory > 1 {
		dependsOnHistory = 1
	}

	// Extrinsic = mutual information with past (simplified as dependence × prior uncertainty)
	// If this step strongly depends on history, and history was uncertain, that uncertainty
	// propagates forward as extrinsic uncertainty for this step.
	var extrinsic float64
	if step > 0 {
		// Get the total uncertainty of the previous step
		prevUncertainty := u.steps[step-1].total
		// Extrinsic = dependence * prior uncertainty
		// This approximates I(d_t; d_{<t}) without full MI computation
		extrinsic = dependsOnHistory * prevUncertainty
	}

	total := intrinsicUncertainty + extrinsic

	// Compute cumulative confidence (weakest-link metric)
	// Confidence = 1 - uncertainty
	stepConfidence := 1.0 - total
	if stepConfidence < 0 {
		stepConfidence = 0
	}

	// Cumulative = product of all step confidences
	var cumulative float64
	if step == 0 {
		cumulative = stepConfidence
	} else {
		prevCumulative := u.steps[step-1].cumulative
		cumulative = prevCumulative * stepConfidence
	}

	// Update weakest-link tracking
	if cumulative < u.weakestLink {
		u.weakestLink = cumulative
		u.weakestLinkStep = step
	}

	component := uncertaintyComponent{
		intrinsic:  intrinsicUncertainty,
		extrinsic:  extrinsic,
		total:      total,
		cumulative: cumulative,
	}

	u.steps = append(u.steps, component)
	u.totalUncertainty += total
}

// getExtrinsicRatio returns extrinsic/total for the most recent step.
// Returns 0 if no steps recorded.
func (u *uPropState) getExtrinsicRatio() float64 {
	u.mu.Lock()
	defer u.mu.Unlock()

	if len(u.steps) == 0 {
		return 0
	}

	last := u.steps[len(u.steps)-1]
	if last.total == 0 {
		return 0
	}
	return last.extrinsic / last.total
}

// maybeIntervene provides guidance when uncertainty propagation indicates trouble.
// Fires at most once per run.
func (u *uPropState) maybeIntervene(recentToolNames []string) string {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.guidanceGiven {
		return ""
	}
	if len(u.steps) < uPropMinCalls {
		return ""
	}

	var guidance strings.Builder
	var warnings []string
	var criticalIssue bool

	// Check 1: High propagation ratio (step dominated by inherited uncertainty)
	extrinsicRatio := u.getExtrinsicRatio()
	if extrinsicRatio > uPropHighPropagationThreshold {
		criticalIssue = true
		warnings = append(warnings,
			fmt.Sprintf("recent step's uncertainty is %.0f%% inherited from earlier decisions",
				extrinsicRatio*100))
	}

	// Check 2: Weakest-link below threshold (poison pill detected)
	if u.weakestLink < uPropWeakestLinkThreshold {
		criticalIssue = true
		warnings = append(warnings,
			fmt.Sprintf("trajectory confidence dropped to %.0f%% at step %d "+
				"(a critical early error is compromising all subsequent steps)",
				u.weakestLink*100, u.weakestLinkStep+1))
	}

	// Check 3: Rising extrinsic trend (uncertainty is growing over time)
	if len(u.steps) >= 3 {
		risingCount := 0
		for i := 1; i < len(u.steps); i++ {
			if u.steps[i].extrinsic > u.steps[i-1].extrinsic {
				risingCount++
			}
		}
		if risingCount >= 2 {
			warnings = append(warnings, "inherited uncertainty is trending upward")
		}
	}

	if len(warnings) == 0 {
		return ""
	}

	u.guidanceGiven = true

	guidance.WriteString("[uncertainty propagation] ")

	if criticalIssue {
		guidance.WriteString("CRITICAL: ")
	}

	guidance.WriteString(strings.Join(warnings, ". "))
	guidance.WriteString(".\n\n")

	// Provide actionable guidance based on the type of problem
	if u.weakestLink < uPropWeakestLinkThreshold {
		guidance.WriteString("Action: A weak early step is poisoning the trajectory. " +
			"Consider stepping back to verify foundational assumptions. " +
			"If you made a wrong assumption or misread something early on, " +
			"continuing from that flawed context will likely produce more errors. " +
			"Use undo_edit or git reset to the last known-good state, then proceed fresh.\n")
	} else if extrinsicRatio > uPropHighPropagationThreshold {
		guidance.WriteString("Action: Current decisions are heavily dependent on earlier steps " +
			"that may be flawed. Verify the chain of reasoning: " +
			"read the files again, re-run diagnostic tools, or ask for clarification " +
			"before building further on potentially shaky foundations.\n")
	} else {
		guidance.WriteString("Action: Uncertainty is accumulating through the trajectory. " +
			"Pause to verify your current state before proceeding: run tests, check git status, " +
			"or use verification tools to confirm intermediate results are correct.\n")
	}

	// Add tool-specific context if available
	if len(recentToolNames) > 0 {
		guidance.WriteString(fmt.Sprintf("\nRecent tool chain: %s",
			strings.Join(recentToolNames, " → ")))
	}

	return guidance.String()
}

// estimateIntrinsicUncertainty heuristically estimates intrinsic uncertainty
// for a tool call based on its result.
func estimateIntrinsicUncertainty(toolName string, isError bool, outputSize int) float64 {
	if isError {
		// Error = high intrinsic uncertainty
		return 0.9
	}

	// Different tools have different uncertainty profiles
	switch {
	case isUPropReadTool(toolName):
		// Read tools: low uncertainty if got data, moderate if empty/truncated
		if outputSize > 0 {
			return 0.1
		}
		return 0.5 // empty result → ambiguous

	case isEditTool(toolName):
		// Edit tools: low uncertainty if succeeded
		return 0.1

	case isUPropSearchTool(toolName):
		// Search tools: moderate uncertainty (results may be incomplete/noisy)
		if outputSize > 100 {
			return 0.2 // got results
		}
		return 0.6 // few/no results → uncertain

	default:
		// Other tools: assume moderate uncertainty
		return 0.3
	}
}

// estimateHistoryDependence heuristically estimates how much a tool call
// depends on previous decisions.
func estimateHistoryDependence(toolName string, _ int, previousErrors int) float64 {
	// Independent operations: low dependence
	if isUPropReadTool(toolName) || isListTool(toolName) {
		return 0.1
	}

	// Edits after previous errors: high dependence (may be fixing errors)
	if isEditTool(toolName) && previousErrors > 0 {
		return 0.8
	}

	// Multi-file operations: medium-high dependence (builds on context)
	if strings.HasPrefix(toolName, "multi_") {
		return 0.6
	}

	// Default: moderate dependence
	return 0.4
}

// isListTool returns true for tools that list/discover resources.
func isListTool(name string) bool {
	return name == "list_directory" ||
		name == "glob" ||
		strings.HasPrefix(name, "git_") && strings.Contains(name, "list")
}

// isUPropReadTool returns true for tools that read information.
func isUPropReadTool(name string) bool {
	return strings.HasPrefix(name, "read_") ||
		name == "search_files" ||
		name == "grep" ||
		name == "lsp_" ||
		name == "git_" ||
		name == "list_directory"
}

// isUPropSearchTool returns true for tools that search for content.
func isUPropSearchTool(name string) bool {
	return name == "search_files" || name == "grep" || name == "code_search"
}
