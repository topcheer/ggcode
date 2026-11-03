package swarm

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/task"
)

type TeammateStatus string

const (
	TeammateIdle         TeammateStatus = "idle"
	TeammateWorking      TeammateStatus = "working"
	TeammateShuttingDown TeammateStatus = "shutting_down"
)

type MailMessage struct {
	From    string
	Content string
	Summary string
	Type    string

	ReplyTo chan<- TaskResult
}

type TaskResult struct {
	Output string
	Error  error
}

type TeammateEventType int

const (
	TeammateEventText TeammateEventType = iota
	TeammateEventReasoning
	TeammateEventToolCall
	TeammateEventToolResult
	TeammateEventError
)

type TeammateEvent struct {
	Type     TeammateEventType
	Text     string
	ToolName string
	ToolID   string
	ToolArgs string
	Result   string
	IsError  bool
}

const maxTeammateEvents = 200

type Teammate struct {
	ID            string
	Name          string
	Color         string
	Status        TeammateStatus
	CurrentTask   string
	CurrentTaskID string
	LastResult    string
	Inbox         chan MailMessage
	CreatedAt     time.Time
	StartedAt     time.Time
	EndedAt       time.Time

	events        []TeammateEvent
	eventsDropped int

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
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

func (t *Teammate) EventsSince(fromIdx int) ([]TeammateEvent, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	total := len(t.events) + t.eventsDropped
	if fromIdx >= total {
		return nil, total
	}

	rel := fromIdx - t.eventsDropped
	if rel < 0 {

		rel = 0
	}
	out := make([]TeammateEvent, len(t.events)-rel)
	copy(out, t.events[rel:])
	return out, total
}

func (t *Teammate) getResults() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.LastResult
}

type TeammateStatusInfo struct {
	ID          string
	Name        string
	Color       string
	Status      TeammateStatus
	CurrentTask string
	LastResult  string
}

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

type TeammateSnapshot struct {
	ID            string
	Name          string
	Color         string
	Status        TeammateStatus
	CurrentTask   string
	LastResult    string
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

type Team struct {
	ID        string
	Name      string
	LeaderID  string
	Teammates map[string]*Teammate
	Tasks     *task.Manager
	CreatedAt time.Time

	mu sync.RWMutex
}

type TeamSnapshot struct {
	ID        string
	Name      string
	LeaderID  string
	Teammates []TeammateSnapshot
	TaskCount int
	CreatedAt time.Time
}

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

func (t *Team) removeTeammate(id string) {
	t.mu.Lock()
	delete(t.Teammates, id)
	t.mu.Unlock()
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

type Event struct {
	Type         string
	TeamID       string
	TeammateID   string
	TeammateName string
	Result       string
	Error        error
	CurrentTool  string
	ToolID       string
	ToolArgs     string
	IsError      bool
	Timestamp    time.Time
}
