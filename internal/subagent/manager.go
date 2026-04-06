package subagent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// Status represents the lifecycle state of a sub-agent.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// SubAgent represents a spawned child agent.
type SubAgent struct {
	ID              string
	Task            string
	DisplayTask     string
	Tools           []string
	ToolCallCount   int
	Status          Status
	CurrentPhase    string
	CurrentTool     string
	CurrentArgs     string
	ProgressSummary string
	Result          string
	Error           error
	CreatedAt       time.Time
	StartedAt       time.Time
	EndedAt         time.Time
	cancel          context.CancelFunc
	mu              sync.Mutex
}

func (s *SubAgent) IncrementToolCalls() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ToolCallCount++
}

func (s *SubAgent) setStatus(st Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = st
}

func (s *SubAgent) setActivity(phase, toolName, args string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CurrentPhase = phase
	s.CurrentTool = toolName
	s.CurrentArgs = args
}

func (s *SubAgent) setProgressSummary(summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ProgressSummary = summary
}

// Manager manages spawning, tracking, and collecting results from sub-agents.
type Manager struct {
	agents     map[string]*SubAgent
	mu         sync.Mutex
	sem        chan struct{}
	timeout    time.Duration
	showOutput bool
	onUpdate   func(*SubAgent)
	onComplete func(*SubAgent)
	nextID     int
}

// NewManager creates a Manager with the given config.
func NewManager(cfg config.SubAgentConfig) *Manager {
	max := cfg.MaxConcurrent
	if max <= 0 {
		max = 5
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	return &Manager{
		agents:     make(map[string]*SubAgent),
		sem:        make(chan struct{}, max),
		timeout:    timeout,
		showOutput: cfg.ShowOutput,
	}
}

// Spawn creates a new sub-agent with the given task and returns its ID.
func (m *Manager) Spawn(task, displayTask string, tools []string, ctx context.Context) string {
	m.mu.Lock()
	m.nextID++
	id := fmt.Sprintf("sa-%d", m.nextID)
	m.mu.Unlock()

	sa := &SubAgent{
		ID:           id,
		Task:         task,
		DisplayTask:  displayTask,
		Tools:        tools,
		Status:       StatusPending,
		CurrentPhase: "pending",
		CreatedAt:    time.Now(),
	}

	m.mu.Lock()
	m.agents[id] = sa
	m.mu.Unlock()

	return id
}

// Get retrieves a sub-agent by ID.
func (m *Manager) Get(id string) (*SubAgent, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sa, ok := m.agents[id]
	return sa, ok
}

// List returns all sub-agents.
func (m *Manager) List() []*SubAgent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*SubAgent, 0, len(m.agents))
	for _, sa := range m.agents {
		out = append(out, sa)
	}
	return out
}

// RunningCount returns the number of currently running agents.
func (m *Manager) RunningCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, sa := range m.agents {
		if sa.Status == StatusRunning {
			count++
		}
	}
	return count
}

// Cancel cancels a running sub-agent.
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	sa, ok := m.agents[id]
	m.mu.Unlock()
	if !ok {
		return false
	}
	sa.mu.Lock()
	defer sa.mu.Unlock()
	if sa.Status != StatusRunning {
		return false
	}
	if sa.cancel != nil {
		sa.cancel()
	}
	sa.Status = StatusCancelled
	sa.EndedAt = time.Now()
	return true
}

// SetCancel stores the cancel function for a sub-agent.
func (m *Manager) SetCancel(id string, cancel context.CancelFunc) {
	m.mu.Lock()
	sa, ok := m.agents[id]
	m.mu.Unlock()
	if ok {
		sa.mu.Lock()
		sa.cancel = cancel
		sa.Status = StatusRunning
		sa.mu.Unlock()
		m.notifyUpdate(sa)
	}
}

// Complete marks a sub-agent as completed or failed.
func (m *Manager) Complete(id string, result string, err error) {
	m.mu.Lock()
	sa, ok := m.agents[id]
	m.mu.Unlock()
	if !ok {
		return
	}
	sa.mu.Lock()
	if err != nil {
		sa.Status = StatusFailed
		sa.CurrentPhase = "failed"
		sa.CurrentTool = ""
		sa.CurrentArgs = ""
		sa.Error = err
	} else {
		sa.Status = StatusCompleted
		sa.CurrentPhase = "completed"
		sa.CurrentTool = ""
		sa.CurrentArgs = ""
	}
	sa.Result = result
	sa.EndedAt = time.Now()
	sa.mu.Unlock()

	if m.onComplete != nil {
		m.onComplete(sa)
	}
}

func (m *Manager) UpdateProgress(id, summary string) {
	m.mu.Lock()
	sa, ok := m.agents[id]
	m.mu.Unlock()
	if !ok {
		return
	}
	sa.setProgressSummary(summary)
	m.notifyUpdate(sa)
}

func (m *Manager) Notify(id string) {
	m.mu.Lock()
	sa, ok := m.agents[id]
	m.mu.Unlock()
	if ok {
		m.notifyUpdate(sa)
	}
}

// SetOnComplete sets a callback invoked when any sub-agent completes.
func (m *Manager) SetOnComplete(fn func(*SubAgent)) {
	m.onComplete = fn
}

// SetOnUpdate sets a callback invoked when any sub-agent activity changes.
func (m *Manager) SetOnUpdate(fn func(*SubAgent)) {
	m.onUpdate = fn
}

// AcquireSemaphore blocks until a slot is available for a new sub-agent to run.
func (m *Manager) AcquireSemaphore(ctx context.Context) error {
	select {
	case m.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ReleaseSemaphore releases a slot.
func (m *Manager) ReleaseSemaphore() {
	<-m.sem
}

// Timeout returns the configured timeout.
func (m *Manager) Timeout() time.Duration {
	return m.timeout
}

// ShowOutput returns whether to show sub-agent output.
func (m *Manager) ShowOutput() bool {
	return m.showOutput
}

func (m *Manager) notifyUpdate(sa *SubAgent) {
	if m.onUpdate != nil {
		m.onUpdate(sa)
	}
}
