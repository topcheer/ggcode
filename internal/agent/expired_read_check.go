package agent

// Expired Read Warning -- Self-Invalidated Context Detection
//
// Research basis: "Reducing Cost of LLM Agents with Trajectory Reduction"
// (AgentDiet, arXiv:2509.23586, FSE 2026) identifies three categories of
// trajectory waste: useless, redundant, and EXPIRED information. Expired
// information is tool output that was valid when produced but has been
// invalidated by subsequent actions. The paper shows 39.9%-59.7% of input
// tokens in agent trajectories are waste, with expired reads being a major
// contributor.
//
// In coding agents, the most common expired-information pattern is:
//   1. Agent reads file A (content enters context window)
//   2. Agent edits file A (the read content is now stale)
//   3. Agent continues working, referencing the old read content from
//      "memory" rather than re-reading the updated version
//
// This is subtly different from existing guards:
//   - unread_edit_guard.go: warns when editing a file NOT previously read
//   - redundant_read_guard.go: warns when re-reading unchanged files
//   - stale_read (in unread_edit_guard): warns when file changed on disk
//     due to EXTERNAL modification between read and edit
//
// THIS detector fills the orthogonal gap: warns when the agent EDITS a file
// it previously read, explicitly marking the prior read as EXPIRED in the
// agent's context. This serves as a "context invalidation notice" -- the
// agent knows its cached view of the file is no longer trustworthy and any
// future references to that file's content should be re-read first.
//
// Additionally, this detector tracks a converse failure: when the agent
// re-reads a file it JUST edited, that re-read wastes context budget
// because the edit result already contains the updated content.
//
// Design:
//   - Zero LLM cost -- pure map lookups + sequence tracking
//   - Fires at most once per file per run for the expiry notice
//   - Fires at most once per file per run for the post-edit re-read notice
//   - Non-blocking: hint appended to edit result, execution proceeds

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// maxExpiredReadWarnings: cap total notices per run to avoid flooding.
	maxExpiredReadWarnings = 3

	// postEditRereadGap: maximum sequence gap between edit and re-read
	// for the re-read to be considered wasteful. Beyond this, the edit
	// result may have been compacted away, making a re-read legitimate.
	postEditRereadGap = 3
)

// expiredReadState tracks self-invalidated reads (files read then edited).
type expiredReadState struct {
	// readBeforeEdit tracks normalized paths of files that were read and
	// subsequently edited in this run.
	readBeforeEdit map[string]bool

	// expiredWarned tracks files for which the expiry notice has fired.
	expiredWarned map[string]bool

	// editedFiles tracks files edited in this run, mapped to the sequence
	// number of the last edit. Used for post-edit re-read detection.
	editedFiles map[string]int

	// rereadWarned tracks files for which the post-edit re-read notice fired.
	rereadWarned map[string]bool

	// warningCount is the total notices issued this run.
	warningCount int

	// seq is a monotonically increasing counter for event ordering.
	seq int
}

func newExpiredReadState() *expiredReadState {
	return &expiredReadState{
		readBeforeEdit: make(map[string]bool),
		expiredWarned:  make(map[string]bool),
		editedFiles:    make(map[string]int),
		rereadWarned:   make(map[string]bool),
	}
}

func (e *expiredReadState) reset() {
	e.readBeforeEdit = make(map[string]bool)
	e.expiredWarned = make(map[string]bool)
	e.editedFiles = make(map[string]int)
	e.rereadWarned = make(map[string]bool)
	e.warningCount = 0
	e.seq = 0
}

// recordRead is called when the agent reads a file via read_file or similar.
// It records the path so we can later detect self-invalidation on edit.
func (e *expiredReadState) recordRead(path string) {
	if path == "" {
		return
	}
	n := normalizePath(path)
	// Mark that this file has been read at least once (for expiry detection).
	// Only track if not yet edited -- if already edited, the prior read is
	// already expired and this re-read will be caught by checkPostEditReread.
	if _, edited := e.editedFiles[n]; !edited {
		e.readBeforeEdit[n] = true
	}
}

// recordEdit is called when the agent edits a file. It returns a hint
// if the file was previously read, marking that read as expired.
func (e *expiredReadState) recordEdit(path string) string {
	if path == "" {
		return ""
	}
	n := normalizePath(path)

	// Record this edit with the current sequence number.
	e.seq++
	e.editedFiles[n] = e.seq

	// Check if the file was read before this edit.
	if !e.readBeforeEdit[n] {
		return "" // No prior read to expire.
	}

	// Already warned about this file -- don't nag.
	if e.expiredWarned[n] {
		return ""
	}

	// Cap total warnings.
	if e.warningCount >= maxExpiredReadWarnings {
		return ""
	}

	e.expiredWarned[n] = true
	e.warningCount++

	debug.Log("agent", "expired-read: %s was read earlier and is now edited (prior read is expired)", n)

	return fmt.Sprintf(
		"[expired-read] %s: your pre-edit read is stale; the edit result above contains the updated content - reference it instead of re-reading.",
		path,
	)
}

// checkPostEditReread is called when the agent reads a file. It returns a
// hint if the file was recently edited, indicating the edit result already
// contains the updated content and a re-read wastes context budget.
func (e *expiredReadState) checkPostEditReread(path string) string {
	if path == "" {
		return ""
	}
	n := normalizePath(path)

	// Was this file edited?
	editSeq, wasEdited := e.editedFiles[n]
	if !wasEdited {
		return ""
	}

	// Already warned -- don't nag.
	if e.rereadWarned[n] {
		return ""
	}

	// Only warn if the re-read happens shortly after the edit (within
	// postEditRereadGap sequence ticks). Beyond that, the edit result may
	// have been compacted away, making a re-read legitimate.
	e.seq++
	gap := e.seq - editSeq
	if gap > postEditRereadGap {
		return ""
	}

	// Cap total warnings.
	if e.warningCount >= maxExpiredReadWarnings {
		return ""
	}

	e.rereadWarned[n] = true
	e.warningCount++

	debug.Log("agent", "expired-read: %s re-read %d ticks after edit (edit result has current content)", n, gap)

	return fmt.Sprintf(
		"[Context hint] %s was edited recently -- the edit result already contains the updated content. "+
			"Re-reading wastes context budget. Reference the edit result instead.",
		path,
	)
}
