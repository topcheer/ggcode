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

	cp := &m.checkpoints[len(m.checkpoints)-1]

	if err := util.AtomicWriteFile(cp.FilePath, []byte(cp.OldContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	m.checkpoints = m.checkpoints[:len(m.checkpoints)-1]
	return cp, nil
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

	cp := &m.checkpoints[idx]

	if err := util.AtomicWriteFile(cp.FilePath, []byte(cp.OldContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	m.checkpoints = m.checkpoints[:idx]
	return cp, nil
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
	return &m.checkpoints[len(m.checkpoints)-1]
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
