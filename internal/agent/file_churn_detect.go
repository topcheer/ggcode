package agent

// File Churn Detector - Invalidated Assumption Detection
//
// Research basis:
//   - "Replanning When Agent Execution Fails: Dynamic Plan Revision" (2025-2026):
//     The #2 replanning trigger is "Invalidated Assumptions" -- when execution
//     reveals that a planning assumption was wrong, all dependent steps must be
//     revised. In coding agents, the most common, measurable manifestation is
//     repeated edits to the SAME file: each re-edit means the prior edit was
//     based on a wrong assumption about the file's structure or content.
//   - "MetaCognition Patterns for AI Agent Self-Monitoring" (Microsoft, 2025-2026):
//     Metacognitive monitoring includes detecting when your own actions aren't
//     converging -- repeated edits to one file without convergence is a clear
//     self-monitoring signal.
//   - "Position: Uncertainty Quantification Needs Reassessment" (arXiv:2505.22655):
//     Epistemic uncertainty in LLMs manifests as inconsistent outputs. Repeated
//     edits to the same file across iterations is observable epistemic uncertainty.
//
// What it detects: When the agent edits the same file 3+ times in a single run,
// each re-edit signals that a prior assumption about that file was wrong. Instead
// of continuing to patch, the agent should step back and re-read the file holistically.
//
// This is different from:
//   - fixCascade (edit-verify-fail cycle tracking -- focuses on ERROR patterns)
//   - recurringError (same error after edits -- error-focused)
//   - convergenceLock (edits AFTER successful verification)
//   - loopDetector (identical consecutive calls)
// File churn catches ASSUMPTION INVALIDATION: re-editing the same file because
// the mental model of it was wrong, regardless of whether errors occurred.
//
// Zero LLM cost. Non-blocking. Fires at most once per run.

import (
	"fmt"
	"strings"
)

const (
	// churnThreshold: editing the same file this many times triggers guidance.
	churnThreshold = 3

	// churnMaxTracked: maximum number of files to track edit counts for.
	churnMaxTracked = 50

	// churnMaxWarningFiles: maximum number of churned files to list in guidance.
	churnMaxWarningFiles = 3
)

// churnState tracks per-file edit counts within a single run.
type churnState struct {
	editCounts map[string]int
	fired      bool
}

func newChurnState() *churnState {
	return &churnState{
		editCounts: make(map[string]int),
	}
}

func (c *churnState) reset() {
	c.editCounts = make(map[string]int)
	c.fired = false
}

// recordVerifySuccess clears per-file edit counts when a verification
// command succeeds (#1460-C): iterate-edit-verify loops where EVERY step
// is confirmed green are legitimate refinement - the old bare count
// reached the threshold on the third polish pass and ordered the agent
// to 'STOP editing, your initial assumptions were wrong'.
func (c *churnState) recordVerifySuccess() {
	for p := range c.editCounts {
		delete(c.editCounts, p)
	}
}

// recordEdit increments the edit count for the given file paths.
// Called after each edit-type tool execution.
func (c *churnState) recordEdit(paths []string) {
	for _, p := range paths {
		if p == "" {
			continue
		}
		c.editCounts[p]++
		// Prevent unbounded growth from pathological patterns.
		if len(c.editCounts) > churnMaxTracked {
			// Evict the file with the lowest count to make room.
			var minKey string
			var minVal int = -1
			for k, v := range c.editCounts {
				if minVal == -1 || v < minVal {
					minKey = k
					minVal = v
				}
			}
			if minKey != "" && minKey != p {
				delete(c.editCounts, minKey)
			}
		}
	}
}

// check returns non-empty guidance if any file has been edited >= churnThreshold times.
func (c *churnState) check() string {
	if c.fired {
		return ""
	}

	var churned []string
	for path, count := range c.editCounts {
		if count >= churnThreshold {
			churned = append(churned, path)
		}
	}

	if len(churned) == 0 {
		return ""
	}

	c.fired = true
	return c.formatGuidance(churned)
}

func (c *churnState) formatGuidance(churnedFiles []string) string {
	// Sort by edit count descending (most churned first).
	type fileCount struct {
		path  string
		count int
	}
	var fcs []fileCount
	for _, fp := range churnedFiles {
		fcs = append(fcs, fileCount{fp, c.editCounts[fp]})
	}
	// Simple sort by count descending.
	for i := 0; i < len(fcs); i++ {
		for j := i + 1; j < len(fcs); j++ {
			if fcs[j].count > fcs[i].count {
				fcs[i], fcs[j] = fcs[j], fcs[i]
			}
		}
	}

	// Limit to top N files.
	if len(fcs) > churnMaxWarningFiles {
		fcs = fcs[:churnMaxWarningFiles]
	}

	var sb strings.Builder
	sb.WriteString("[Assumption Invalidation Alert] You have edited the following file(s) ")
	sb.WriteString(fmt.Sprintf("3+ times in this run:\n"))
	for _, fc := range fcs {
		sb.WriteString(fmt.Sprintf("  - %s (%d edits)\n", fc.path, fc.count))
	}
	sb.WriteString("\n")
	sb.WriteString("Repeated edits to the same file indicate that your initial assumptions ")
	sb.WriteString("about its structure or content were wrong. Each re-edit is a patch on a ")
	sb.WriteString("flawed mental model.\n\n")
	sb.WriteString("Recommended actions:\n")
	sb.WriteString("1. STOP editing and re-read the file holistically to update your understanding\n")
	sb.WriteString("2. Check if the file has dependencies or interfaces you're violating\n")
	sb.WriteString("3. Consider whether the approach itself needs to change rather than patching further\n")
	sb.WriteString("4. If fixing one issue keeps breaking another, the root cause may be architectural\n")
	sb.WriteString("Continuing to patch without understanding will compound errors.")

	return sb.String()
}
