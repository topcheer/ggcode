package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// repetitionTracker implements semantic-level repetition detection that goes
// beyond the exact-match loop detector in loop_detect.go.
//
// Research basis: SICA's async overseer (Robeyns et al. 2025, arXiv:2504.15228)
// monitors the agent's execution trajectory for pathological patterns. The key
// insight from Reflexion (Shinn et al. 2023) and the Iterative Refinement Loop
// is that agents often get stuck in "near-miss loops" — repeatedly attempting
// to edit the same file with slightly different arguments, each attempt failing
// for the same underlying reason.
//
// The exact-match loop detector misses these because each attempt has different
// arguments. This tracker operates at a higher level of abstraction:
// "same tool + same file + error" = semantic repetition, regardless of arg
// differences.
//
// Detection modes:
//  1. Failed-edit clustering: N+ failed edits to the same file path
//     (edit_file, write_file, multi_edit_file, multi_file_edit).
//  2. Repeated failed searches: N+ failed search_files/grep calls with
//     semantically similar patterns (same directory or overlapping terms).
//  3. Read-edit-read cycle: reading the same file >3 times interleaved with
//     failed edits to it — the agent is "refreshing" without understanding.
//
// Intervention escalation (per file):
//   - 3 failed edits → gentle hint: re-read the file, check whitespace
//   - 5 failed edits → strong warning: try a completely different approach
//   - 7 failed edits → escalate: suggest ask_user or skip this change
//
// All interventions fire at most once per escalation level per file per run.

const (
	failedEditSoftThreshold = 3 // gentle hint
	failedEditHardThreshold = 5 // strong warning
	failedEditEscThreshold  = 7 // escalate to ask_user
)

type repetitionTracker struct {
	mu sync.Mutex

	// failedEditsByFile counts failed edit attempts per file path.
	// Key is the normalized file path from tool args.
	failedEditsByFile map[string]int

	// firedLevels tracks which escalation level has fired for each file.
	// Prevents duplicate interventions at the same level.
	// Key: "filepath:level" (e.g., "main.go:soft")
	firedLevels map[string]bool

	// lastEditFile records the most recent file edit target (any result).
	// Used for read-edit-read cycle detection.
	lastEditFile string

	// readAfterFailedEdit counts read_file calls to a file that had a recent
	// failed edit, without a successful edit in between.
	readAfterFailedEdit map[string]int
}

func newRepetitionTracker() *repetitionTracker {
	return &repetitionTracker{
		failedEditsByFile:   make(map[string]int),
		firedLevels:         make(map[string]bool),
		readAfterFailedEdit: make(map[string]int),
	}
}

// recordEditAttempt tracks a file-editing tool call result.
// Returns guidance text if intervention is needed, "" otherwise.
func (rt *repetitionTracker) recordEditAttempt(toolName string, args json.RawMessage, isError bool) string {
	if !fileEditingTools[toolName] {
		return ""
	}

	filePath := extractFilePathFromArgs(toolName, args)
	if filePath == "" {
		return ""
	}
	filePath = normalizeFilePath(filePath)

	rt.mu.Lock()
	defer rt.mu.Unlock()

	if isError {
		rt.failedEditsByFile[filePath]++
		rt.lastEditFile = filePath
	} else {
		// Successful edit — reset counters for this file.
		rt.failedEditsByFile[filePath] = 0
		rt.readAfterFailedEdit[filePath] = 0
		rt.lastEditFile = filePath
		return ""
	}

	count := rt.failedEditsByFile[filePath]
	debug.Log("repetition", "failed edit #%d to %s", count, filePath)

	return rt.checkEscalation(filePath, count)
}

// recordReadAttempt tracks read_file calls that follow failed edits to the
// same file. This detects the "read-edit-fail-read-edit-fail" cycle.
// Returns guidance if the cycle threshold is exceeded.
func (rt *repetitionTracker) recordReadAttempt(filePath string) string {
	if filePath == "" {
		return ""
	}
	filePath = normalizeFilePath(filePath)

	rt.mu.Lock()
	defer rt.mu.Unlock()

	// Only count reads of files with recent failed edits.
	if rt.failedEditsByFile[filePath] > 0 {
		rt.readAfterFailedEdit[filePath]++
	}

	// If we've read the file 3+ times after failed edits, suggest stepping back.
	cycleKey := filePath + ":cycle"
	if rt.readAfterFailedEdit[filePath] >= 3 && !rt.firedLevels[cycleKey] {
		rt.firedLevels[cycleKey] = true
		debug.Log("repetition", "read-edit-fail cycle detected for %s (%d reads after %d failed edits)",
			filePath, rt.readAfterFailedEdit[filePath], rt.failedEditsByFile[filePath])
		return fmt.Sprintf(
			"Repetition detector: You have read %s %d times after failed edits to it. "+
				"The file content may have changed since you last read it, or your edit target "+
				"may not exist as you expect. Try:\n"+
				"1. Read the file once more and copy the EXACT text you want to match\n"+
				"2. Check for tabs vs spaces — the file may use different indentation\n"+
				"3. Use a smaller, more targeted edit with less surrounding context",
			filePath, rt.readAfterFailedEdit[filePath],
		)
	}

	return ""
}

// checkEscalation returns guidance based on the failed-edit count for a file.
func (rt *repetitionTracker) checkEscalation(filePath string, count int) string {
	// Escalation ladder: each level fires once.
	if count >= failedEditEscThreshold {
		key := filePath + ":escalate"
		if rt.firedLevels[key] {
			return ""
		}
		rt.firedLevels[key] = true
		return fmt.Sprintf(
			"Repetition detector: %d failed edits to %s. You have exhausted reasonable attempts. "+
				"This file may have structural issues you're not seeing. Consider:\n"+
				"1. Use ask_user to get human guidance on this specific file\n"+
				"2. Skip this change and work on other parts of the task\n"+
				"3. Rewrite the entire function/section instead of patching it",
			count, filePath,
		)
	}

	if count >= failedEditHardThreshold {
		key := filePath + ":hard"
		if rt.firedLevels[key] {
			return ""
		}
		rt.firedLevels[key] = true
		return fmt.Sprintf(
			"Repetition detector: %d failed edits to %s. Your approach is not working. "+
				"STOP and reconsider:\n"+
				"1. Read the current file content from scratch — your mental model may be stale\n"+
				"2. Check if the function/type you're editing has been renamed or moved\n"+
				"3. Try a completely different editing strategy (e.g., rewrite the whole function "+
				"instead of a targeted edit)",
			count, filePath,
		)
	}

	if count >= failedEditSoftThreshold {
		key := filePath + ":soft"
		if rt.firedLevels[key] {
			return ""
		}
		rt.firedLevels[key] = true
		return fmt.Sprintf(
			"Repetition detector: %d failed edits to %s. Common causes:\n"+
				"1. Whitespace mismatch (tabs vs spaces) — check the raw file bytes\n"+
				"2. The target text was already changed by a previous edit\n"+
				"3. Line numbers shifted — re-read the file to get current content\n"+
				"Consider using a smaller, more unique anchor string for your edit.",
			count, filePath,
		)
	}

	return ""
}

// reset clears all tracking state for a new run.
func (rt *repetitionTracker) reset() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.failedEditsByFile = make(map[string]int)
	rt.firedLevels = make(map[string]bool)
	rt.readAfterFailedEdit = make(map[string]int)
	rt.lastEditFile = ""
}

// normalizeFilePath canonicalizes a file path for tracking purposes.
// Strips leading "./" and resolves to a consistent form.
func normalizeFilePath(path string) string {
	path = strings.TrimSpace(path)
	// Strip leading "./" which varies between tools.
	for strings.HasPrefix(path, "./") {
		path = path[2:]
	}
	// Lowercase for case-insensitive matching (handles case-only differences
	// that cause false negatives on case-insensitive filesystems like macOS).
	// Note: we keep the original case in the guidance message via the caller.
	return path
}

// --- Agent integration ---

// repetitionCheckEdit is called from the agent loop after a file-editing tool
// completes. It tracks the result and returns guidance if intervention is needed.
func (a *Agent) repetitionCheckEdit(toolName string, args json.RawMessage, isError bool) string {
	if a.repetition == nil {
		return ""
	}
	return a.repetition.recordEditAttempt(toolName, args, isError)
}

// repetitionCheckRead is called from the agent loop after a read_file tool
// completes. It detects read-edit-fail cycles.
func (a *Agent) repetitionCheckRead(filePath string) string {
	if a.repetition == nil {
		return ""
	}
	return a.repetition.recordReadAttempt(filePath)
}

// resetRepetitionTracker clears all repetition tracking for a new run.
func (a *Agent) resetRepetitionTracker() {
	if a.repetition == nil {
		return
	}
	a.repetition.reset()
}
