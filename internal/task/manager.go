package task

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
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

type UpdateOptions struct {
	Status         *TaskStatus
	ExpectedStatus *TaskStatus
	Subject        *string
	Description    *string
	ActiveForm     *string
	Owner          *string
	AddBlocks      []string
	AddBlockedBy   []string
	Metadata       map[string]string
}

type Manager struct {
	mu     sync.Mutex
	tasks  map[string]*Task
	nextID int
}

func NewManager() *Manager {
	return &Manager{
		tasks: make(map[string]*Task),
	}
}

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

func (m *Manager) Get(taskID string) (Task, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.tasks[taskID]
	if !ok {
		return Task{}, false
	}
	return t.Snapshot(), true
}

func (m *Manager) List() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, t.Snapshot())
	}
	sort.Slice(out, func(i, j int) bool {

		ipi, ipj := out[i].Status == StatusInProgress, out[j].Status == StatusInProgress
		if ipi != ipj {
			return ipi
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

const (
	defaultDigestMaxTasks = 20
	defaultDigestMaxChars = 1200
)

func (m *Manager) Digest(maxTasks, maxChars int) string {
	if maxTasks <= 0 {
		maxTasks = defaultDigestMaxTasks
	}
	if maxChars <= 0 {
		maxChars = defaultDigestMaxChars
	}

	stats, body := m.digestStats(maxTasks)
	if stats.pending+stats.inProgress == 0 {
		return ""
	}

	header := fmt.Sprintf("Task board: %d pending, %d in_progress, %d completed. Task IDs remain valid; call task_list for full details.", stats.pending, stats.inProgress, stats.completed)
	out := header
	if body != "" {
		out += "\n" + body
	}
	if n := utf8.RuneCountInString(out); n > maxChars {
		runes := []rune(out)
		cut := string(runes[:maxChars])

		if i := strings.LastIndex(cut, "\n"); i > 0 {
			cut = cut[:i]
		}
		out = cut + "\n... (truncated)"
	}
	return out
}

func (m *Manager) digestStats(maxTasks int) (digestStats, string) {
	var st digestStats
	var sb strings.Builder
	for _, t := range m.List() {
		switch t.Status {
		case StatusInProgress:
			st.inProgress++
		case StatusCompleted:
			st.completed++
		default:
			st.pending++
		}
		if t.Status != StatusCompleted && st.lines < maxTasks {
			st.lines++
			fmt.Fprintf(&sb, "- %s [%s] %s\n", t.ID, t.Status, truncateDigestSubject(t.Subject))
		}
	}
	return st, strings.TrimRight(sb.String(), "\n")
}

type digestStats struct {
	pending    int
	inProgress int
	completed  int
	lines      int
}

func truncateDigestSubject(s string) string {
	const maxDigestSubject = 80
	if n := utf8.RuneCountInString(s); n > maxDigestSubject {
		return string([]rune(s)[:maxDigestSubject]) + "..."
	}
	return s
}

func (m *Manager) Update(taskID string, opts UpdateOptions) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.tasks[taskID]
	if !ok {
		return Task{}, fmt.Errorf("task %q not found", taskID)
	}

	if opts.ExpectedStatus != nil && t.Status != *opts.ExpectedStatus {
		return Task{}, fmt.Errorf("task %q status is %s, expected %s", taskID, t.Status, *opts.ExpectedStatus)
	}

	if opts.Status != nil && !IsValidStatus(string(*opts.Status)) {
		return Task{}, fmt.Errorf("invalid status %q", *opts.Status)
	}

	virtualBlockedBy := make(map[string][]string, len(opts.AddBlocks))
	if len(opts.AddBlockedBy) > 0 {
		virtualBlockedBy[taskID] = opts.AddBlockedBy
	}
	for _, blockID := range opts.AddBlocks {
		virtualBlockedBy[blockID] = append(virtualBlockedBy[blockID], taskID)
	}

	for _, blockID := range opts.AddBlocks {
		if blockID == taskID {
			return Task{}, fmt.Errorf("task %q cannot block itself (self-dependency)", taskID)
		}
		if !m.hasTask(blockID) {
			return Task{}, fmt.Errorf("blocked task %q not found", blockID)
		}

		if m.wouldCreateCycleVirtual(virtualBlockedBy, taskID, blockID) {
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

		if m.wouldCreateCycleVirtual(virtualBlockedBy, depID, taskID) {
			return Task{}, fmt.Errorf("circular dependency detected adding blocked-by %q (including edges from this call's addBlocks)", depID)
		}

		if contains(opts.AddBlocks, depID) {
			return Task{}, fmt.Errorf("circular dependency detected: task %q cannot both block and be blocked by task %q", taskID, depID)
		}
	}

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

	for _, blockID := range opts.AddBlocks {

		if m.wouldCreateCycle(taskID, blockID) {
			return Task{}, fmt.Errorf("circular dependency detected: adding block %q → %q would close a cycle", taskID, blockID)
		}
		if !contains(t.Blocks, blockID) {
			t.Blocks = append(t.Blocks, blockID)
		}

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

		if m.wouldCreateCycle(blockerID, taskID) {
			return Task{}, fmt.Errorf("circular dependency detected: task %q already (transitively) blocks task %q — cannot add blockedBy %q → %q", blockerID, taskID, taskID, blockerID)
		}
		if !contains(t.BlockedBy, blockerID) {
			t.BlockedBy = append(t.BlockedBy, blockerID)
		}

		blocker := m.tasks[blockerID]
		if !contains(blocker.Blocks, taskID) {
			blocker.Blocks = append(blocker.Blocks, taskID)
		}
	}

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

func (m *Manager) Delete(taskID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.tasks[taskID]; !ok {
		return false
	}

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

func (m *Manager) wouldCreateCycle(start, target string) bool {
	return m.wouldCreateCycleVirtual(nil, start, target)
}

func (m *Manager) wouldCreateCycleVirtual(extraBlockedBy map[string][]string, start, target string) bool {
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
		for _, blockerID := range extraBlockedBy[taskID] {
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
