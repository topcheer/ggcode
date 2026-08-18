package checkpoint

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

// Checkpoint represents a saved file state before a tool edit.
type Checkpoint struct {
	ID         string `json:"id"`
	FilePath   string `json:"file_path"`
	OldContent string `json:"old_content"`
	NewContent string `json:"new_content"`
	// Existed records whether the file existed on disk before the tool call
	// that produced this checkpoint. For write_file on a missing path it is
	// false and OldContent is "", so undo must REMOVE the file rather than
	// write back an empty buffer (which leaves a stray 0-byte file that can
	// break builds). OldContent=="" alone cannot distinguish a newly created
	// file from a pre-existing empty file (empty __init__.py, .gitkeep) —
	// that conflation is issue #554 B/C. Manager.Save defaults it to true,
	// which is correct for edit_file (its compute path fails when the file
	// cannot be read).
	Existed   bool      `json:"existed,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	ToolCall  string    `json:"tool_call"`
	RunID     string    `json:"run_id,omitempty"` // agent run that created this checkpoint
}

// Correction represents a user-initiated undo of agent file changes.
// It captures which files were reverted and the tool that made the original
// change, enabling the agent to learn from the rejection and try a different
// approach on the next run.
type Correction struct {
	Files    []string  // file paths that were reverted
	ToolCall string    // the tool call that made the original change
	RunID    string    // the run ID whose changes were reverted
	Time     time.Time // when the correction occurred
}

// Manager manages file checkpoints for undo/redo support.
type Manager struct {
	checkpoints    []Checkpoint
	redoStack      []Checkpoint // checkpoints popped by Undo, available for Redo
	maxCheckpoints int
	mu             sync.Mutex
	currentRunID   string // active run ID, set by StartRun

	// evictedRuns records run IDs that lost checkpoints to the
	// maxCheckpoints FIFO eviction. UndoRun refuses to batch-revert a run
	// whose earliest checkpoints were evicted: the earliest surviving
	// checkpoint is then a mid-run state, not the pre-run baseline, and
	// writing it back would silently corrupt the file (issue #517).
	evictedRuns map[string]bool

	// corrections records user-initiated undos so the agent can be told its
	// previous approach was rejected. Cleared at the start of each new run.
	corrections []Correction
}

// NewManager creates a new checkpoint manager with the given max limit.
func NewManager(maxCheckpoints int) *Manager {
	if maxCheckpoints <= 0 {
		maxCheckpoints = 50
	}
	return &Manager{maxCheckpoints: maxCheckpoints}
}

// StartRun marks the beginning of a new agent run. All subsequent Save()
// calls are tagged with runID until the next StartRun. This enables
// UndoRun() to batch-revert all file changes from a single run.
func (m *Manager) StartRun(runID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentRunID = runID
}

// Save records a checkpoint before a file edit.
// A new edit invalidates the redo history (standard undo/redo semantics).
// It assumes the file existed before the edit (true for edit_file, whose
// compute path fails when the file cannot be read). Callers that can create
// files (write_file) must call SaveWithExistence with the real stat result so
// undo removes the file instead of writing back "" (issue #554 B).
func (m *Manager) Save(filePath, oldContent, newContent, toolCall string) Checkpoint {
	return m.SaveWithExistence(filePath, oldContent, newContent, toolCall, true)
}

// SaveWithExistence is Save with an explicit existed-before-edit flag.
// existed=false marks a file-creating edit: OldContent is "" because the file
// was absent, and undo restores that absence by deleting the file
// (issue #554 B).
func (m *Manager) SaveWithExistence(filePath, oldContent, newContent, toolCall string, existed bool) Checkpoint {
	m.mu.Lock()
	defer m.mu.Unlock()

	cp := Checkpoint{
		ID:         generateID(),
		FilePath:   filePath,
		OldContent: oldContent,
		NewContent: newContent,
		Existed:    existed,
		Timestamp:  time.Now(),
		ToolCall:   toolCall,
		RunID:      m.currentRunID,
	}

	m.checkpoints = append(m.checkpoints, cp)

	// Evict oldest if over limit. Prefer evicting entries that do NOT
	// belong to the active run, so the tail run segment — and with it the
	// pre-run baseline UndoRun relies on — stays intact as long as possible
	// (issue #517). When every entry belongs to the active run, eviction is
	// unavoidable; the run is then flagged so UndoRun can refuse instead of
	// silently rolling back to a mid-run state.
	for len(m.checkpoints) > m.maxCheckpoints {
		evictIdx := 0
		for i, cp := range m.checkpoints {
			if cp.RunID != m.currentRunID {
				evictIdx = i
				break
			}
		}
		if m.evictedRuns == nil {
			m.evictedRuns = make(map[string]bool)
		}
		// FIFO eviction removes a run's earliest surviving entry, which is
		// either its true baseline (now lost) or evidence the baseline was
		// already lost. Either way that run's UndoRun is no longer trustworthy.
		m.evictedRuns[m.checkpoints[evictIdx].RunID] = true
		m.checkpoints = append(m.checkpoints[:evictIdx], m.checkpoints[evictIdx+1:]...)
	}

	// New edit invalidates redo history
	m.redoStack = nil

	return cp
}

// Undo rolls back the most recent checkpoint by writing OldContent back to the file.
// The undone checkpoint is pushed onto the redo stack so it can be re-applied.
func (m *Manager) Undo() (*Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.checkpoints) == 0 {
		return nil, fmt.Errorf("no checkpoints to undo")
	}

	// Copy the checkpoint value before truncating the slice. Without this copy,
	// the returned pointer would alias the backing array slot that a subsequent
	// Save() call could overwrite (append into the truncated capacity).
	cp := m.checkpoints[len(m.checkpoints)-1]

	// Restore the pre-edit state. A checkpoint with existed=false captured a
	// file creation, so the restored state is "file missing" — remove it;
	// writing "" back would leave a stray 0-byte file (issue #554 B).
	if err := restoreCheckpointState(cp.FilePath, cp.OldContent, cp.Existed); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	m.checkpoints = m.checkpoints[:len(m.checkpoints)-1]
	m.redoStack = append(m.redoStack, cp)

	// Record the correction so the agent can learn from the rejection.
	m.corrections = append(m.corrections, Correction{
		Files:    []string{cp.FilePath},
		ToolCall: cp.ToolCall,
		RunID:    cp.RunID,
		Time:     time.Now(),
	})

	return &cp, nil
}

// Revert rolls back to a specific checkpoint by ID, restoring EVERY file
// touched by that checkpoint or any of its successors to its state at the
// moment just before the target checkpoint ran, then removes those
// checkpoints.
// Unlike Undo (which reverts the most recent checkpoint), Revert jumps to an
// arbitrary past state, so the redo stack is cleared (standard undo/redo semantics).
// A Correction is recorded so the agent can learn from this rejection (#574 Bug G).
//
// #678: the pre-fix implementation restored only the target checkpoint's own
// file while still truncating ALL later checkpoints. In a multi-file run
// (cp1 edits f1, cp2 edits f2, cp3 edits f1 again), Revert(cp2) left f1 at its
// cp3 state on disk, deleted cp3 (the record needed to undo that state),
// recorded a Correction claiming BOTH files were reverted, and produced a
// mixed disk state that never existed. UndoRun already had the correct
// semantics (per-file baseline write-back); Revert now follows the same
// pattern.
func (m *Manager) Revert(id string) (*Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx := -1
	for i, cp := range m.checkpoints {
		if cp.ID == id {
			idx = i
			break
		}
	}

	if idx < 0 {
		return nil, fmt.Errorf("checkpoint %q not found", id)
	}

	// Copy the checkpoint value before truncating the slice to avoid aliasing
	// the backing array (see Undo for details).
	cp := m.checkpoints[idx]

	// Compute, for every file touched at idx or later, the state it was in at
	// the revert moment (just before the target checkpoint's edit ran):
	//   - a file whose latest pre-idx checkpoint is cp_prev was last written
	//     by that edit, so its state is cp_prev.NewContent (the file exists
	//     after any edit, hence existed=true even when cp_prev created it);
	//   - a file with no pre-idx checkpoint is entering its FIRST edit of the
	//     surviving history, so its pre-state is the first idx-or-later
	//     checkpoint's OldContent/Existed pair.
	targets := make(map[string]baselineState)
	for i := idx; i < len(m.checkpoints); i++ {
		f := m.checkpoints[i].FilePath
		if _, ok := targets[f]; ok {
			continue
		}
		state := baselineState{content: m.checkpoints[i].OldContent, existed: m.checkpoints[i].Existed}
		for j := idx - 1; j >= 0; j-- {
			if m.checkpoints[j].FilePath == f {
				state = baselineState{content: m.checkpoints[j].NewContent, existed: true}
				break
			}
		}
		targets[f] = state
	}

	// Write every file's revert-moment state back to disk BEFORE truncating
	// history: only when all writes succeed does disk match the pre-idx
	// moment. On failure the checkpoint list is left intact so the caller can
	// retry or fall back to single-step Undo — a partial truncation would
	// strand the not-yet-restored files exactly like #678.
	files := make([]string, 0, len(targets))
	for f, st := range targets {
		if err := restoreCheckpointState(f, st.content, st.existed); err != nil {
			return nil, fmt.Errorf("failed to revert %s: %w", f, err)
		}
		files = append(files, f)
	}

	m.checkpoints = m.checkpoints[:idx]
	// Jumping to a past state invalidates the redo history, exactly like a
	// new Save does. Without this, Undo → Revert → Redo would re-apply a
	// checkpoint the user explicitly rolled back past, ending in a state the
	// user rejected (issue #554 E).
	m.redoStack = nil

	// Record the correction so the agent can learn from the rejection (#574 Bug G).
	// Use the ToolCall from the reverted checkpoint to identify what was rejected.
	// Files is now honest: every listed file was actually written back.
	m.corrections = append(m.corrections, Correction{
		Files:    files,
		ToolCall: cp.ToolCall,
		RunID:    cp.RunID,
		Time:     time.Now(),
	})

	return &cp, nil
}

// restoreCheckpointState writes oldContent back to path. When the checkpoint
// recorded a file creation (file absent before the edit), the pre-edit state
// is "missing", so the file is removed instead — a write would leave a stray
// 0-byte file (issue #554 B). Removal tolerates an already-gone file so undo
// stays idempotent.
func restoreCheckpointState(path, oldContent string, existed bool) error {
	if !existed && oldContent == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return util.AtomicWriteFile(path, []byte(oldContent), 0644)
}

// FileSummary summarizes the edits made to a single file.
type FileSummary struct {
	Path     string
	Edits    int
	LastTool string
	IsNew    bool // true if the file did not exist before its first checkpoint
}

// ModifiedFiles returns a summary of unique files modified via checkpoints,
// ordered by first modification time (oldest first).
func (m *Manager) ModifiedFiles() []FileSummary {
	m.mu.Lock()
	defer m.mu.Unlock()

	order := make([]string, 0, len(m.checkpoints))
	summary := make(map[string]*FileSummary)

	for _, cp := range m.checkpoints {
		fs, ok := summary[cp.FilePath]
		if !ok {
			fs = &FileSummary{
				Path:     cp.FilePath,
				IsNew:    !cp.Existed, // absent before the first checkpoint — NOT merely empty OldContent, which also matches pre-existing empty files (issue #554 C)
				LastTool: cp.ToolCall,
			}
			summary[cp.FilePath] = fs
			order = append(order, cp.FilePath)
		}
		fs.Edits++
		fs.LastTool = cp.ToolCall
	}

	out := make([]FileSummary, 0, len(order))
	for _, p := range order {
		out = append(out, *summary[p])
	}
	return out
}

// UndoRun reverts all checkpoints belonging to the most recent run in one
// batch operation. It identifies the run ID of the last checkpoint, then
// reverts every checkpoint with that run ID, writing each file's original
// (pre-run) content back to disk. The reverted checkpoints are pushed onto
// the redo stack in reverse order so Redo() can re-apply them one at a time.
//
// Returns the reverted checkpoints (in revert order: last-to-first) and nil
// on success. If no checkpoints exist, returns an error.
//
// Files are reverted to their state at the FIRST checkpoint of the run for
// each unique file path — this is the pre-run baseline.
func (m *Manager) UndoRun() ([]Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.checkpoints) == 0 {
		return nil, fmt.Errorf("no checkpoints to undo")
	}

	// Identify the run ID of the most recent checkpoint.
	runID := m.checkpoints[len(m.checkpoints)-1].RunID

	// Refuse when FIFO eviction has truncated this run: the earliest
	// surviving checkpoint is a mid-run state, not the pre-run baseline, and
	// writing it back would silently roll the file back to the wrong state
	// (issue #517 Bug B). Refusal happens before any disk writes.
	if m.evictedRuns[runID] {
		return nil, fmt.Errorf(
			"refusing to undo run %q: its earliest checkpoints were evicted by the %d-checkpoint limit, "+
				"so the pre-run baseline is no longer recoverable (rolling back would restore a mid-run state); "+
				"use single-step Undo instead",
			runID, m.maxCheckpoints)
	}

	runIndices := m.runSegmentIndices(runID)
	if len(runIndices) == 0 {
		return nil, fmt.Errorf("no checkpoints in current run")
	}

	// Issue #554 D: runSegmentIndices stops at the first checkpoint of a
	// DIFFERENT run when scanning backward from the tail, so any checkpoints
	// of the same run BEFORE that boundary (a mid-list segment, left behind
	// e.g. by writeBaselines' partial-failure cleanup removing only some
	// files' entries) are invisible to it. Undoing only the tail segment would
	// restore a mid-run state as if it were the pre-run baseline — the same
	// silent corruption #517 guards against. Refuse instead.
	tailStart := runIndices[len(runIndices)-1] // earliest index in the tail segment
	for i := 0; i < tailStart; i++ {
		if m.checkpoints[i].RunID == runID {
			return nil, fmt.Errorf(
				"refusing to undo run %q: its checkpoints are split into non-contiguous segments (mid-run entries survive before the tail segment, e.g. after a partial failure cleanup), so the pre-run baseline is no longer recoverable; use single-step Undo instead",
				runID)
		}
	}
	baselines := m.preRunBaselines(runIndices)

	// Write baseline content for each unique file.
	reverted, failErr := m.writeBaselines(runIndices, baselines, runID)
	if failErr != nil {
		return reverted, failErr
	}

	// Remove all run checkpoints from the list and push onto redo stack.
	cutoff := runIndices[len(runIndices)-1] // earliest index in this run
	removed := make([]Checkpoint, len(m.checkpoints[cutoff:]))
	copy(removed, m.checkpoints[cutoff:])
	m.checkpoints = m.checkpoints[:cutoff]
	delete(m.evictedRuns, runID) // hygiene: run fully undone, flag no longer meaningful
	// Push in reverse so Redo() re-applies in original order.
	for i := len(removed) - 1; i >= 0; i-- {
		m.redoStack = append(m.redoStack, removed[i])
	}

	m.recordRunCorrection(reverted, runID)

	return reverted, nil
}

// runSegmentIndices returns the indices of checkpoints belonging to runID,
// collected from the end backward (last-to-first). Must hold m.mu.
func (m *Manager) runSegmentIndices(runID string) []int {
	var runIndices []int
	for i := len(m.checkpoints) - 1; i >= 0; i-- {
		if m.checkpoints[i].RunID != runID {
			break
		}
		runIndices = append(runIndices, i)
	}
	return runIndices
}

// preRunBaselines maps each unique file path in the run to the baseline of
// its FIRST checkpoint — the pre-run state. runIndices is in reverse order
// (last first), so the first occurrence in forward order gives the earliest
// checkpoint per file. Must hold m.mu.
func (m *Manager) preRunBaselines(runIndices []int) map[string]baselineState {
	baselines := make(map[string]baselineState)
	for i := len(runIndices) - 1; i >= 0; i-- {
		cp := m.checkpoints[runIndices[i]]
		if _, exists := baselines[cp.FilePath]; !exists {
			baselines[cp.FilePath] = baselineState{content: cp.OldContent, existed: cp.Existed}
		}
	}
	return baselines
}

// baselineState is a file's pre-run state: its original content, or absence
// when the run created the file (existed=false, content=="").
type baselineState struct {
	content string
	existed bool
}

// writeBaselines writes each unique file's pre-run baseline to disk, once
// per file, iterating runIndices last-to-first. On the first write failure it
// removes ALL checkpoints of the already-reverted files in this run (not just
// one per file) to keep metadata consistent with disk state — leaving
// mid-run entries behind would let a later single-step Undo re-apply a
// mid-run state on top of the rolled-back baseline (issue #517 Bug A) — and
// returns the partially reverted checkpoints plus the error. Must hold m.mu.
func (m *Manager) writeBaselines(runIndices []int, baselines map[string]baselineState, runID string) ([]Checkpoint, error) {
	var reverted []Checkpoint
	revertedFiles := make(map[string]bool)
	for _, idx := range runIndices {
		cp := m.checkpoints[idx]
		if revertedFiles[cp.FilePath] {
			continue
		}
		// Restore the file's pre-run state. When the run created the file,
		// the baseline is "absent" and the file is removed rather than
		// written back as a 0-byte file (issue #554 B).
		if bl, ok := baselines[cp.FilePath]; ok {
			if err := restoreCheckpointState(cp.FilePath, bl.content, bl.existed); err != nil {
				m.removeRunCheckpointsFor(reverted, runID)
				return reverted, fmt.Errorf("failed to revert %s: %w", cp.FilePath, err)
			}
		}
		revertedFiles[cp.FilePath] = true
		reverted = append(reverted, cp)
	}
	return reverted, nil
}

// removeRunCheckpointsFor deletes every checkpoint of the given files that
// belongs to runID, scanning from the tail. Must hold m.mu.
func (m *Manager) removeRunCheckpointsFor(cps []Checkpoint, runID string) {
	for _, r := range cps {
		for j := len(m.checkpoints) - 1; j >= 0; j-- {
			if m.checkpoints[j].FilePath == r.FilePath && m.checkpoints[j].RunID == runID {
				m.checkpoints = append(m.checkpoints[:j], m.checkpoints[j+1:]...)
			}
		}
	}
}

// recordRunCorrection appends a Correction covering the unique files of a
// fully reverted run so the agent can learn from the rejection.
// Must hold m.mu.
func (m *Manager) recordRunCorrection(reverted []Checkpoint, runID string) {
	fileSet := make(map[string]bool)
	for _, cp := range reverted {
		fileSet[cp.FilePath] = true
	}
	files := make([]string, 0, len(fileSet))
	for f := range fileSet {
		files = append(files, f)
	}
	toolCall := ""
	if len(reverted) > 0 {
		toolCall = reverted[0].ToolCall
	}
	m.corrections = append(m.corrections, Correction{
		Files:    files,
		ToolCall: toolCall,
		RunID:    runID,
		Time:     time.Now(),
	})
}

// List returns all checkpoints (most recent last).
func (m *Manager) List() []Checkpoint {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Checkpoint, len(m.checkpoints))
	copy(out, m.checkpoints)
	return out
}

// Last returns the most recent checkpoint, or nil if empty.
func (m *Manager) Last() *Checkpoint {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.checkpoints) == 0 {
		return nil
	}
	// Return a copy to avoid aliasing the backing array.
	cp := m.checkpoints[len(m.checkpoints)-1]
	return &cp
}

// Redo re-applies the most recently undone checkpoint by writing NewContent
// back to the file. The checkpoint is returned to the main checkpoint list.
func (m *Manager) Redo() (*Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.redoStack) == 0 {
		return nil, fmt.Errorf("nothing to redo")
	}

	cp := m.redoStack[len(m.redoStack)-1]

	if err := util.AtomicWriteFile(cp.FilePath, []byte(cp.NewContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	m.redoStack = m.redoStack[:len(m.redoStack)-1]
	m.checkpoints = append(m.checkpoints, cp)
	return &cp, nil
}

// CanRedo returns true if there are checkpoints available for redo.
func (m *Manager) CanRedo() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.redoStack) > 0
}

// Clear removes all checkpoints and redo history.
func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkpoints = nil
	m.redoStack = nil
	m.corrections = nil
	m.evictedRuns = nil
}

// RecentCorrections returns corrections recorded since the last run start.
// Returns nil if no user-initiated undos have occurred.
func (m *Manager) RecentCorrections() []Correction {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.corrections) == 0 {
		return nil
	}
	out := make([]Correction, len(m.corrections))
	copy(out, m.corrections)
	return out
}

// ClearCorrections removes all recorded corrections. Called at the start
// of a new agent run so the correction feedback is one-shot.
func (m *Manager) ClearCorrections() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.corrections = nil
}

func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
