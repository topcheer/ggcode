package agent

// Last-Known-Good Checkpoint -- actionable revert targets for failed self-correction
//
// Research basis: "Agent self-correction has 3 distinct failure modes" (2026):
//   1. False convergence (symptom suppression)
//   2. Correction-induced regression (fix A breaks B → oscillation)
//   3. Context collapse on long repair chains
//
// Existing ggcode systems that are CLOSE but don't cover this:
//
//   - self_correction_gate.go: detects net-negative self-correction (EIR > ECR)
//     and tells the agent to "consider reverting your recent edits" -- but provides
//     ZERO actionable detail about WHICH files to revert or HOW. The agent is left
//     guessing, often reverting the wrong file or manually diffing.
//   - verify_regression.go: classifies errors as NEW/PERSISTENT/RESOLVED but
//     doesn't link regressions back to the specific edits that caused them.
//   - convergence_lock.go: detects post-success unnecessary edits, not pre-success
//     regression-causing edits.
//   - checkpoint/ package: full-session undo checkpoints, but not lightweight
//     per-verification-cycle snapshots tied to the last-known-good state.
//
// The gap: when the self-correction gate fires, the agent needs to know:
//   - Which files were modified AFTER the last passing verification
//   - Which of those files are the most likely regression sources
//   - A concrete git command to revert to the last-known-good state
//
// Competitor analysis:
//   - Claude Code: tracks file changes since last test pass; suggests `git checkout`
//     on specific files when stuck in a repair loop
//   - Cursor: maintains a checkpoint of the last working state and offers one-click
//     revert to it via its checkpoint system
//   - Aider: automatically commits after each LLM edit, making `git diff` and
//     `git checkout` trivially reversible per-step
//   - Devin: tracks a "last green" snapshot and can auto-revert when stuck
//
// Our approach: lightweight file-set snapshot. When verification PASSES, record
// the set of files that existed at that point (the "last-known-good" state).
// When verification FAILS and regressions are detected, diff the current modified
// file set against the last-known-good set to identify:
//   - Files modified since last green (primary revert candidates)
//   - Files added since last green (safe to delete / review)
//
// This is pure in-memory state -- zero disk I/O, zero LLM cost. It complements
// the self-correction gate by converting its generic "consider reverting" advice
// into specific, actionable file-level guidance.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// checkpointMaxFiles limits the number of files listed in the revert guidance
	// to avoid context flooding when a large refactor is in progress.
	checkpointMaxFiles = 10

	// checkpointMinIterations is the minimum number of verification cycles before
	// the checkpoint starts providing revert guidance. This avoids false positives
	// during initial exploration before the first build even runs.
	checkpointMinVerifyCycles = 1
)

// lastGoodCheckpoint tracks the set of files modified at the last passing
// verification, enabling actionable revert guidance when self-correction fails.
type lastGoodCheckpoint struct {
	// lastGoodFiles is the set of files that were modified as of the last
	// passing verification. Empty until the first verify pass.
	lastGoodFiles map[string]bool

	// hasBaseline is true after the first verification (pass or fail).
	hasBaseline bool

	// verifyCycles counts total verification cycles (pass + fail).
	verifyCycles int

	// lastVerifyFailed is true if the most recent verification failed.
	// Used to gate revertGuidance -- only provide guidance after a failure.
	lastVerifyFailed bool

	// currentModifiedFiles tracks files modified in the current edit cycle
	// (since the last verification, regardless of outcome).
	currentModifiedFiles map[string]bool

	// filesModifiedSinceLastGood tracks ALL files modified since the last
	// passing verification. This is the key revert-candidate set.
	filesModifiedSinceLastGood map[string]bool
}

func newLastGoodCheckpoint() *lastGoodCheckpoint {
	return &lastGoodCheckpoint{
		lastGoodFiles:              make(map[string]bool),
		currentModifiedFiles:       make(map[string]bool),
		filesModifiedSinceLastGood: make(map[string]bool),
	}
}

func (c *lastGoodCheckpoint) reset() {
	if c == nil {
		return
	}
	c.lastGoodFiles = make(map[string]bool)
	c.hasBaseline = false
	c.verifyCycles = 0
	c.lastVerifyFailed = false
	c.currentModifiedFiles = make(map[string]bool)
	c.filesModifiedSinceLastGood = make(map[string]bool)
}

// recordFileEdit tracks a file modification during the current edit cycle.
// Called from the agent's tool execution path for file-editing tools.
func (c *lastGoodCheckpoint) recordFileEdit(filePath string) {
	if c == nil || filePath == "" {
		return
	}
	fp := filepath.Clean(filePath)
	c.currentModifiedFiles[fp] = true
	c.filesModifiedSinceLastGood[fp] = true
}

// recordVerifyPass is called when verification passes. It snapshots the
// current modified file set as the new "last-known-good" state.
func (c *lastGoodCheckpoint) recordVerifyPass() {
	if c == nil {
		return
	}
	c.verifyCycles++
	c.hasBaseline = true
	c.lastVerifyFailed = false

	// Snapshot current state as the new last-known-good.
	c.lastGoodFiles = make(map[string]bool, len(c.currentModifiedFiles))
	for f := range c.currentModifiedFiles {
		c.lastGoodFiles[f] = true
	}

	// Reset the since-last-good tracker -- everything is green now.
	c.filesModifiedSinceLastGood = make(map[string]bool)

	debug.Log("last-good-checkpoint", "verify PASSED: checkpoint updated with %d files", len(c.lastGoodFiles))
}

// recordVerifyFail is called when verification fails. It records the cycle
// but does not update the last-known-good state.
func (c *lastGoodCheckpoint) recordVerifyFail() {
	if c == nil {
		return
	}
	c.verifyCycles++
	c.hasBaseline = true
	c.lastVerifyFailed = true
}

// revertGuidance generates actionable file-level revert guidance when the
// self-correction loop is detected as net-negative. Returns empty string if
// no guidance is warranted (no baseline, no files modified since last good,
// or the last verification passed).
func (c *lastGoodCheckpoint) revertGuidance() string {
	if c == nil || !c.hasBaseline || !c.lastVerifyFailed {
		return ""
	}
	if c.verifyCycles < checkpointMinVerifyCycles {
		return ""
	}
	if len(c.filesModifiedSinceLastGood) == 0 {
		return ""
	}

	// Categorize files: those that existed at last-good vs newly created.
	var modifiedSinceGood, newFiles []string
	for f := range c.filesModifiedSinceLastGood {
		if c.lastGoodFiles[f] {
			modifiedSinceGood = append(modifiedSinceGood, f)
		} else {
			newFiles = append(newFiles, f)
		}
	}

	if len(modifiedSinceGood) == 0 && len(newFiles) == 0 {
		return ""
	}

	return c.formatRevertGuidance(modifiedSinceGood, newFiles)
}

// formatRevertGuidance builds the revert message for files that were modified
// or created since the last passing verification.
func (c *lastGoodCheckpoint) formatRevertGuidance(modifiedFiles, newFiles []string) string {
	total := len(modifiedFiles) + len(newFiles)
	debug.Log("last-good-checkpoint", "generating revert guidance: %d modified, %d new since last good", len(modifiedFiles), len(newFiles))

	var b strings.Builder
	b.WriteString("[LAST-KNOWN-GOOD] The following files were modified or created after the last passing verification ")
	b.WriteString("and are the most likely source of the regressions above:\n")

	count := 0
	for _, f := range modifiedFiles {
		if count >= checkpointMaxFiles {
			b.WriteString(fmt.Sprintf("  ... and %d more file(s)\n", total-count))
			break
		}
		b.WriteString(fmt.Sprintf("  - %s\n", f))
		count++
	}
	for _, f := range newFiles {
		if count >= checkpointMaxFiles {
			b.WriteString(fmt.Sprintf("  ... and %d more file(s)\n", total-count))
			break
		}
		b.WriteString(fmt.Sprintf("  - %s (new)\n", f))
		count++
	}

	if len(modifiedFiles) > 0 {
		b.WriteString("\nTo revert to the last-known-good state:\n")
		b.WriteString("  1. Use `git diff <file>` to review what changed in each file\n")
		b.WriteString("  2. Use `git checkout -- <file>` to revert specific files\n")
		b.WriteString("  3. Or use the undo_edit tool to revert recent edits\n")
		b.WriteString("  4. After reverting, re-read the original task and try a different approach\n")
	} else {
		b.WriteString("\nThese are new files -- review and consider removing them before trying a different approach.\n")
	}
	b.WriteString("Do NOT continue making incremental fixes -- the self-correction loop is not converging.\n")

	return b.String()
}

// --- Agent integration methods ---

// lastGoodCheckpointRecordEdit records a file modification for checkpoint tracking.
func (a *Agent) lastGoodCheckpointRecordEdit(toolName string, fileHint string) {
	if a.lastGoodCheckpoint == nil {
		return
	}
	if !productiveEditTools[toolName] {
		return
	}
	a.lastGoodCheckpoint.recordFileEdit(fileHint)
}

// lastGoodCheckpointRecordPass records a passing verification.
func (a *Agent) lastGoodCheckpointRecordPass() {
	if a.lastGoodCheckpoint != nil {
		a.lastGoodCheckpoint.recordVerifyPass()
	}
}

// lastGoodCheckpointRecordFail records a failing verification.
func (a *Agent) lastGoodCheckpointRecordFail() {
	if a.lastGoodCheckpoint != nil {
		a.lastGoodCheckpoint.recordVerifyFail()
	}
}

// lastGoodCheckpointGuidance returns actionable revert guidance when the
// self-correction loop is net-negative.
func (a *Agent) lastGoodCheckpointGuidance() string {
	if a.lastGoodCheckpoint == nil {
		return ""
	}
	return a.lastGoodCheckpoint.revertGuidance()
}

// resetLastGoodCheckpoint clears checkpoint state for a new run.
func (a *Agent) resetLastGoodCheckpoint() {
	if a.lastGoodCheckpoint != nil {
		a.lastGoodCheckpoint.reset()
	}
}
