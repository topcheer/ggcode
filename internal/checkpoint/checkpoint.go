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
}

// Manager manages file checkpoints for undo support.
type Manager struct {
	checkpoints    []Checkpoint
	maxCheckpoints int
	mu             sync.Mutex
}

// NewManager creates a new checkpoint manager with the given max limit.
func NewManager(maxCheckpoints int) *Manager {
	if maxCheckpoints <= 0 {
		maxCheckpoints = 50
	}
	return &Manager{maxCheckpoints: maxCheckpoints}
}

// Save records a checkpoint before a file edit.
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
	}

	m.checkpoints = append(m.checkpoints, cp)

	// Evict oldest if over limit
	if len(m.checkpoints) > m.maxCheckpoints {
		m.checkpoints = m.checkpoints[len(m.checkpoints)-m.maxCheckpoints:]
	}

	return cp
}

// Undo rolls back the most recent checkpoint by writing OldContent back to the file.
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

// Clear removes all checkpoints.
func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkpoints = nil
}

func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
