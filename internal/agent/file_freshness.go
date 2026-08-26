package agent

// File Freshness Sentinel -- Proactive Cross-Iteration External Change Detection
//
// Research basis: "Lost in the Middle" (Liu et al., 2023) and follow-up work on
// agent reliability (AgentMarketCap 2026 survey) show that stale context is a
// leading cause of agent failure. When files change on disk between iterations
// -- from build tools, formatters-on-save, git operations, other agents/IDEs,
// or shared filesystem edits -- the agent's cached mental model diverges from
// reality. It reasons about outdated code, plans edits against stale content,
// and generates old_text anchors that no longer match.
//
// Existing ggcode systems that are CLOSE but don't cover this gap:
//
//   - unread_edit_guard.go (checkStaleRead): REACTIVE -- only fires at edit time,
//     after the agent has already decided to edit. By then, the agent may have
//     wasted several iterations reasoning about stale content, or generated an
//     edit plan based on outdated understanding.
//   - file_change_detect (tool layer): detects changes from COMMAND output only.
//     Doesn't detect changes from external processes (IDE save, formatter, git).
//   - change_reconcile.go: captures pre-run dirty-file state for POST-RUN
//     reconciliation. Doesn't monitor during the run.
//   - command_cache.go / memoize.go: invalidate caches on ANY file edit, but
//     only edits made by the AGENT's own tools -- not external modifications.
//
// Competitor analysis:
//   - Claude Code: no proactive external file change detection; relies on
//     git status checks at edit time
//   - Cursor: IDE-integrated, detects file changes but only for its own
//     file-editing surface, not for autonomous agent runs
//   - OpenHands: has a "file refresh" mechanism but only when the agent
//     explicitly requests a file, not proactively between iterations
//   - Devin: monitors file changes in its sandbox but doesn't notify the
//     agent proactively
//   - Aider: operates on a single git commit at a time, so external changes
//     are caught by git diff on next operation
//
// This sentinel provides PROACTIVE awareness: at each iteration boundary,
// it checks whether any file the agent has previously read has been modified
// on disk since that read. If so, it injects a compact notification listing
// the changed files -- BEFORE the agent uses stale information.
//
// Design constraints:
//   - Zero LLM cost (deterministic stat() checks)
//   - Fires at most once per changed-file per run (avoids spam)
//   - Only checks files that were actually READ (not just listed or grepped)
//   - Skips files that the agent itself created/edited after reading
//   - O(n) stat() calls where n = unique files read (typically 5-30)
//   - Skipped entirely when no files have been read yet
//   - Throttled to check at most every freshnessCheckInterval iterations

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// freshnessCheckInterval: run the sentinel check every N iterations.
	// Checking every iteration is wasteful when no external changes occur;
	// every 3rd iteration balances responsiveness with overhead.
	freshnessCheckInterval = 3

	// freshnessMaxFiles limits the number of changed files listed in a single
	// notification to avoid context flooding during large external refactors.
	freshnessMaxFiles = 8
)

// fileFreshnessSentinel tracks files the agent has read and detects external
// modifications between iterations. It reuses the read-mtime data collected
// by the unread-edit guard, avoiding redundant tracking.
type fileFreshnessSentinel struct {
	// readMtimes maps normalized file path → mtime at last read.
	// Populated by recordRead via the shared tracking in unreadEditState.
	readMtimes map[string]time.Time

	// writeMtimes maps normalized file path → mtime when the agent wrote it.
	// Used to exempt files modified by the agent itself from stale detection.
	// Issue #1055: changed from boolean agentWritten to mtime tracking so that
	// external changes (formatters, concurrent edits) AFTER the agent's write
	// are still detected.
	writeMtimes map[string]time.Time

	// notified tracks files we've already sent a stale notification for.
	// Each file is notified at most once per run.
	notified map[string]bool

	// lastCheckIter tracks which iteration we last ran the check.
	lastCheckIter int
}

func newFileFreshnessSentinel() *fileFreshnessSentinel {
	return &fileFreshnessSentinel{
		readMtimes:  make(map[string]time.Time),
		writeMtimes: make(map[string]time.Time),
		notified:    make(map[string]bool),
	}
}

// reset clears all state for a new run.
func (s *fileFreshnessSentinel) reset() {
	s.readMtimes = make(map[string]time.Time)
	s.writeMtimes = make(map[string]time.Time)
	s.notified = make(map[string]bool)
	s.lastCheckIter = 0
}

// recordRead registers that a file was read, recording its current on-disk
// mtime. We use the file's actual mtime (not wall-clock time) so that
// external changes before the read are treated as "already known" and only
// changes AFTER the read trigger stale detection.
func (s *fileFreshnessSentinel) recordRead(path string) {
	if path == "" {
		return
	}
	n := normalizePath(path)
	info, err := os.Stat(path)
	if err != nil {
		s.readMtimes[n] = time.Now()
	} else {
		s.readMtimes[n] = info.ModTime()
	}
	// If the agent reads a file after writing it, clear the writeMtime entry
	// since the read refreshes the agent's understanding.
	delete(s.writeMtimes, n)
	// Clear any prior stale notification for this file -- the agent has
	// re-read it, so it is now up to date.
	delete(s.notified, n)
}

// recordWrite records the mtime when the agent writes a file. Files modified
// by the agent at the same or later time than the read are exempt from stale
// detection. Issue #1055: changed from boolean to mtime tracking so that
// external changes AFTER the agent's write (e.g., formatters, concurrent edits)
// are still detected.
func (s *fileFreshnessSentinel) recordWrite(path string) {
	if path == "" {
		return
	}
	n := normalizePath(path)
	info, err := os.Stat(path)
	if err != nil {
		s.writeMtimes[n] = time.Now()
	} else {
		s.writeMtimes[n] = info.ModTime()
	}
}

// maybeCheckStaleFiles runs the proactive freshness check at iteration
// boundaries. Returns a non-empty notification message if external changes
// were detected, or "" if no changes or the check was skipped this iteration.
//
// iteration is the current 1-based iteration number.
func (s *fileFreshnessSentinel) maybeCheckStaleFiles(iteration int) string {
	// Throttle: only check every freshnessCheckInterval iterations.
	if iteration > 1 && (iteration-s.lastCheckIter) < freshnessCheckInterval {
		return ""
	}
	s.lastCheckIter = iteration

	if len(s.readMtimes) == 0 {
		return ""
	}

	var changed []string
	for path, readAt := range s.readMtimes {
		// Skip files already notified about.
		if s.notified[path] {
			continue
		}

		info, err := os.Stat(path)
		if err != nil {
			continue // File may not exist (e.g., temp file, deleted externally)
		}

		// Issue #1055: skip files modified by the agent itself (write mtime >= read mtime)
		// but DO detect external changes AFTER the agent's write (current mtime > write mtime)
		writeAt, ok := s.writeMtimes[path]
		if ok && info.ModTime().Equal(writeAt) || (ok && info.ModTime().Before(writeAt)) {
			continue
		}

		if info.ModTime().After(readAt) {
			changed = append(changed, path)
			s.notified[path] = true
		}
	}

	if len(changed) == 0 {
		return ""
	}

	// Build a compact notification.
	shown := changed
	truncated := 0
	if len(shown) > freshnessMaxFiles {
		shown = shown[:freshnessMaxFiles]
		truncated = len(changed) - freshnessMaxFiles
	}

	// Shorten paths for readability -- strip common working directory prefix.
	var lines []string
	for _, p := range shown {
		short := shortenForDisplay(p)
		lines = append(lines, "  - "+short)
	}

	msg := fmt.Sprintf(
		"[file-watch] %d file(s) changed on disk since you last read them. "+
			"Your cached understanding may be stale -- re-read before editing:\n%s",
		len(changed), strings.Join(lines, "\n"),
	)
	if truncated > 0 {
		msg += fmt.Sprintf("\n  ... and %d more", truncated)
	}

	debug.Log("agent", "file freshness sentinel: %d externally modified files detected at iteration %d", len(changed), iteration)
	return msg
}

// shortenForDisplay reduces long absolute paths for readability in the
// notification message. Uses the last 2-3 path components.
func shortenForDisplay(path string) string {
	// Try to make it relative if it looks like an absolute path.
	// Otherwise just show the last few components.
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) <= 3 {
		return filepath.ToSlash(path)
	}
	// Show last 3 components with ... prefix.
	return ".../" + strings.Join(parts[len(parts)-3:], "/")
}
