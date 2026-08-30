package task

import (
	"fmt"
	"sync"
	"time"
)

type TaskStatus string

const (
	StatusPending    TaskStatus = "pending"
	StatusInProgress TaskStatus = "in_progress"
	StatusCompleted  TaskStatus = "completed"
)

func ValidStatuses() []TaskStatus {
	return []TaskStatus{StatusPending, StatusInProgress, StatusCompleted}
}

func IsValidStatus(s string) bool {
	for _, v := range ValidStatuses() {
		if TaskStatus(s) == v {
			return true
		}
	}
	return false
}

// Task represents a single tracked task in the session.
type Task struct {
	ID          string
	Subject     string
	Description string
	ActiveForm  string
	Status      TaskStatus
	Owner       string
	Blocks      []string
	BlockedBy   []string
	Metadata    map[string]string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Snapshot returns a copy of the task safe for external use.
func (t *Task) Snapshot() Task {
	cp := *t
	cp.Blocks = append([]string(nil), t.Blocks...)
	cp.BlockedBy = append([]string(nil), t.BlockedBy...)
	if t.Metadata != nil {
		cp.Metadata = make(map[string]string, len(t.Metadata))
		for k, v := range t.Metadata {
			cp.Metadata[k] = v
		}
	}
	return cp
}

// UpdateOptions specifies which fields to change on a task.
type UpdateOptions struct {
	Status         *TaskStatus
	ExpectedStatus *TaskStatus // if set, the update fails if current status doesn't match
	Subject        *string
	Description    *string
	ActiveForm     *string
	Owner          *string
	AddBlocks      []string
	AddBlockedBy   []string
	Metadata       map[string]string // merged; nil values are ignored
}

// Manager tracks tasks within a single session (in-memory, no persistence).
type Manager struct {
	mu     sync.Mutex
	tasks  map[string]*Task
	nextID int
}

// NewManager creates an empty task manager.
func NewManager() *Manager {
	return &Manager{
		tasks: make(map[string]*Task),
	}
}

// Create adds a new task and returns a snapshot.
func (m *Manager) Create(subject, description, activeForm string, metadata map[string]string) Task {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.nextID++
	id := fmt.Sprintf("task-%d", m.nextID)
	now := time.Now()

	if metadata == nil {
		metadata = make(map[string]string)
	}

	t := &Task{
		ID:          id,
		Subject:     subject,
		Description: description,
		ActiveForm:  activeForm,
		Status:      StatusPending,
		Metadata:    metadata,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	m.tasks[id] = t
	return t.Snapshot()
}

// Get retrieves a task by ID. Returns the snapshot and whether it was found.
func (m *Manager) Get(taskID string) (Task, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.tasks[taskID]
	if !ok {
		return Task{}, false
	}
	return t.Snapshot(), true
}

// List returns snapshots of all tasks.
func (m *Manager) List() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, t.Snapshot())
	}
	return out
}

// Update modifies a task according to opts. Returns the updated snapshot.
func (m *Manager) Update(taskID string, opts UpdateOptions) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.tasks[taskID]
	if !ok {
		return Task{}, fmt.Errorf("task %q not found", taskID)
	}

	// Validate expected status (for conditional updates)
	if opts.ExpectedStatus != nil && t.Status != *opts.ExpectedStatus {
		return Task{}, fmt.Errorf("task %q status is %s, expected %s", taskID, t.Status, *opts.ExpectedStatus)
	}

	// Validate status transition FIRST (#803): collect dependency edges and
	// verify them before ANY mutation, so 'error = not modified' holds even
	// when a later edge fails — Update(id, {Status: completed, AddBlockedBy:
	// [nonexistent]}) must not leave Status half-applied.
	if opts.Status != nil && !IsValidStatus(string(*opts.Status)) {
		return Task{}, fmt.Errorf("invalid status %q", *opts.Status)
	}
	for _, blockID := range opts.AddBlocks {
		if blockID == taskID {
			return Task{}, fmt.Errorf("task %q cannot block itself (self-dependency)", taskID)
		}
		if !m.hasTask(blockID) {
			return Task{}, fmt.Errorf("blocked task %q not found", blockID)
		}
		// #1297: args were swapped. wouldCreateCycle(start, target) checks
		// whether target is reachable from start's BlockedBy chain; adding
		// "taskID blocks blockID" creates a cycle iff taskID is already
		// transitively blocked by blockID, i.e. wouldCreateCycle(taskID, blockID).
		if m.wouldCreateCycle(taskID, blockID) {
			return Task{}, fmt.Errorf("circular dependency detected: task %q is (transitively) blocked by task %q — cannot add block %q → %q", taskID, blockID, taskID, blockID)
		}
	}
	for _, depID := range opts.AddBlockedBy {
		if depID == taskID {
			return Task{}, fmt.Errorf("task %q cannot depend on itself", taskID)
		}
		if !m.hasTask(depID) {
			return Task{}, fmt.Errorf("blocking task %q not found", depID)
		}
		// #1297: adding "depID blocks taskID" - cycle iff depID is already
		// transitively blocked by taskID.
		if m.wouldCreateCycle(depID, taskID) {
			return Task{}, fmt.Errorf("circular dependency detected adding blocked-by %q", depID)
		}
		// #1345: cross-array cycle. If depID is also in AddBlocks, the
		// apply phase writes "taskID blocks depID" first (depID.BlockedBy
		// gains taskID) and only then detects that this blockedBy edge
		// closes a 2-cycle - after partial mutation, violating the
		// "error = not modified" contract. "X blocks B" and "B blocks X"
		// is always a cycle, so reject up front.
		if contains(opts.AddBlocks, depID) {
			return Task{}, fmt.Errorf("circular dependency detected: task %q cannot both block and be blocked by task %q", taskID, depID)
		}
	}

	// All validation passed — apply mutations.
	if opts.Status != nil {
		t.Status = *opts.Status
	}
	if opts.Subject != nil {
		t.Subject = *opts.Subject
	}
	if opts.Description != nil {
		t.Description = *opts.Description
	}
	if opts.ActiveForm != nil {
		t.ActiveForm = *opts.ActiveForm
	}
	if opts.Owner != nil {
		t.Owner = *opts.Owner
	}

	// Add block relationships (validated above; now safe to apply)
	for _, blockID := range opts.AddBlocks {
		// #1297: re-check against evolving state - earlier iterations of
		// this loop add reverse links that earlier edges' validations did
		// not see (the pre-validation loop judged each edge in isolation).
		if m.wouldCreateCycle(taskID, blockID) {
			return Task{}, fmt.Errorf("circular dependency detected: adding block %q → %q would close a cycle", taskID, blockID)
		}
		if !contains(t.Blocks, blockID) {
			t.Blocks = append(t.Blocks, blockID)
		}
		// Add reverse link
		blocked := m.tasks[blockID]
		if !contains(blocked.BlockedBy, taskID) {
			blocked.BlockedBy = append(blocked.BlockedBy, taskID)
		}
	}
	for _, blockerID := range opts.AddBlockedBy {
		if blockerID == taskID {
			return Task{}, fmt.Errorf("task %q cannot be blocked by itself (self-dependency)", taskID)
		}
		if !m.hasTask(blockerID) {
			return Task{}, fmt.Errorf("blocker task %q not found", blockerID)
		}
		// Cycle detection: if taskID is already (transitively) blocking
		// blockerID... #1297: args were swapped here too. Adding
		// "blockerID blocks taskID" closes a cycle iff blockerID is
		// already transitively blocked by taskID.
		if m.wouldCreateCycle(blockerID, taskID) {
			return Task{}, fmt.Errorf("circular dependency detected: task %q already (transitively) blocks task %q — cannot add blockedBy %q → %q", blockerID, taskID, taskID, blockerID)
		}
		if !contains(t.BlockedBy, blockerID) {
			t.BlockedBy = append(t.BlockedBy, blockerID)
		}
		// Add reverse link
		blocker := m.tasks[blockerID]
		if !contains(blocker.Blocks, taskID) {
			blocker.Blocks = append(blocker.Blocks, taskID)
		}
	}

	// Merge metadata
	if opts.Metadata != nil {
		if t.Metadata == nil {
			t.Metadata = make(map[string]string)
		}
		for k, v := range opts.Metadata {
			t.Metadata[k] = v
		}
	}

	t.UpdatedAt = time.Now()
	return t.Snapshot(), nil
}

// Delete removes a task by ID and cleans up dangling block references
// in other tasks.
func (m *Manager) Delete(taskID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.tasks[taskID]; !ok {
		return false
	}
	// Clean up block references in remaining tasks.
	for id, other := range m.tasks {
		if id == taskID {
			continue
		}
		other.Blocks = removeString(other.Blocks, taskID)
		other.BlockedBy = removeString(other.BlockedBy, taskID)
	}
	delete(m.tasks, taskID)
	return true
}

func removeString(slice []string, s string) []string {
	for i, v := range slice {
		if v == s {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}

func (m *Manager) hasTask(id string) bool {
	_, ok := m.tasks[id]
	return ok
}

// wouldCreateCycle checks if adding "from blocks to" would create a
// circular dependency. It traverses the BlockedBy chain starting from
// 'start' to see if 'target' is reachable. If so, adding the edge
// target→start would close a cycle.
//
// Example: A blocks B, B blocks C. If we try to add C blocks A:
// wouldCreateCycle(C, A) traverses C.BlockedBy → [B], B.BlockedBy → [A]
// finds A → returns true (cycle detected).
func (m *Manager) wouldCreateCycle(start, target string) bool {
	visited := make(map[string]bool)
	var check func(taskID string) bool
	check = func(taskID string) bool {
		if taskID == target {
			return true
		}
		if visited[taskID] {
			return false
		}
		visited[taskID] = true
		t, ok := m.tasks[taskID]
		if !ok {
			return false
		}
		for _, blockerID := range t.BlockedBy {
			if check(blockerID) {
				return true
			}
		}
		return false
	}
	return check(start)
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
