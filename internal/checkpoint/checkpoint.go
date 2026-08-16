package checkpoint

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

// Checkpoint represents a saved file state before a tool edit.
type Checkpoint struct {
	ID         string    `json:"id"`
	FilePath   string    `json:"file_path"`
	OldContent string    `json:"old_content"`
	NewContent string    `json:"new_content"`
	Timestamp  time.Time `json:"timestamp"`
	ToolCall   string    `json:"tool_call"`
	RunID      string    `json:"run_id,omitempty"` // agent run that created this checkpoint
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
func (m *Manager) Save(filePath, oldContent, newContent, toolCall string) Checkpoint {
	m.mu.Lock()
	defer m.mu.Unlock()

	cp := Checkpoint{
		ID:         generateID(),
		FilePath:   filePath,
		OldContent: oldContent,
		NewContent: newContent,
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

	if err := util.AtomicWriteFile(cp.FilePath, []byte(cp.OldContent), 0644); err != nil {
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

// Revert rolls back to a specific checkpoint by ID, writing OldContent back to the file.
// It also removes all checkpoints newer than the target.
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

	if err := util.AtomicWriteFile(cp.FilePath, []byte(cp.OldContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	m.checkpoints = m.checkpoints[:idx]
	return &cp, nil
}

// FileSummary summarizes the edits made to a single file.
type FileSummary struct {
	Path     string
	Edits    int
	LastTool string
	IsNew    bool // true if the first checkpoint had empty OldContent
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
				IsNew:    cp.OldContent == "",
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

// preRunBaselines maps each unique file path in the run to the OldContent of
// its FIRST checkpoint — the pre-run state. runIndices is in reverse order
// (last first), so the first occurrence in forward order gives the earliest
// checkpoint per file. Must hold m.mu.
func (m *Manager) preRunBaselines(runIndices []int) map[string]string {
	baselines := make(map[string]string)
	for i := len(runIndices) - 1; i >= 0; i-- {
		cp := m.checkpoints[runIndices[i]]
		if _, exists := baselines[cp.FilePath]; !exists {
			baselines[cp.FilePath] = cp.OldContent
		}
	}
	return baselines
}

// writeBaselines writes each unique file's pre-run baseline to disk, once
// per file, iterating runIndices last-to-first. On the first write failure it
// removes ALL checkpoints of the already-reverted files in this run (not just
// one per file) to keep metadata consistent with disk state — leaving
// mid-run entries behind would let a later single-step Undo re-apply a
// mid-run state on top of the rolled-back baseline (issue #517 Bug A) — and
// returns the partially reverted checkpoints plus the error. Must hold m.mu.
func (m *Manager) writeBaselines(runIndices []int, baselines map[string]string, runID string) ([]Checkpoint, error) {
	var reverted []Checkpoint
	revertedFiles := make(map[string]bool)
	for _, idx := range runIndices {
		cp := m.checkpoints[idx]
		if revertedFiles[cp.FilePath] {
			continue
		}
		if err := util.AtomicWriteFile(cp.FilePath, []byte(baselines[cp.FilePath]), 0644); err != nil {
			m.removeRunCheckpointsFor(reverted, runID)
			return reverted, fmt.Errorf("failed to revert %s: %w", cp.FilePath, err)
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
