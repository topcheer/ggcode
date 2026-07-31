package subagent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// AgentEventType identifies the kind of event recorded during sub-agent execution.
type AgentEventType int

const (
	AgentEventText       AgentEventType = iota // LLM text output
	AgentEventToolCall                         // tool invocation started
	AgentEventToolResult                       // tool execution result
	AgentEventError                            // error encountered
	AgentEventReasoning                        // LLM thinking/reasoning content
)

// AgentEvent is a single recorded event from a sub-agent's execution.
type AgentEvent struct {
	Type            AgentEventType
	Text            string    // AgentEventText / AgentEventError
	ToolName        string    // AgentEventToolCall / AgentEventToolResult
	ToolID          string    // AgentEventToolCall / AgentEventToolResult — unique ID for precise matching
	ToolArgs        string    // AgentEventToolCall / AgentEventToolResult
	ToolDisplayName string    // AgentEventToolCall / AgentEventToolResult
	ToolDetail      string    // AgentEventToolCall / AgentEventToolResult
	Result          string    // AgentEventToolResult
	IsError         bool      // AgentEventToolResult / AgentEventError
	Time            time.Time // when the event was recorded
}

const maxAgentEvents = 400

// maxConcurrentSubAgents limits how many sub-agents can run simultaneously.
// Each sub-agent consumes a goroutine, an LLM API connection, and context
// window tokens. Without a limit, an agent can spawn dozens of sub-agents
// in parallel, exhausting API rate limits and memory.
const maxConcurrentSubAgents = 5

// maxSubAgentResultBytes caps the result text stored and returned by a
// sub-agent. Without this, a sub-agent that reads large files can produce
// megabytes of output that floods the parent agent's context window.
// 100KB is generous enough for normal results while preventing abuse.
const maxSubAgentResultBytes = 100 * 1024

// cancelAllTimeout is the maximum time CancelAll waits for each sub-agent to
// actually terminate after its context is cancelled. Sub-agents running long
// LLM streams or external tool calls may not finish instantly.
const cancelAllTimeout = 5 * time.Second

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
	ID               string
	Name             string // short, meaningful label (required)
	Task             string
	DisplayTask      string
	Model            string
	Tools            []string
	ToolCallCount    int
	Status           Status
	CurrentPhase     string
	CurrentTool      string
	CurrentArgs      string
	ProgressSummary  string
	Result           string
	Error            error
	CreatedAt        time.Time
	StartedAt        time.Time
	EndedAt          time.Time
	Mailbox          chan AgentMessage
	events           []AgentEvent
	eventsDropped    int
	cancel           context.CancelFunc
	done             chan struct{} // closed when the sub-agent reaches any terminal state
	goroutineStarted bool          // true once Run() has started the goroutine (SetCancel alone doesn't set this)
	lastActivity     time.Time     // updated on every LLM chunk / tool call; used by watchdog
	mu               sync.Mutex
}

type Snapshot struct {
	ID              string
	Name            string // short, meaningful label
	Task            string
	DisplayTask     string
	Model           string
	Tools           []string
	ToolCallCount   int
	Status          Status
	CurrentPhase    string
	CurrentTool     string
	CurrentArgs     string
	ProgressSummary string
	Result          string
	Error           string
	EventsDropped   int
	CreatedAt       time.Time
	StartedAt       time.Time
	EndedAt         time.Time
	Events          []AgentEvent
}

// StatusInfo is a lightweight copy of a sub-agent's identity and status.
// Unlike Snapshot, it does NOT copy events, making it safe for high-frequency
// calls (e.g. strip display refresh) without O(maxAgentEvents) copy overhead.
type StatusInfo struct {
	ID           string
	Name         string
	Model        string
	Status       Status
	CurrentPhase string
	EndedAt      time.Time
}

// RecordEvent appends an event to the sub-agent's event log.
// This is the exported version for external callers (e.g., tests).
func (s *SubAgent) RecordEvent(ev AgentEvent) {
	s.appendEvent(ev)
}

// AppendEvent adds an event to the sub-agent's history (thread-safe).
// Used for testing and for external event injection.
func (s *SubAgent) AppendEvent(ev AgentEvent) {
	s.appendEvent(ev)
}

// textMergeInterval is the maximum gap between consecutive text events
// before they are flushed as separate events. Events arriving within this
// window are coalesced into one, reducing event spam from fine-grained
// streaming providers (e.g., GLM sends 3-5 token chunks).
const textMergeInterval = 50 * time.Millisecond

// textMergeMaxChars is the maximum accumulated text before a merged event
// is flushed, even if more text is still arriving. This ensures the follow
// panel shows progressive output rather than waiting indefinitely.
const textMergeMaxChars = 2000

// isTurnBoundary returns true if the event marks a boundary between LLM turns
// (tool calls/results), meaning any text before it represents a complete turn.
func isTurnBoundary(ev AgentEvent) bool {
	return ev.Type == AgentEventToolCall || ev.Type == AgentEventToolResult
}

func (s *SubAgent) appendEvent(ev AgentEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	ev.Time = now

	// Text event coalescing: if the last event is also text and arrived
	// within textMergeInterval, merge into it. Flush if accumulated text
	// exceeds textMergeMaxChars to preserve streaming feel.
	if ev.Type == AgentEventText && len(s.events) > 0 {
		last := &s.events[len(s.events)-1]
		if last.Type == AgentEventText && now.Sub(last.Time) < textMergeInterval {
			last.Text += ev.Text
			last.Time = now
			if len(last.Text) > textMergeMaxChars {
				// Mark as flushed by setting Time to zero so the next chunk
				// starts a new event even if it arrives quickly.
				last.Time = time.Time{}
			}
			return
		}
	}

	// Turn-aware eviction: when the event buffer is full, drop events up to
	// the next turn boundary (tool_call/tool_result) so the oldest visible
	// text is always a complete LLM turn, not a fragment.
	if len(s.events) >= maxAgentEvents {
		dropIdx := 0
		for i, e := range s.events {
			if isTurnBoundary(e) {
				dropIdx = i + 1
				break
			}
		}
		// If no boundary found (all text), drop 10% to avoid 1-by-1 churn.
		if dropIdx == 0 {
			dropIdx = len(s.events) / 10
			if dropIdx < 1 {
				dropIdx = 1
			}
		}
		s.events = s.events[dropIdx:]
		s.eventsDropped += dropIdx
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

// EventsSince returns only events with index >= fromIdx.
// This avoids copying the full event history when only incremental events
// are needed (e.g. GUI agent panel updates). Returns the total event count
// so callers can track the next fromIdx.
func (s *SubAgent) EventsSince(fromIdx int) ([]AgentEvent, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := len(s.events)
	if fromIdx >= total {
		return nil, total
	}
	out := make([]AgentEvent, total-fromIdx)
	copy(out, s.events[fromIdx:])
	return out, total
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
	s.lastActivity = time.Now()
}

func (s *SubAgent) setProgressSummary(summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ProgressSummary = summary
}

func (s *SubAgent) setStartedAt(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.StartedAt = t
}

func (s *SubAgent) snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{
		ID:              s.ID,
		Name:            s.Name,
		Task:            s.Task,
		DisplayTask:     s.DisplayTask,
		Model:           s.Model,
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
		EventsDropped:   s.eventsDropped,
	}
	if s.Error != nil {
		snap.Error = s.Error.Error()
	}
	return snap
}

// statusInfo returns a lightweight copy with only identity + status.
// Unlike snapshot(), it does NOT copy events.
func (s *SubAgent) statusInfo() StatusInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return StatusInfo{
		ID:           s.ID,
		Name:         s.Name,
		Model:        s.Model,
		Status:       s.Status,
		CurrentPhase: s.CurrentPhase,
		EndedAt:      s.EndedAt,
	}
}

// Manager manages spawning, tracking, and collecting results from sub-agents.
// streamBatchInterval controls how often accumulated sub-agent stream text
// and reasoning chunks are flushed to the TUI. Without batching, each LLM
// token (~50-100/s per agent) triggers a separate program.Send → Bubble Tea
// Update(), flooding the event loop and causing severe TUI stuttering.
const streamBatchInterval = 500 * time.Millisecond

type Manager struct {
	agents       map[string]*SubAgent
	mu           sync.Mutex
	sem          chan struct{}
	timeout      time.Duration
	showOutput   bool
	onUpdate     func(*SubAgent)
	onComplete   func(*SubAgent)
	onStreamText func(agentID, text string)                                                        // called on batched text
	onReasoning  func(agentID, text string)                                                        // called on batched reasoning
	onToolCall   func(agentID, toolID, toolName, displayName, args, detail string)                 // called on tool call
	onToolResult func(agentID, toolID, toolName, displayName, detail, result string, isError bool) // called on tool result
	onSystem     func(agentID, text string)                                                        // called on system events (retry, compaction)
	lastNotify   time.Time                                                                         // throttle: last time onUpdate was called
	nextID       int
	// cancelAllTimeout is the max time CancelAll waits for each Running sub-agent's
	// goroutine to actually terminate after context cancellation. Default: 5s.
	// Overridable for tests.
	cancelAllTimeout time.Duration
	// rootCtx is the lifecycle ctx for sub-agents. It is independent of any
	// per-call/per-submit ctx so that sub-agents survive the parent agent
	// turn that spawned them. It is cancelled by Shutdown(). See locks.md S6.
	rootCtx    context.Context
	rootCancel context.CancelFunc

	// streamBatch accumulates text/reasoning chunks per agent and flushes
	// them at streamBatchInterval. This prevents the TUI event loop from
	// being flooded with per-token messages when multiple sub-agents stream
	// concurrently. Guarded by streamBatchMu.
	streamBatchMu     sync.Mutex
	streamTextBuf     map[string]*strings.Builder // agentID → accumulated text
	streamRsnBuf      map[string]*strings.Builder // agentID → accumulated reasoning
	streamBatchDone   chan struct{}               // closed to stop the ticker goroutine
	shutdownOnce      sync.Once
	watchdogDone      chan struct{} // closed to stop the watchdog goroutine
	inactivityTimeout time.Duration // max time without activity before cancelling
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
	m := &Manager{
		agents:            make(map[string]*SubAgent),
		sem:               make(chan struct{}, max),
		timeout:           timeout,
		showOutput:        cfg.ShowOutput,
		rootCtx:           rootCtx,
		rootCancel:        rootCancel,
		streamTextBuf:     make(map[string]*strings.Builder),
		streamRsnBuf:      make(map[string]*strings.Builder),
		streamBatchDone:   make(chan struct{}),
		watchdogDone:      make(chan struct{}),
		inactivityTimeout: 5 * time.Minute,
	}
	m.startWatchdog()
	return m
}

// startWatchdog launches a background goroutine that periodically checks all
// running sub-agents for inactivity. If a sub-agent has not updated its
// lastActivity timestamp within the inactivity timeout, it is cancelled
// to prevent zombie processes from consuming concurrency slots indefinitely.
func (m *Manager) startWatchdog() {
	safego.Go("subagent.watchdog", func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.reapInactiveAgents()
			case <-m.watchdogDone:
				return
			}
		}
	})
}

// reapInactiveAgents cancels any running sub-agent whose lastActivity
// timestamp is older than the inactivity timeout.
func (m *Manager) reapInactiveAgents() {
	threshold := time.Now().Add(-m.inactivityTimeout)
	m.mu.Lock()
	var stale []string
	for id, sa := range m.agents {
		sa.mu.Lock()
		isRunning := sa.Status == StatusRunning && sa.goroutineStarted
		isStale := !sa.lastActivity.IsZero() && sa.lastActivity.Before(threshold)
		sa.mu.Unlock()
		if isRunning && isStale {
			stale = append(stale, id)
		}
	}
	m.mu.Unlock()
	for _, id := range stale {
		debug.Log("subagent", "watchdog: cancelling stale sub-agent %s (no activity for %v)", id, m.inactivityTimeout)
		m.Cancel(id)
	}
	// Also purge old terminal agents to bound memory growth
	m.purgeTerminalAgents()
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
// Safe to call multiple times (idempotent).
func (m *Manager) Shutdown() {
	m.shutdownOnce.Do(func() {
		close(m.streamBatchDone)
		close(m.watchdogDone)
	})
	// Flush any remaining buffered text before shutdown.
	m.flushStreamBatch()
	if m.rootCancel != nil {
		m.rootCancel()
	}
}

// Spawn creates a new sub-agent with the given task and returns its ID.
func (m *Manager) Spawn(name, task, displayTask string, tools []string, ctx context.Context) string {
	// Enforce concurrent sub-agent limit to prevent resource exhaustion.
	m.mu.Lock()
	running := 0
	for _, sa := range m.agents {
		if sa.Status == StatusRunning || sa.Status == StatusPending {
			running++
		}
	}
	if running >= maxConcurrentSubAgents {
		m.mu.Unlock()
		// Return a synthetic error ID that wait_agent will report as failed.
		errID := fmt.Sprintf("sa-limit-%d", time.Now().UnixNano())
		sa := &SubAgent{
			ID:           errID,
			Name:         name,
			Task:         task,
			DisplayTask:  displayTask,
			Status:       StatusFailed,
			CurrentPhase: "rejected",
			CreatedAt:    time.Now(),
			Error:        fmt.Errorf("concurrent sub-agent limit reached (%d). Wait for existing sub-agents to finish before spawning new ones.", maxConcurrentSubAgents),
			done:         make(chan struct{}),
		}
		close(sa.done)
		m.agents[errID] = sa
		return errID
	}
	m.nextID++
	id := fmt.Sprintf("sa-%d", m.nextID)
	m.mu.Unlock()

	sa := &SubAgent{
		ID:           id,
		Name:         name,
		Task:         task,
		DisplayTask:  displayTask,
		Tools:        tools,
		Status:       StatusPending,
		CurrentPhase: "pending",
		CreatedAt:    time.Now(),
		Mailbox:      make(chan AgentMessage, 16),
		done:         make(chan struct{}),
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

// SnapshotByID returns a stable copy of a sub-agent snapshot.
func (m *Manager) SnapshotByID(id string) (Snapshot, bool) {
	sa, ok := m.Get(id)
	if !ok || sa == nil {
		return Snapshot{}, false
	}
	return sa.snapshot(), true
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

// Statuses returns lightweight status info for all sub-agents.
// Unlike List() + Snapshot(), this does NOT copy events and is safe to call
// at high frequency (e.g. strip display refresh).
func (m *Manager) Statuses() []StatusInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]StatusInfo, 0, len(m.agents))
	for _, sa := range m.agents {
		out = append(out, sa.statusInfo())
	}
	return out
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
		debug.Log("cancel", "Cancel: agent not found id=%s", id)
		return false
	}
	sa.mu.Lock()
	switch sa.Status {
	case StatusCompleted, StatusFailed, StatusCancelled:
		debug.Log("cancel", "Cancel: agent already terminal id=%s status=%s", id, sa.Status)
		sa.mu.Unlock()
		return false
	}
	if sa.cancel != nil {
		sa.cancel()
		sa.cancel = nil
	}
	sa.Status = StatusCancelled
	sa.CurrentPhase = "cancelled"
	sa.Error = context.Canceled
	sa.EndedAt = time.Now()
	debug.Log("cancel", "Cancel: set Status=Cancelled id=%s goroutineStarted=%v", id, sa.goroutineStarted)
	// Do NOT close done here — let the goroutine's Complete() call close it
	// when it actually terminates. This allows CancelAll()'s wait loop to
	// genuinely wait for goroutine termination (with timeout fallback).
	// Complete() calls closeDone() in all paths, including when Status is
	// already Cancelled (terminal state check).
	sa.mu.Unlock()
	m.notifyUpdate(sa)
	return true
}

// closeDone closes the done channel if not already closed. Caller must hold sa.mu.
func (sa *SubAgent) closeDone() {
	if sa.done != nil {
		select {
		case <-sa.done:
		default:
			close(sa.done)
		}
	}
}

// isGoroutineStarted reports whether the agent's goroutine has actually started.
// Caller must NOT hold sa.mu.
func (sa *SubAgent) isGoroutineStarted() bool {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	return sa.goroutineStarted
}

// CancelAll cancels all pending or running sub-agents, then waits up to
// cancelAllTimeout for each Running sub-agent's goroutine to actually
// terminate. Returns the number cancelled.
//
// Only Running agents (which have an active goroutine) are waited on.
// Pending agents (no goroutine started yet) are cancelled but not waited
// on, since there is nothing to wait for.
func (m *Manager) CancelAll() int {
	timeout := m.cancelAllTimeout
	if timeout == 0 {
		timeout = cancelAllTimeout
	}
	debug.Log("cancel", "CancelAll: START timeout=%v", timeout)

	m.mu.Lock()
	ids := make([]string, 0)
	// Collect done channels for Running agents BEFORE calling Cancel().
	// Cancel() no longer closes done (it lets Complete() do that when the
	// goroutine actually exits), so we must grab the channels while we can
	// still distinguish Running (has goroutine) from Pending (no goroutine).
	var doneChs []<-chan struct{}
	for id, sa := range m.agents {
		status := sa.getStatus()
		if status == StatusPending || status == StatusRunning {
			ids = append(ids, id)
			// Only wait on agents whose goroutine was actually started by Run().
			// SetCancel() transitions Pending→Running but doesn't start a goroutine;
			// only Run() sets goroutineStarted=true.
			if sa.isGoroutineStarted() && sa.done != nil {
				doneChs = append(doneChs, sa.done)
			}
		}
	}
	m.mu.Unlock()

	cancelled := 0
	for _, id := range ids {
		if m.Cancel(id) {
			cancelled++
		}
	}
	debug.Log("cancel", "CancelAll: cancelled %d agents, waiting on %d goroutines", cancelled, len(doneChs))

	// Wait for Running agents' goroutines to actually terminate (with timeout).
	if len(doneChs) > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		for _, ch := range doneChs {
			select {
			case <-ch:
				debug.Log("cancel", "CancelAll: goroutine terminated normally")
			case <-timer.C:
				// Timed out waiting — the sub-agent's goroutine may still be
				// finishing. We've already set Status=Cancelled and cancelled
				// its context, so it will terminate eventually.
				debug.Log("cancel", "CancelAll: TIMEOUT waiting for goroutine termination")
				return cancelled
			}
		}
	}
	debug.Log("cancel", "CancelAll: DONE all goroutines terminated, cancelled=%d", cancelled)
	return cancelled
}

// SetCancel stores the cancel function for a sub-agent.
// Returns false if the sub-agent is already terminal and must not be started.
func (m *Manager) SetCancel(id string, cancel context.CancelFunc) bool {
	m.mu.Lock()
	sa, ok := m.agents[id]
	m.mu.Unlock()
	if !ok {
		if cancel != nil {
			cancel()
		}
		return false
	}
	sa.mu.Lock()
	switch sa.Status {
	case StatusCompleted, StatusFailed, StatusCancelled:
		sa.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return false
	}
	sa.cancel = cancel
	sa.Status = StatusRunning
	if sa.StartedAt.IsZero() {
		sa.StartedAt = time.Now()
	}
	sa.mu.Unlock()
	m.notifyUpdate(sa)
	return true
}

// Complete marks a sub-agent as completed or failed.
func (m *Manager) Complete(id string, result string, err error) {
	m.mu.Lock()
	sa, ok := m.agents[id]
	onComplete := m.onComplete
	m.mu.Unlock()
	if !ok {
		return
	}
	sa.mu.Lock()
	// Terminal state check: don't overwrite cancelled/completed/failed
	switch sa.Status {
	case StatusCancelled, StatusCompleted, StatusFailed:
		sa.closeDone()
		sa.mu.Unlock()
		return
	}
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
	sa.closeDone()
	sa.mu.Unlock()

	if onComplete != nil {
		onComplete(sa)
	}
	m.notifyUpdate(sa)
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

// SetOnStreamText sets a callback invoked on each text chunk from any sub-agent.
// Unlike OnUpdate, this is NOT throttled.
func (m *Manager) SetOnStreamText(fn func(agentID, text string)) {
	m.mu.Lock()
	m.onStreamText = fn
	m.mu.Unlock()
}

func (m *Manager) SetOnReasoning(fn func(agentID, text string)) {
	m.mu.Lock()
	m.onReasoning = fn
	m.mu.Unlock()
}

func (m *Manager) SetOnToolCall(fn func(agentID, toolID, toolName, displayName, args, detail string)) {
	m.mu.Lock()
	m.onToolCall = fn
	m.mu.Unlock()
}

func (m *Manager) SetOnToolResult(fn func(agentID, toolID, toolName, displayName, detail, result string, isError bool)) {
	m.mu.Lock()
	m.onToolResult = fn
	m.mu.Unlock()
}

func (m *Manager) SetOnSystem(fn func(agentID, text string)) {
	m.mu.Lock()
	m.onSystem = fn
	m.mu.Unlock()
}

func (m *Manager) NotifySystem(agentID, text string) {
	m.mu.Lock()
	fn := m.onSystem
	m.mu.Unlock()
	if fn != nil {
		fn(agentID, text)
	}
}

func (m *Manager) NotifyToolCall(agentID, toolID, toolName, displayName, args, detail string) {
	m.mu.Lock()
	fn := m.onToolCall
	m.mu.Unlock()
	if fn != nil {
		fn(agentID, toolID, toolName, displayName, args, detail)
	}
}

func (m *Manager) NotifyToolResult(agentID, toolID, toolName, displayName, detail, result string, isError bool) {
	m.mu.Lock()
	fn := m.onToolResult
	m.mu.Unlock()
	if fn != nil {
		fn(agentID, toolID, toolName, displayName, detail, result, isError)
	}
}

// NotifyStreamText buffers a text chunk for batched delivery to the
// onStreamText callback. Chunks are accumulated per-agent and flushed
// at streamBatchInterval to avoid flooding the TUI event loop.
func (m *Manager) NotifyStreamText(agentID, text string) {
	m.mu.Lock()
	fn := m.onStreamText
	m.mu.Unlock()
	if fn == nil {
		return
	}
	m.streamBatchMu.Lock()
	buf, ok := m.streamTextBuf[agentID]
	if !ok {
		buf = &strings.Builder{}
		m.streamTextBuf[agentID] = buf
	}
	buf.WriteString(text)
	m.streamBatchMu.Unlock()
}

// NotifyReasoning buffers a reasoning chunk for batched delivery.
func (m *Manager) NotifyReasoning(agentID, text string) {
	m.mu.Lock()
	fn := m.onReasoning
	m.mu.Unlock()
	if fn == nil {
		return
	}
	m.streamBatchMu.Lock()
	buf, ok := m.streamRsnBuf[agentID]
	if !ok {
		buf = &strings.Builder{}
		m.streamRsnBuf[agentID] = buf
	}
	buf.WriteString(text)
	m.streamBatchMu.Unlock()
}

// StartStreamBatcher starts the background goroutine that periodically
// flushes accumulated stream text/reasoning to the registered callbacks.
// Must be called after SetOnStreamText/SetOnReasoning.
func (m *Manager) StartStreamBatcher() {
	safego.Go("subagent.streamBatcher", func() {
		ticker := time.NewTicker(streamBatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.flushStreamBatch()
			case <-m.streamBatchDone:
				return
			case <-m.rootCtx.Done():
				m.flushStreamBatch()
				return
			}
		}
	})
}

// flushStreamBatch delivers all accumulated text/reasoning chunks to
// their respective callbacks in a single burst, then clears the buffers.
func (m *Manager) flushStreamBatch() {
	m.streamBatchMu.Lock()
	textBufs := m.streamTextBuf
	rsnBufs := m.streamRsnBuf
	m.streamTextBuf = make(map[string]*strings.Builder)
	m.streamRsnBuf = make(map[string]*strings.Builder)
	m.streamBatchMu.Unlock()

	if len(textBufs) == 0 && len(rsnBufs) == 0 {
		return
	}

	m.mu.Lock()
	onText := m.onStreamText
	onRsn := m.onReasoning
	m.mu.Unlock()

	for id, buf := range textBufs {
		if buf.Len() > 0 && onText != nil {
			onText(id, buf.String())
		}
	}
	for id, buf := range rsnBufs {
		if buf.Len() > 0 && onRsn != nil {
			onRsn(id, buf.String())
		}
	}
}

// FlushStreamBatch exports flushStreamBatch for use in tests that need
// synchronous delivery of buffered stream events.
func (m *Manager) FlushStreamBatch() {
	m.flushStreamBatch()
}

// SetOnComplete sets a callback invoked when any sub-agent completes.
func (m *Manager) SetOnComplete(fn func(*SubAgent)) {
	m.mu.Lock()
	m.onComplete = fn
	m.mu.Unlock()
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
	m.mu.Lock()
	m.onUpdate = fn
	m.mu.Unlock()
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

// notifyUpdate invokes the onUpdate callback. This is called frequently
// during streaming (every token), so we throttle to ~10 Hz to avoid
// flooding Bubble Tea's unbuffered message channel, which would starve
// the spinner and other UI updates.
func (m *Manager) notifyUpdate(sa *SubAgent) {
	m.mu.Lock()
	fn := m.onUpdate
	now := time.Now()
	lastNotify := m.lastNotify
	m.lastNotify = now
	m.mu.Unlock()
	if fn == nil {
		return
	}
	// Throttle: skip if we notified less than 100ms ago.
	if !lastNotify.IsZero() && now.Sub(lastNotify) < 100*time.Millisecond {
		return
	}
	fn(sa)
}

// maxTerminalAgents is the maximum number of terminal (completed/failed/
// cancelled) sub-agents retained in the map. Older terminal entries are
// purged to prevent unbounded memory growth in long-running sessions.
const maxTerminalAgents = 20

// purgeTerminalAgents removes the oldest terminal sub-agents when the
// count exceeds maxTerminalAgents. Called from reapInactiveAgents to
// piggyback on the watchdog's periodic sweep.
func (m *Manager) purgeTerminalAgents() {
	type terminalEntry struct {
		id      string
		endedAt time.Time
	}
	m.mu.Lock()
	var terminals []terminalEntry
	for id, sa := range m.agents {
		sa.mu.Lock()
		isTerminal := sa.Status == StatusCompleted || sa.Status == StatusFailed || sa.Status == StatusCancelled
		ended := sa.EndedAt
		sa.mu.Unlock()
		if isTerminal {
			terminals = append(terminals, terminalEntry{id: id, endedAt: ended})
		}
	}
	if len(terminals) <= maxTerminalAgents {
		m.mu.Unlock()
		return
	}
	// Sort by EndedAt ascending (oldest first)
	sort.Slice(terminals, func(i, j int) bool {
		return terminals[i].endedAt.Before(terminals[j].endedAt)
	})
	// Delete oldest entries until we're at the limit
	toDelete := len(terminals) - maxTerminalAgents
	for i := 0; i < toDelete; i++ {
		delete(m.agents, terminals[i].id)
	}
	debug.Log("subagent", "purgeTerminalAgents: removed %d old terminal agents, %d remaining", toDelete, len(terminals)-toDelete)
	m.mu.Unlock()
}
