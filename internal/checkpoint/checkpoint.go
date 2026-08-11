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

	// Evict oldest if over limit
	if len(m.checkpoints) > m.maxCheckpoints {
		m.checkpoints = m.checkpoints[len(m.checkpoints)-m.maxCheckpoints:]
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

	// Collect indices belonging to this run (from the end backward).
	var runIndices []int
	for i := len(m.checkpoints) - 1; i >= 0; i-- {
		if m.checkpoints[i].RunID != runID {
			break
		}
		runIndices = append(runIndices, i)
	}
	if len(runIndices) == 0 {
		return nil, fmt.Errorf("no checkpoints in current run")
	}

	// For each unique file, we need the ORIGINAL content from the FIRST
	// checkpoint of the run for that file. Build a map: filePath -> firstOldContent.
	// runIndices is in reverse order (last first), so the first occurrence
	// in forward order gives us the earliest checkpoint per file.
	baselines := make(map[string]string)
	for i := len(runIndices) - 1; i >= 0; i-- {
		cp := m.checkpoints[runIndices[i]]
		if _, exists := baselines[cp.FilePath]; !exists {
			baselines[cp.FilePath] = cp.OldContent
		}
	}

	// Write baseline content for each unique file.
	var reverted []Checkpoint
	revertedFiles := make(map[string]bool)
	for _, idx := range runIndices {
		cp := m.checkpoints[idx]
		if revertedFiles[cp.FilePath] {
			continue
		}
		baseline := baselines[cp.FilePath]
		if err := util.AtomicWriteFile(cp.FilePath, []byte(baseline), 0644); err != nil {
			// Remove already-reverted checkpoints from the list to keep
			// metadata consistent with disk state.
			for _, r := range reverted {
				for j := len(m.checkpoints) - 1; j >= 0; j-- {
					if m.checkpoints[j].FilePath == r.FilePath && m.checkpoints[j].RunID == runID {
						m.checkpoints = append(m.checkpoints[:j], m.checkpoints[j+1:]...)
						break
					}
				}
			}
			return reverted, fmt.Errorf("failed to revert %s: %w", cp.FilePath, err)
		}
		revertedFiles[cp.FilePath] = true
		reverted = append(reverted, cp)
	}

	// Remove all run checkpoints from the list and push onto redo stack.
	cutoff := runIndices[len(runIndices)-1] // earliest index in this run
	removed := make([]Checkpoint, len(m.checkpoints[cutoff:]))
	copy(removed, m.checkpoints[cutoff:])
	m.checkpoints = m.checkpoints[:cutoff]
	// Push in reverse so Redo() re-applies in original order.
	for i := len(removed) - 1; i >= 0; i-- {
		m.redoStack = append(m.redoStack, removed[i])
	}

	// Record the correction so the agent can learn from the rejection.
	// Collect unique file paths from the reverted checkpoints.
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

	return reverted, nil
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
