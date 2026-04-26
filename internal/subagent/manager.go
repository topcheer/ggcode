package subagent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// AgentEventType identifies the kind of event recorded during sub-agent execution.
type AgentEventType int

const (
	AgentEventText       AgentEventType = iota // LLM text output
	AgentEventToolCall                         // tool invocation started
	AgentEventToolResult                       // tool execution result
	AgentEventError                            // error encountered
)

// AgentEvent is a single recorded event from a sub-agent's execution.
type AgentEvent struct {
	Type     AgentEventType
	Text     string // AgentEventText / AgentEventError
	ToolName string // AgentEventToolCall / AgentEventToolResult
	ToolArgs string // AgentEventToolCall
	Result   string // AgentEventToolResult
	IsError  bool   // AgentEventToolResult / AgentEventError
}

const maxAgentEvents = 200

// Status represents the lifecycle state of a sub-agent.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// AgentMessage represents a message sent to a sub-agent.
type AgentMessage struct {
	From    string
	Message string
	Summary string
}

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
	Mailbox         chan AgentMessage
	events          []AgentEvent
	cancel          context.CancelFunc
	mu              sync.Mutex
}

type Snapshot struct {
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
	Error           string
	CreatedAt       time.Time
	StartedAt       time.Time
	EndedAt         time.Time
	Events          []AgentEvent
}

// RecordEvent appends an event to the sub-agent's event log.
// This is the exported version for external callers (e.g., tests).
func (s *SubAgent) RecordEvent(ev AgentEvent) {
	s.appendEvent(ev)
}

func (s *SubAgent) appendEvent(ev AgentEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) >= maxAgentEvents {
		s.events = s.events[1:]
	}
	s.events = append(s.events, ev)
}

// Events returns a copy of the recorded events.
func (s *SubAgent) Events() []AgentEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AgentEvent, len(s.events))
	copy(out, s.events)
	return out
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

func (s *SubAgent) getStatus() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Status
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

func (s *SubAgent) snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{
		ID:              s.ID,
		Task:            s.Task,
		DisplayTask:     s.DisplayTask,
		Tools:           append([]string(nil), s.Tools...),
		ToolCallCount:   s.ToolCallCount,
		Status:          s.Status,
		CurrentPhase:    s.CurrentPhase,
		CurrentTool:     s.CurrentTool,
		CurrentArgs:     s.CurrentArgs,
		ProgressSummary: s.ProgressSummary,
		Result:          s.Result,
		CreatedAt:       s.CreatedAt,
		StartedAt:       s.StartedAt,
		EndedAt:         s.EndedAt,
		Events:          append([]AgentEvent(nil), s.events...),
	}
	if s.Error != nil {
		snap.Error = s.Error.Error()
	}
	return snap
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
	// rootCtx is the lifecycle ctx for sub-agents. It is independent of any
	// per-call/per-submit ctx so that sub-agents survive the parent agent
	// turn that spawned them. It is cancelled by Shutdown(). See locks.md S6.
	rootCtx    context.Context
	rootCancel context.CancelFunc
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
	rootCtx, rootCancel := context.WithCancel(context.Background())
	return &Manager{
		agents:     make(map[string]*SubAgent),
		sem:        make(chan struct{}, max),
		timeout:    timeout,
		showOutput: cfg.ShowOutput,
		rootCtx:    rootCtx,
		rootCancel: rootCancel,
	}
}

// RootContext returns the manager's lifecycle context. spawn_agent uses this
// (instead of the per-tool-call ctx) so that sub-agents are not cancelled the
// moment the parent agent's current turn ends.
func (m *Manager) RootContext() context.Context {
	if m.rootCtx == nil {
		return context.Background()
	}
	return m.rootCtx
}

// Shutdown cancels every running sub-agent. Call once during app shutdown.
func (m *Manager) Shutdown() {
	if m.rootCancel != nil {
		m.rootCancel()
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
		Mailbox:      make(chan AgentMessage, 16),
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

// GetOutput returns the result of a completed (or in-progress) sub-agent.
// Returns (result, true) if the agent exists, ("", false) otherwise.
func (m *Manager) GetTaskOutput(id string) (string, bool) {
	sa, ok := m.Get(id)
	if !ok {
		return "", false
	}
	snap := sa.snapshot()
	if snap.Result != "" {
		return snap.Result, true
	}
	if snap.ProgressSummary != "" {
		return "[in progress] " + snap.ProgressSummary, true
	}
	if snap.Status == "running" {
		return "[running, no output yet]", true
	}
	return "", true
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

func (m *Manager) Snapshot(id string) (Snapshot, bool) {
	m.mu.Lock()
	sa, ok := m.agents[id]
	m.mu.Unlock()
	if !ok {
		return Snapshot{}, false
	}
	return sa.snapshot(), true
}

// RunningCount returns the number of currently running agents.
func (m *Manager) RunningCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, sa := range m.agents {
		if sa.getStatus() == StatusRunning {
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
		if sa.StartedAt.IsZero() {
			sa.StartedAt = time.Now()
		}
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

func (m *Manager) UpdateActivity(id, phase, toolName, args string) {
	m.mu.Lock()
	sa, ok := m.agents[id]
	m.mu.Unlock()
	if !ok {
		return
	}
	sa.setActivity(phase, toolName, args)
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

// SendToAgent sends a message to a specific sub-agent's mailbox.
func (m *Manager) SendToAgent(id string, msg AgentMessage) error {
	m.mu.Lock()
	sa, ok := m.agents[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("sub-agent %q not found", id)
	}
	select {
	case sa.Mailbox <- msg:
		return nil
	default:
		return fmt.Errorf("sub-agent %q mailbox is full", id)
	}
}

// Broadcast sends a message to all running sub-agents.
func (m *Manager) Broadcast(msg AgentMessage) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var sent []string
	for _, sa := range m.agents {
		if sa.getStatus() == StatusRunning {
			select {
			case sa.Mailbox <- msg:
				sent = append(sent, sa.ID)
			default:
				// mailbox full, skip
			}
		}
	}
	return sent
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
