package agent

// Redundant Read Guard - Context Waste Prevention
//
// Research basis: Anthropic's "Effective Context Engineering for Agents" (2025)
// identifies context window waste as a primary efficiency bottleneck. The ACE
// framework (ICLR 2026) catalogues "context waste patterns" where redundant
// information crowds out useful context, shortening the effective context budget.
//
// One of the most common waste patterns in coding agents is redundant file
// re-reads: the agent reads a file, then reads it again 1-2 iterations later
// without having edited it (or having had it changed externally). Each re-read
// injects the full file content into the context window, consuming budget that
// could be used for new information.
//
// Competitor approaches:
//   - Claude Code: no preventive re-read detection (relies on compaction after the fact)
//   - Cursor: manages context implicitly via editor state (not applicable to CLI agents)
//   - Aider: tracks files in chat context; user explicitly manages which files are "in scope"
//   - OpenHands: has post-hoc context compaction but no pre-read waste prevention
//
// ggcode already has superseded-reads compaction (internal/context), which
// removes old read results AFTER the fact. But compaction is reactive - it only
// fires when context pressure is high. This guard is PROACTIVE: it warns the
// agent at read time that the content is likely already in context, preventing
// the waste from occurring in the first place.
//
// Design:
//   - Tracks files read during the current run (shared with unreadEditState).
//   - Detects re-reads where the file has NOT been edited since the last read
//     (file mtime unchanged), meaning the agent already has the current content.
//   - Fires at most once per file per run (avoids nagging).
//   - Only triggers for files above a minimum size threshold (tiny files waste
//     negligible context; warning about them adds noise).
//   - Zero LLM cost - deterministic file stat + map lookup.

import (
	"fmt"
	"os"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// redundantReadMinSize: only warn for files above this size. Small files
	// (<2KB) waste negligible context budget; warning about them adds more
	// noise than the re-read itself.
	redundantReadMinSize = 2048
)

// redundantReadState tracks re-reads for context waste prevention.
type redundantReadState struct {
	// warnedFiles tracks files for which the redundant-read hint has fired.
	// Prevents duplicate warnings within a run.
	warnedFiles map[string]bool

	// lastReadMtime stores the mtime captured at the most recent read, keyed
	// by normalized path. Used to determine whether the file has changed since
	// the last read - if unchanged, the re-read is redundant.
	lastReadMtime map[string]int64
}

func newRedundantReadState() *redundantReadState {
	return &redundantReadState{
		warnedFiles:   make(map[string]bool),
		lastReadMtime: make(map[string]int64),
	}
}

func (r *redundantReadState) reset() {
	r.warnedFiles = make(map[string]bool)
	r.lastReadMtime = make(map[string]int64)
}

// checkRedundantRead returns a non-empty hint if the agent is re-reading a file
// whose content hasn't changed since the last read in this run, indicating the
// content is already in context and the re-read wastes context budget.
//

// Returns "" if the read is not redundant (first read, file changed, already
// warned, or file too small to matter).
func (r *redundantReadState) checkRedundantRead(path string, partial bool) string {
	if path == "" {
		return ""
	}
	// #463: windowed reads (offset/limit) fetch a DIFFERENT chunk of a
	// large file — path-level "already in context" logic does not apply.
	// #626: a partial read must NOT seed the mtime baseline either. Only a
	// fragment of the file is in context, so a later FULL read of the same
	// file is not redundant — seeding the baseline here used to make the
	// full read get mis-flagged as "already read" even though the context
	// held just a slice. Partial reads step aside entirely.
	if partial {
		return ""
	}
	n := normalizePath(path)

	// Already warned about this file — don't nag.
	if r.warnedFiles[n] {
		return ""
	}

	// Check if we have a prior mtime for this file.
	prevMtime, hadPrior := r.lastReadMtime[n]
	if !hadPrior {
		// First read in this run — not redundant. Record mtime for future checks.
		r.recordReadMtime(path)
		return ""
	}

	// Get current file info.
	info, err := os.Stat(path)
	if err != nil {
		// Can't stat — can't determine redundancy. Let the read proceed.
		return ""
	}

	// File too small to waste meaningful context budget.
	if info.Size() < redundantReadMinSize {
		return ""
	}

	// If the file's mtime changed since the last read, it was modified
	// externally (or by the agent via a different path). The re-read is
	// legitimate - the agent needs the updated content.
	if info.ModTime().UnixNano() != prevMtime {
		r.recordReadMtime(path)
		return ""
	}

	// Redundant re-read detected: file hasn't changed since the last read.
	r.warnedFiles[n] = true
	debug.Log("agent", "redundant-read guard: %s re-read without changes (%d bytes already in context)", n, info.Size())

	sizeKB := info.Size() / 1024
	return fmt.Sprintf(
		"[context-hint] %s already read (%dKB in context). Use earlier read or grep for specific lines.",
		path, sizeKB,
	)
}

// recordReadMtime captures the file's modification time for future redundancy checks.
func (r *redundantReadState) recordReadMtime(path string) {
	if path == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	n := normalizePath(path)
	r.lastReadMtime[n] = info.ModTime().UnixNano()
}

// recordWrite clears the redundancy state for a file after it's been edited,
// so a subsequent read of the modified file is not flagged as redundant.
func (r *redundantReadState) recordWrite(path string) {
	if path == "" {
		return
	}
	n := normalizePath(path)
	// Clear the prior mtime so the next read is treated as a fresh read
	// (the file was modified by the agent, so re-reading is legitimate).
	delete(r.lastReadMtime, n)
	// Also clear the warned flag so if the agent reads it again later without
	// further edits, it can be flagged again.
	delete(r.warnedFiles, n)
}
