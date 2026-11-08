package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	failedEditSoftThreshold = 3
	failedEditHardThreshold = 5
	failedEditEscThreshold  = 7
)

type repetitionTracker struct {
	mu sync.Mutex

	failedEditsByFile map[string]int

	firedLevels map[string]bool

	readAfterFailedEdit map[string]int
}

func newRepetitionTracker() *repetitionTracker {
	return &repetitionTracker{
		failedEditsByFile:   make(map[string]int),
		firedLevels:         make(map[string]bool),
		readAfterFailedEdit: make(map[string]int),
	}
}

func (rt *repetitionTracker) recordEditAttempt(toolName string, args json.RawMessage, isError bool) string {
	// #5254: apply_patch (Responses-native) edit attempts count for
	// repeated-failed-edit tracking (its args carry a top-level path that
	// extractFilePathFromArgs already reads).
	if !mutatingSourceEdit(toolName) {
		return ""
	}

	filePath := extractFilePathFromArgs(toolName, args)
	if filePath == "" {
		return ""
	}

	filePath = repetitionFileKey(filePath)

	rt.mu.Lock()
	defer rt.mu.Unlock()

	if isError {
		rt.failedEditsByFile[filePath]++
	} else {

		rt.failedEditsByFile[filePath] = 0
		rt.readAfterFailedEdit[filePath] = 0
		return ""
	}

	count := rt.failedEditsByFile[filePath]
	debug.Log("repetition", "failed edit #%d to %s", count, filePath)

	return rt.checkEscalation(filePath, count)
}

func (rt *repetitionTracker) failedEditSnapshot() map[string]int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	out := make(map[string]int, len(rt.failedEditsByFile))
	for f, n := range rt.failedEditsByFile {
		out[f] = n
	}
	return out
}

func (rt *repetitionTracker) recordReadAttempt(filePath string) string {
	if filePath == "" {
		return ""
	}

	filePath = repetitionFileKey(filePath)

	rt.mu.Lock()
	defer rt.mu.Unlock()

	if rt.failedEditsByFile[filePath] > 0 {
		rt.readAfterFailedEdit[filePath]++
	}

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

func (rt *repetitionTracker) checkEscalation(filePath string, count int) string {

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

func (rt *repetitionTracker) reset() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.failedEditsByFile = make(map[string]int)
	rt.firedLevels = make(map[string]bool)
	rt.readAfterFailedEdit = make(map[string]int)
}

func normalizeFilePath(path string) string {
	path = strings.TrimSpace(path)

	for strings.HasPrefix(path, "./") {
		path = path[2:]
	}

	// #5290: the unconditional strings.ToLower collapsed case-distinct
	// paths on case-sensitive filesystems (linux), merging distinct files
	// in repetition/premature-commit tracking. Mirror the batch-conflict
	// normalization: fold only on case-insensitive platforms, ASCII-only.
	if batchPathFoldActive() {
		return asciiFoldLower(path)
	}
	return path
}

func truncateRunesSafe(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

func repetitionFileKey(path string) string {
	p := strings.TrimSuffix(normalizeFilePath(path), "...")
	return truncateRunesSafe(p, 77)
}

func (a *Agent) repetitionCheckEdit(toolName string, args json.RawMessage, isError bool) string {
	if a.repetition == nil {
		return ""
	}
	return a.repetition.recordEditAttempt(toolName, args, isError)
}

func (a *Agent) repetitionCheckRead(filePath string) string {
	if a.repetition == nil {
		return ""
	}
	return a.repetition.recordReadAttempt(filePath)
}

func (a *Agent) resetRepetitionTracker() {
	if a.repetition == nil {
		return
	}
	a.repetition.reset()
}
