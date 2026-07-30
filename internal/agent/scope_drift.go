package agent

// Scope Drift Detection — Semantic Task Scope Creep Warning
//
// While the overseer detects quantitative drift (no productive actions for N
// iterations), this guard detects qualitative/semantic drift: the agent IS
// making productive changes (edits, writes, commands) but the set of files
// and directories being modified has grown far beyond what the original task
// implied. This is "scope creep" — a common failure mode in autonomous agents:
//
//   - Agent fixes a bug, then starts refactoring nearby code "while I'm here"
//   - Agent adds tests for files unrelated to the original task
//   - Agent edits configuration files outside the expected scope
//   - Agent touches >15 files across >6 directories for a "fix one function" task
//
// Competitor approaches:
//   - Claude Code: system prompt with scope guidance, no runtime detection
//   - Cursor Composer: explicit user-selected scope, no auto-detection
//   - OpenHands: periodic re-planning by a separate LLM planner (costs tokens)
//   - Devin: SICA overseer monitors trajectory shape but not file diversity
//
// Our approach: deterministic file-diversity tracking. Zero LLM cost.
// Tracks unique directories touched by productive operations and fires when
// the diversity suggests scope creep. The threshold adapts to task complexity
// detected by the planner: complex multi-file tasks get a higher bar before
// triggering, while simple single-file tasks trigger earlier.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// scopeDirThresholdSimple: max unique directories for simple tasks before
	// scope creep warning. A simple "fix this function" task shouldn't touch
	// more than a few directories.
	scopeDirThresholdSimple = 5

	// scopeDirThresholdComplex: max unique directories for complex tasks.
	// Multi-file refactors may legitimately touch more directories.
	scopeDirThresholdComplex = 10

	// scopeFileThreshold: max unique files before warning regardless of
	// task complexity. Even complex tasks rarely need >25 files.
	scopeFileThreshold = 25

	// scopeWarnAfter: minimum number of productive operations before the
	// guard starts checking. Avoids false positives during early exploration.
	scopeWarnAfter = 6
)

// scopeDriftState tracks the diversity of files/directories modified during
// a run to detect semantic scope creep.
type scopeDriftState struct {
	// editedDirs: set of unique top-level-ish directories modified.
	editedDirs map[string]bool

	// editedFiles: set of unique file paths modified.
	editFiles map[string]bool

	// productiveCount: total productive operations (edits/writes).
	productiveCount int

	// fired: whether the scope drift warning has already been injected.
	fired bool
}

func newScopeDriftState() *scopeDriftState {
	return &scopeDriftState{
		editedDirs: make(map[string]bool),
		editFiles:  make(map[string]bool),
	}
}

// reset clears state for a new run.
func (s *scopeDriftState) reset() {
	s.editedDirs = make(map[string]bool)
	s.editFiles = make(map[string]bool)
	s.productiveCount = 0
	s.fired = false
}

// recordEdit tracks a file modification (edit/write operation).
func (s *scopeDriftState) recordEdit(filePath string) {
	if filePath == "" {
		return
	}
	// Normalize path.
	fp := filepath.Clean(filePath)
	s.editFiles[fp] = true

	// Extract a directory signature. Use the first 2-3 path segments to group
	// related files (e.g., "internal/agent" counts as one scope area).
	dir := filepath.Dir(fp)
	sig := dirSignature(dir)
	if sig != "" {
		s.editedDirs[sig] = true
	}
	s.productiveCount++
}

// dirSignature reduces a directory path to a 2-segment signature for grouping.
// e.g., "internal/agent/sub" -> "internal/agent"
//
//	"src/components" -> "src/components"
//	"pkg" -> "pkg"
func dirSignature(dir string) string {
	parts := strings.Split(dir, "/")
	if len(parts) <= 1 {
		return dir
	}
	// Keep top 2 segments for meaningful grouping.
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return dir
}

// check returns a guidance message if scope drift is detected, empty otherwise.
// isComplex adapts the threshold based on the planner's complexity assessment.
func (s *scopeDriftState) check(isComplex bool) string {
	if s.fired || s.productiveCount < scopeWarnAfter {
		return ""
	}

	dirThreshold := scopeDirThresholdSimple
	if isComplex {
		dirThreshold = scopeDirThresholdComplex
	}

	dirCount := len(s.editedDirs)
	fileCount := len(s.editFiles)

	// Trigger if either directories or files exceed their thresholds.
	if dirCount <= dirThreshold && fileCount <= scopeFileThreshold {
		return ""
	}

	s.fired = true

	// Build a list of touched directories for the guidance message.
	dirs := make([]string, 0, len(s.editedDirs))
	for d := range s.editedDirs {
		dirs = append(dirs, d)
	}
	dirList := strings.Join(dirs, ", ")
	if len([]rune(dirList)) > 200 {
		dirList = string([]rune(dirList)[:197]) + "..."
	}

	debug.Log("scope_drift", "scope creep: %d dirs, %d files (threshold dirs=%d files=%d, complex=%v)",
		dirCount, fileCount, dirThreshold, scopeFileThreshold, isComplex)

	primaryMetric := "directories"
	primaryCount := dirCount
	primaryThreshold := dirThreshold
	if fileCount > scopeFileThreshold && fileCount >= dirCount {
		primaryMetric = "files"
		primaryCount = fileCount
		primaryThreshold = scopeFileThreshold
	}

	return fmt.Sprintf(
		"Scope check: You have modified %d %s across %d unique directories "+
			"(threshold: %d %s). This exceeds what the original task likely requires. "+
			"If you are doing legitimate multi-file work (e.g., a planned refactor), continue. "+
			"Otherwise, re-read the original request and focus on completing ONLY what was asked.\n"+
			"Directories touched: %s",
		primaryCount, primaryMetric, dirCount,
		primaryThreshold, primaryMetric,
		dirList,
	)
}

// --- Agent integration ---

// scopeDriftRecord tracks a productive file operation for scope drift analysis.
func (a *Agent) scopeDriftRecord(toolName string, fileHint string) {
	if a.scopeDrift == nil {
		return
	}
	if !productiveEditTools[toolName] {
		return
	}
	a.scopeDrift.recordEdit(fileHint)
}

// scopeDriftCheck returns guidance if scope drift is detected.
func (a *Agent) scopeDriftCheck() string {
	if a.scopeDrift == nil {
		return ""
	}
	isComplex := false
	if a.planner != nil {
		a.planner.mu.Lock()
		isComplex = a.planner.isComplex
		a.planner.mu.Unlock()
	}
	return a.scopeDrift.check(isComplex)
}

// resetScopeDrift clears state for a new run.
func (a *Agent) resetScopeDrift() {
	if a.scopeDrift != nil {
		a.scopeDrift.reset()
	}
}

// productiveEditTools are tools that modify files (subset of productiveTools).
var productiveEditTools = map[string]bool{
	"edit_file":        true,
	"write_file":       true,
	"multi_edit_file":  true,
	"multi_file_edit":  true,
	"multi_file_write": true,
	"notebook_edit":    true,
}
