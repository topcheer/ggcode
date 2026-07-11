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

// TeammateEventType identifies the kind of teammate event.
type TeammateEventType int

const (
	TeammateEventText       TeammateEventType = iota // LLM text output
	TeammateEventReasoning                           // LLM thinking/reasoning output
	TeammateEventToolCall                            // tool invocation started
	TeammateEventToolResult                          // tool execution result
	TeammateEventError                               // error encountered
)

// TeammateEvent is a single recorded event from a teammate's execution.
type TeammateEvent struct {
	Type     TeammateEventType
	Text     string // TeammateEventText / TeammateEventReasoning / TeammateEventError
	ToolName string // TeammateEventToolCall / TeammateEventToolResult
	ToolID   string // TeammateEventToolCall / TeammateEventToolResult — unique ID for precise matching
	ToolArgs string // TeammateEventToolCall
	Result   string // TeammateEventToolResult
	IsError  bool   // TeammateEventToolResult / TeammateEventError
}

const maxTeammateEvents = 200

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

	events        []TeammateEvent
	eventsDropped int

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{} // closed when the idle-runner goroutine exits
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

func (t *Teammate) appendEvent(ev TeammateEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.events) >= maxTeammateEvents {
		t.events = t.events[1:]
		t.eventsDropped++
	}
	t.events = append(t.events, ev)
}

// EventsSince returns only events with index >= fromIdx, along with the total
// event count. This avoids copying the full event history when only incremental
// events are needed (e.g. GUI agent panel updates).
func (t *Teammate) EventsSince(fromIdx int) ([]TeammateEvent, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	total := len(t.events)
	if fromIdx >= total {
		return nil, total
	}
	out := make([]TeammateEvent, total-fromIdx)
	copy(out, t.events[fromIdx:])
	return out, total
}

func (t *Teammate) getResults() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.LastResult
}

// TeammateStatusInfo is a lightweight read-only copy of a Teammate's identity
// and status. Unlike TeammateSnapshot, it does NOT copy events, making it
// safe to call at high frequency (e.g. every streaming token) without
// incurring O(maxTeammateEvents) copy overhead or contending the Teammate.mu
// lock that the runner uses for appendEvent.
type TeammateStatusInfo struct {
	ID          string
	Name        string
	Color       string
	Status      TeammateStatus
	CurrentTask string
	LastResult  string
}

// statusInfo returns a lightweight copy with only identity + status.
// It acquires Teammate.mu briefly to read the Status field.
func (t *Teammate) statusInfo() TeammateStatusInfo {
	t.mu.Lock()
	info := TeammateStatusInfo{
		ID:          t.ID,
		Name:        t.Name,
		Color:       t.Color,
		Status:      t.Status,
		CurrentTask: t.CurrentTask,
		LastResult:  t.LastResult,
	}
	t.mu.Unlock()
	return info
}

// TeammateSnapshot is a read-only copy of a Teammate for external consumption.
type TeammateSnapshot struct {
	ID            string
	Name          string
	Color         string
	Status        TeammateStatus
	CurrentTask   string
	LastResult    string // most recent task output (truncated)
	CreatedAt     time.Time
	StartedAt     time.Time
	EndedAt       time.Time
	Events        []TeammateEvent
	EventsDropped int
}

func (t *Teammate) snapshot() TeammateSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	events := make([]TeammateEvent, len(t.events))
	copy(events, t.events)
	return TeammateSnapshot{
		ID:            t.ID,
		Name:          t.Name,
		Color:         t.Color,
		Status:        t.Status,
		CurrentTask:   t.CurrentTask,
		LastResult:    t.LastResult,
		CreatedAt:     t.CreatedAt,
		StartedAt:     t.StartedAt,
		EndedAt:       t.EndedAt,
		Events:        events,
		EventsDropped: t.eventsDropped,
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

// teammateStatuses returns lightweight status info for all teammates.
// Unlike snapshot(), this does NOT copy events and is safe to call at high
// frequency. It only acquires Team.mu.RLock briefly to iterate teammates,
// and each Teammate.mu briefly to read Status.
func (t *Team) teammateStatuses() []TeammateStatusInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]TeammateStatusInfo, 0, len(t.Teammates))
	for _, m := range t.Teammates {
		out = append(out, m.statusInfo())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
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

func (t *Team) boardSnapshot() TeamBoardSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()

	mates := make([]TeamBoardTeammate, 0, len(t.Teammates))
	for _, m := range t.Teammates {
		snap := m.statusInfo()
		mates = append(mates, TeamBoardTeammate{
			ID:          snap.ID,
			Name:        snap.Name,
			Color:       snap.Color,
			Status:      string(snap.Status),
			CurrentTask: snap.CurrentTask,
			LastResult:  snap.LastResult,
		})
	}
	sort.Slice(mates, func(i, j int) bool { return mates[i].ID < mates[j].ID })

	tasks := []TeamBoardTask{}
	if t.Tasks != nil {
		taskSnapshots := t.Tasks.List()
		tasks = make([]TeamBoardTask, 0, len(taskSnapshots))
		for _, taskSnap := range taskSnapshots {
			metadata := taskSnap.Metadata
			assignee := ""
			if metadata != nil {
				assignee = metadata["assignee"]
			}
			tasks = append(tasks, TeamBoardTask{
				ID:          taskSnap.ID,
				Subject:     taskSnap.Subject,
				Description: taskSnap.Description,
				ActiveForm:  taskSnap.ActiveForm,
				Status:      string(taskSnap.Status),
				Owner:       taskSnap.Owner,
				Assignee:    assignee,
				Blocks:      taskSnap.Blocks,
				BlockedBy:   taskSnap.BlockedBy,
				Metadata:    metadata,
				CreatedAt:   taskSnap.CreatedAt,
				UpdatedAt:   taskSnap.UpdatedAt,
			})
		}
		sort.Slice(tasks, func(i, j int) bool { return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt) })
	}

	return TeamBoardSnapshot{
		ID:        t.ID,
		Name:      t.Name,
		LeaderID:  t.LeaderID,
		Teammates: mates,
		Tasks:     tasks,
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
	Type         string // "teammate_spawned", "teammate_working", "teammate_text", "teammate_tool_call", "teammate_idle", "teammate_shutdown", "team_created", "team_deleted"
	TeamID       string
	TeammateID   string
	TeammateName string
	Result       string
	Error        error
	CurrentTool  string // tool name (for teammate_tool_call)
	ToolID       string // tool call ID (for teammate_tool_call/result)
	ToolArgs     string // tool args (for teammate_tool_call)
	IsError      bool   // tool result error (for teammate_tool_result)
	Timestamp    time.Time
}
