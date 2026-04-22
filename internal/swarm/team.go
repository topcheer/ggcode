package swarm

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/task"
)

// TeammateStatus represents the lifecycle state of a teammate within a team.
type TeammateStatus string

const (
	TeammateIdle         TeammateStatus = "idle"
	TeammateWorking      TeammateStatus = "working"
	TeammateShuttingDown TeammateStatus = "shutting_down"
)

// MailMessage is a message delivered to a teammate's inbox.
type MailMessage struct {
	From    string // sender ID (leader or another teammate)
	Content string // task or message content
	Summary string // optional short summary
	Type    string // "task", "message", "shutdown"

	// ReplyTo is an optional channel for sending the task result back to the caller.
	// If non-nil, the idle runner will send the execution result here after the task completes.
	// The caller (e.g., send_message tool) blocks on this channel to await the result.
	ReplyTo chan<- TaskResult
}

// TaskResult holds the outcome of a teammate executing a task.
type TaskResult struct {
	Output string
	Error  error
}

// Teammate represents a worker agent within a team.
type Teammate struct {
	ID          string
	Name        string // e.g., "researcher", "coder"
	Color       string // TUI display color (ANSI code or empty)
	Status      TeammateStatus
	CurrentTask string
	LastResult  string // most recent task output (truncated)
	Inbox       chan MailMessage
	CreatedAt   time.Time
	StartedAt   time.Time
	EndedAt     time.Time

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
}

func (t *Teammate) setStatus(s TeammateStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = s
}

func (t *Teammate) getStatus() TeammateStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.Status
}

func (t *Teammate) setCurrentTask(task string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.CurrentTask = task
}

func (t *Teammate) setLastResult(result string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.LastResult = result
}

func (t *Teammate) getResults() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.LastResult
}

// TeammateSnapshot is a read-only copy of a Teammate for external consumption.
type TeammateSnapshot struct {
	ID          string
	Name        string
	Color       string
	Status      TeammateStatus
	CurrentTask string
	LastResult  string // most recent task output (truncated)
	CreatedAt   time.Time
	StartedAt   time.Time
	EndedAt     time.Time
}

func (t *Teammate) snapshot() TeammateSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return TeammateSnapshot{
		ID:          t.ID,
		Name:        t.Name,
		Color:       t.Color,
		Status:      t.Status,
		CurrentTask: t.CurrentTask,
		LastResult:  t.LastResult,
		CreatedAt:   t.CreatedAt,
		StartedAt:   t.StartedAt,
		EndedAt:     t.EndedAt,
	}
}

// Team represents a collaboration group with a leader and multiple teammates.
type Team struct {
	ID        string
	Name      string
	LeaderID  string
	Teammates map[string]*Teammate
	Tasks     *task.Manager // shared task board
	CreatedAt time.Time

	mu sync.RWMutex
}

// TeamSnapshot is a read-only copy of a Team for external consumption.
type TeamSnapshot struct {
	ID        string
	Name      string
	LeaderID  string
	Teammates []TeammateSnapshot
	TaskCount int
	CreatedAt time.Time
}

func (t *Team) snapshot() TeamSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()

	mates := make([]TeammateSnapshot, 0, len(t.Teammates))
	for _, m := range t.Teammates {
		mates = append(mates, m.snapshot())
	}
	sort.Slice(mates, func(i, j int) bool { return mates[i].ID < mates[j].ID })

	taskCount := 0
	if t.Tasks != nil {
		taskCount = len(t.Tasks.List())
	}

	return TeamSnapshot{
		ID:        t.ID,
		Name:      t.Name,
		LeaderID:  t.LeaderID,
		Teammates: mates,
		TaskCount: taskCount,
		CreatedAt: t.CreatedAt,
	}
}

func (t *Team) getTeammate(id string) (*Teammate, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	m, ok := t.Teammates[id]
	return m, ok
}

func (t *Team) listTeammates() []*Teammate {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]*Teammate, 0, len(t.Teammates))
	for _, m := range t.Teammates {
		out = append(out, m)
	}
	return out
}

// Event represents a state change in the swarm system, sent to TUI via callback.
type Event struct {
	Type         string // "teammate_spawned", "teammate_working", "teammate_idle", "teammate_shutdown", "team_created", "team_deleted"
	TeamID       string
	TeammateID   string
	TeammateName string
	Result       string
	Error        error
	Timestamp    time.Time
}
