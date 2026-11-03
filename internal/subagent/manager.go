package subagent

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

type AgentEventType int

const (
	AgentEventText AgentEventType = iota
	AgentEventToolCall
	AgentEventToolResult
	AgentEventError
	AgentEventReasoning
)

type AgentEvent struct {
	Type            AgentEventType
	Text            string
	ToolName        string
	ToolID          string
	ToolArgs        string
	ToolDisplayName string
	ToolDetail      string
	Result          string
	IsError         bool
	Time            time.Time
}

const maxAgentEvents = 400

const maxSubAgentResultBytes = 100 * 1024

const cancelAllTimeout = 5 * time.Second

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

type AgentMessage struct {
	From    string
	Message string
	Summary string
}

type SubAgent struct {
	ID               string
	Name             string
	Task             string
	DisplayTask      string
	Model            string
	Worktree         string
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
	externalSources  map[string]int
	cancel           context.CancelFunc
	done             chan struct{}
	goroutineStarted bool
	lastActivity     time.Time
	toolExecuting    bool
	mu               sync.Mutex
}

type Snapshot struct {
	ID              string
	Name            string
	Task            string
	DisplayTask     string
	Model           string
	Worktree        string
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

	ExternalSources map[string]int
	CreatedAt       time.Time
	StartedAt       time.Time
	EndedAt         time.Time
	Events          []AgentEvent
}

type StatusInfo struct {
	ID           string
	Name         string
	Model        string
	Status       Status
	CurrentPhase string
	EndedAt      time.Time
}

func (s *SubAgent) RecordEvent(ev AgentEvent) {
	s.appendEvent(ev)
}

func (s *SubAgent) AppendEvent(ev AgentEvent) {
	s.appendEvent(ev)
}

const textMergeInterval = 50 * time.Millisecond

const textMergeMaxChars = 2000

func isTurnBoundary(ev AgentEvent) bool {
	return ev.Type == AgentEventToolCall || ev.Type == AgentEventToolResult
}

func (s *SubAgent) appendEvent(ev AgentEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	ev.Time = now

	isCoalescable := ev.Type == AgentEventText || ev.Type == AgentEventReasoning
	if isCoalescable && len(s.events) > 0 {
		last := &s.events[len(s.events)-1]
		if last.Type == ev.Type && now.Sub(last.Time) < textMergeInterval {
			last.Text += ev.Text
			last.Time = now
			if len(last.Text) > textMergeMaxChars {
				last.Time = time.Time{}
			}
			return
		}
	}

	if len(s.events) >= maxAgentEvents {
		dropIdx := 0
		for i, e := range s.events {
			if isTurnBoundary(e) {
				dropIdx = i + 1
				break
			}
		}
		if dropIdx == 0 {

			dropIdx = len(s.events) / 10
			if dropIdx < 1 {
				dropIdx = 1
			}
			var savedText strings.Builder
			for i := 0; i < dropIdx; i++ {
				if s.events[i].Type == AgentEventText {
					savedText.WriteString(s.events[i].Text)
				}
			}
			remaining := s.events[dropIdx:]
			if savedText.Len() > 0 && len(remaining) > 0 {
				if remaining[0].Type == AgentEventText {

					remaining[0].Text = savedText.String() + remaining[0].Text
				} else {

					anchor := remaining[0].Time
					rescue := AgentEvent{Type: AgentEventText, Text: savedText.String(), Time: anchor}
					kept := make([]AgentEvent, 0, len(remaining)+1)
					kept = append(kept, rescue)
					kept = append(kept, remaining...)
					remaining = kept
				}
			}
			s.events = remaining
		} else {
			s.events = s.events[dropIdx:]
		}
		s.eventsDropped += dropIdx
	}
	s.events = append(s.events, ev)
}

func (s *SubAgent) Events() []AgentEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AgentEvent, len(s.events))
	copy(out, s.events)
	return out
}

func (s *SubAgent) EventsSince(fromIdx int) ([]AgentEvent, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := len(s.events) + s.eventsDropped
	if fromIdx >= total {
		return nil, total
	}
	if fromIdx < s.eventsDropped {

		fromIdx = s.eventsDropped
	}
	out := make([]AgentEvent, total-fromIdx)
	copy(out, s.events[fromIdx-s.eventsDropped:])
	return out, total
}

func (s *SubAgent) IncrementToolCalls() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ToolCallCount++
}

func (s *SubAgent) RecordExternalSource(toolName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.externalSources == nil {
		s.externalSources = make(map[string]int)
	}
	s.externalSources[toolName]++
}

func (s *SubAgent) setStatus(st Status) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch s.Status {
	case StatusCompleted, StatusFailed, StatusCancelled:
		debug.Log("subagent", "setStatus: ignore %s transition, id=%s already terminal %s", st, s.ID, s.Status)
		return
	}
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

func (s *SubAgent) refreshActivity() {
	s.mu.Lock()
	s.lastActivity = time.Now()
	s.mu.Unlock()
}

func (s *SubAgent) setToolExecuting(executing bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolExecuting = executing

	if executing {
		s.lastActivity = time.Now()
	}
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
		Worktree:        s.Worktree,
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
		ExternalSources: maps.Clone(s.externalSources),
	}
	if s.Error != nil {
		snap.Error = s.Error.Error()
	}
	return snap
}

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

const streamBatchInterval = 500 * time.Millisecond

type Manager struct {
	agents       map[string]*SubAgent
	mu           sync.Mutex
	sem          chan struct{}
	timeout      time.Duration
	showOutput   bool
	onUpdate     func(*SubAgent)
	onComplete   func(*SubAgent)
	onStreamText func(agentID, text string)
	onReasoning  func(agentID, text string)
	onToolCall   func(agentID, toolID, toolName, displayName, args, detail string)
	onToolResult func(agentID, toolID, toolName, displayName, detail, result string, isError bool)
	onSystem     func(agentID, text string)
	lastNotify   map[string]time.Time
	nextID       int

	maxConcurrent int

	maxTotal     int
	totalSpawned int

	cancelAllTimeout time.Duration

	rootCtx    context.Context
	rootCancel context.CancelFunc

	streamBatchMu     sync.Mutex
	streamTextBuf     map[string]*strings.Builder
	streamRsnBuf      map[string]*strings.Builder
	streamBatchDone   chan struct{}
	shutdownOnce      sync.Once
	watchdogDone      chan struct{}
	inactivityTimeout time.Duration

	semOwners map[string]bool
}

func NewManager(cfg config.SubAgentConfig) *Manager {
	max := cfg.MaxConcurrent
	if max <= 0 {
		max = 16
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 180 * time.Minute
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
		maxConcurrent:     max,
		maxTotal:          cfg.MaxTotal,
		semOwners:         make(map[string]bool),
	}
	m.startWatchdog()
	return m
}

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

func (m *Manager) reapInactiveAgents() {
	threshold := time.Now().Add(-m.inactivityTimeout)
	m.mu.Lock()
	var stale []string
	var stalePending []string
	for id, sa := range m.agents {
		sa.mu.Lock()
		isRunning := sa.Status == StatusRunning && sa.goroutineStarted
		isPending := sa.Status == StatusPending && !sa.goroutineStarted
		isStale := !sa.lastActivity.IsZero() && sa.lastActivity.Before(threshold)
		toolRunning := sa.toolExecuting
		sa.mu.Unlock()
		if isRunning && isStale && !toolRunning {
			stale = append(stale, id)
			continue
		}

		if isPending && sa.CreatedAt.Before(threshold) {
			stalePending = append(stalePending, id)
		}
	}
	m.mu.Unlock()
	for _, id := range stale {
		debug.Log("subagent", "watchdog: cancelling stale sub-agent %s (no activity for %v)", id, m.inactivityTimeout)
		m.Cancel(id)

		staleID := id
		safego.Go("subagent.watchdog.reclaim", func() {
			m.ensureSlotReclaimed(staleID, m.reclaimGrace())
		})
	}

	for _, id := range stalePending {
		m.mu.Lock()
		sa, ok := m.agents[id]
		if !ok {
			m.mu.Unlock()
			continue
		}
		sa.mu.Lock()
		if sa.Status == StatusPending && !sa.goroutineStarted {
			sa.Status = StatusFailed
			sa.Error = fmt.Errorf("sub-agent never started (pending for over %v); spawn/Run contract violated", m.inactivityTimeout)
			sa.EndedAt = time.Now()
			if sa.done != nil {
				close(sa.done)
			}
		}
		sa.mu.Unlock()
		m.mu.Unlock()

		m.notifyUpdate(sa)
		debug.Log("subagent", "watchdog: failed never-started pending sub-agent %s (slot reclaimed)", id)
	}

	m.purgeTerminalAgents()
}

func (m *Manager) RootContext() context.Context {
	if m.rootCtx == nil {
		return context.Background()
	}
	return m.rootCtx
}

func (m *Manager) Shutdown() {
	m.shutdownOnce.Do(func() {
		close(m.streamBatchDone)
		close(m.watchdogDone)
	})

	m.flushStreamBatch()
	if m.rootCancel != nil {
		m.rootCancel()
	}
}

func (m *Manager) Spawn(name, task, displayTask string, tools []string, ctx context.Context) string {

	select {
	case <-m.rootCtx.Done():
		errID := fmt.Sprintf("sa-shutdown-%d", time.Now().UnixNano())
		sa := &SubAgent{
			ID:           errID,
			Name:         name,
			Task:         task,
			DisplayTask:  displayTask,
			Status:       StatusFailed,
			CurrentPhase: "rejected",
			CreatedAt:    time.Now(),
			Error:        fmt.Errorf("subagent manager is shutting down"),
			done:         make(chan struct{}),
		}
		close(sa.done)
		m.mu.Lock()
		m.agents[errID] = sa
		m.mu.Unlock()
		return errID
	default:
	}

	m.mu.Lock()

	if m.maxTotal > 0 && m.totalSpawned >= m.maxTotal {
		errID := fmt.Sprintf("sa-budget-%d", time.Now().UnixNano())
		sa := &SubAgent{
			ID:           errID,
			Name:         name,
			Task:         task,
			DisplayTask:  displayTask,
			Status:       StatusFailed,
			CurrentPhase: "rejected",
			CreatedAt:    time.Now(),
			Error:        fmt.Errorf("sub-agent spawn budget exhausted: %d/%d lifetime spawns used (subagents.max_total). Raise the budget in config or continue without new sub-agents.", m.totalSpawned, m.maxTotal),
			done:         make(chan struct{}),
		}
		close(sa.done)
		m.agents[errID] = sa
		m.mu.Unlock()
		return errID
	}
	running := 0
	for _, sa := range m.agents {
		sa.mu.Lock()
		if sa.Status == StatusRunning || sa.Status == StatusPending {
			running++
		}
		sa.mu.Unlock()
	}
	if running >= m.maxConcurrent {

		errID := fmt.Sprintf("sa-limit-%d", time.Now().UnixNano())
		sa := &SubAgent{
			ID:           errID,
			Name:         name,
			Task:         task,
			DisplayTask:  displayTask,
			Status:       StatusFailed,
			CurrentPhase: "rejected",
			CreatedAt:    time.Now(),
			Error:        fmt.Errorf("concurrent sub-agent limit reached (%d). Wait for existing sub-agents to finish before spawning new ones.", m.maxConcurrent),
			done:         make(chan struct{}),
		}
		close(sa.done)
		m.agents[errID] = sa
		m.mu.Unlock()
		return errID
	}
	m.nextID++
	id := fmt.Sprintf("sa-%d", m.nextID)
	m.totalSpawned++

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

	m.agents[id] = sa
	m.mu.Unlock()

	return id
}

func (m *Manager) SetWorktree(id, path string) {
	m.mu.Lock()
	sa, ok := m.agents[id]
	m.mu.Unlock()
	if !ok {
		return
	}
	sa.mu.Lock()
	sa.Worktree = path
	sa.mu.Unlock()
}

func (m *Manager) Get(id string) (*SubAgent, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sa, ok := m.agents[id]
	return sa, ok
}

func (m *Manager) SnapshotByID(id string) (Snapshot, bool) {
	sa, ok := m.Get(id)
	if !ok || sa == nil {
		return Snapshot{}, false
	}
	return sa.snapshot(), true
}

func (m *Manager) GetTaskText(id string) (string, bool) {
	sa, ok := m.Get(id)
	if !ok {
		return "", false
	}
	snap := sa.snapshot()
	if snap.Status != StatusCompleted || snap.Result == "" || snap.Task == "" {
		return "", false
	}
	return snap.Task, true
}

func (m *Manager) GetTaskOutput(id string) (string, bool) {
	sa, ok := m.Get(id)
	if !ok {
		return "", false
	}
	snap := sa.snapshot()
	if snap.Result != "" {
		return snap.Result, true
	}
	if snap.Error != "" {

		return fmt.Sprintf("[failed] %s", snap.Error), true
	}
	if snap.ProgressSummary != "" {
		return "[in progress] " + snap.ProgressSummary, true
	}
	if snap.Status == StatusPending {
		return "[pending, not started yet]", true
	}
	if snap.Status == StatusRunning {
		return "[running, no output yet]", true
	}
	return "", true
}

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

func (m *Manager) Statuses() []StatusInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]StatusInfo, 0, len(m.agents))
	for _, sa := range m.agents {
		out = append(out, sa.statusInfo())
	}
	return out
}

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
	sa.toolExecuting = false
	debug.Log("cancel", "Cancel: set Status=Cancelled id=%s goroutineStarted=%v", id, sa.goroutineStarted)

	if !sa.goroutineStarted {
		sa.closeDone()
	}
	sa.mu.Unlock()
	m.notifyUpdate(sa)
	return true
}

func (sa *SubAgent) SetModel(model string) {
	sa.mu.Lock()
	sa.Model = model
	sa.mu.Unlock()
}

func IsSyntheticID(id string) bool {
	return strings.HasPrefix(id, "sa-shutdown-") ||
		strings.HasPrefix(id, "sa-budget-") ||
		strings.HasPrefix(id, "sa-limit-")
}

func (sa *SubAgent) CurrentStatus() Status {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	return sa.Status
}

func (sa *SubAgent) closeDone() {
	if sa.done != nil {
		select {
		case <-sa.done:
		default:
			close(sa.done)
		}
	}
}

func (sa *SubAgent) doneChan() <-chan struct{} {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	return sa.done
}

func (sa *SubAgent) isGoroutineStarted() bool {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	return sa.goroutineStarted
}

func (m *Manager) reclaimGrace() time.Duration {
	if m.cancelAllTimeout > 0 {
		return m.cancelAllTimeout
	}
	return cancelAllTimeout
}

func (m *Manager) ensureSlotReclaimed(id string, grace time.Duration) {
	sa, ok := m.Get(id)
	if !ok || sa == nil {
		return
	}
	sa.mu.Lock()
	done := sa.done
	started := sa.goroutineStarted
	sa.mu.Unlock()
	if !started || done == nil {
		return
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		if m.forceReclaimSlot(id) {
			debug.Log("subagent", "watchdog: force-reclaimed semaphore slot of stuck sub-agent %s", id)
		}
	}
}

func (m *Manager) forceReclaimSlot(id string) bool {
	m.mu.Lock()
	held := m.semOwners[id]
	if held {
		delete(m.semOwners, id)
	}
	m.mu.Unlock()
	if !held {
		return false
	}
	select {
	case <-m.sem:
	default:

	}
	return true
}

func (m *Manager) CancelAll() int {
	timeout := m.cancelAllTimeout
	if timeout == 0 {
		timeout = cancelAllTimeout
	}
	debug.Log("cancel", "CancelAll: START timeout=%v", timeout)

	m.mu.Lock()
	ids := make([]string, 0)

	type doneEntry struct {
		id string
		ch <-chan struct{}
	}
	var doneChs []doneEntry
	for id, sa := range m.agents {
		status := sa.getStatus()
		if status == StatusPending || status == StatusRunning {
			ids = append(ids, id)

			if sa.isGoroutineStarted() && sa.done != nil {
				doneChs = append(doneChs, doneEntry{id: id, ch: sa.done})
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

	timedOut := 0
	for _, ent := range doneChs {
		timer := time.NewTimer(timeout)
		select {
		case <-ent.ch:
			timer.Stop()
			debug.Log("cancel", "CancelAll: goroutine %s terminated normally", ent.id)
		case <-timer.C:
			timer.Stop()

			timedOut++
			if m.forceReclaimSlot(ent.id) {
				debug.Log("cancel", "CancelAll: TIMEOUT waiting for %s, force-reclaimed its semaphore slot", ent.id)
			}

		}
	}
	if timedOut > 0 {
		debug.Log("cancel", "CancelAll: DONE with %d stuck goroutine(s), cancelled=%d", timedOut, cancelled)
		return cancelled
	}
	debug.Log("cancel", "CancelAll: DONE all goroutines terminated, cancelled=%d", cancelled)
	return cancelled
}

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

func (m *Manager) Complete(id string, result string, err error) {
	m.mu.Lock()
	sa, ok := m.agents[id]
	onComplete := m.onComplete
	m.mu.Unlock()
	if !ok {
		return
	}
	sa.mu.Lock()

	switch sa.Status {
	case StatusCancelled, StatusCompleted, StatusFailed:

		backfilled := false
		if result != "" && sa.Result == "" {
			sa.Result = result
			backfilled = true
		}
		sa.closeDone()
		sa.mu.Unlock()

		if backfilled {
			if onComplete != nil {
				onComplete(sa)
			}
			m.notifyUpdate(sa)
		}
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
	sa.toolExecuting = false
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

func (m *Manager) FlushStreamBatch() {
	m.flushStreamBatch()
}

func (m *Manager) SetOnComplete(fn func(*SubAgent)) {
	m.mu.Lock()
	m.onComplete = fn
	m.mu.Unlock()
}

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

			}
		}
	}
	return sent
}

func (m *Manager) SetOnUpdate(fn func(*SubAgent)) {
	m.mu.Lock()
	m.onUpdate = fn
	m.mu.Unlock()
}

func (m *Manager) AcquireSemaphore(ctx context.Context) error {
	select {
	case m.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) acquireSlot(ctx context.Context, id string) error {
	if err := m.AcquireSemaphore(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	m.semOwners[id] = true
	m.mu.Unlock()
	return nil
}

func (m *Manager) ReleaseSemaphore() {
	<-m.sem
}

func (m *Manager) releaseSlot(id string) {
	m.mu.Lock()
	held, ok := m.semOwners[id]
	if ok {
		delete(m.semOwners, id)
	}
	m.mu.Unlock()
	if !ok || !held {
		return
	}
	<-m.sem
}

func (m *Manager) Timeout() time.Duration {
	return m.timeout
}

func (m *Manager) ShowOutput() bool {
	return m.showOutput
}

func (m *Manager) notifyUpdate(sa *SubAgent) {
	m.mu.Lock()
	fn := m.onUpdate
	if fn == nil {
		m.mu.Unlock()
		return
	}
	now := time.Now()
	if m.lastNotify == nil {
		m.lastNotify = make(map[string]time.Time)
	}
	last := m.lastNotify[sa.ID]

	sa.mu.Lock()
	terminal := sa.Status == StatusCompleted || sa.Status == StatusFailed || sa.Status == StatusCancelled
	sa.mu.Unlock()
	if !terminal && !last.IsZero() && now.Sub(last) < 100*time.Millisecond {
		m.mu.Unlock()
		return
	}
	m.lastNotify[sa.ID] = now
	m.mu.Unlock()
	fn(sa)
}

const maxTerminalAgents = 20

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
		doneCh := sa.done
		sa.mu.Unlock()
		if !isTerminal {
			continue
		}

		if doneCh != nil {
			select {
			case <-doneCh:

			default:
				continue
			}
		}
		terminals = append(terminals, terminalEntry{id: id, endedAt: ended})
	}
	if len(terminals) <= maxTerminalAgents {
		m.mu.Unlock()
		return
	}

	sort.Slice(terminals, func(i, j int) bool {
		return terminals[i].endedAt.Before(terminals[j].endedAt)
	})

	toDelete := len(terminals) - maxTerminalAgents
	for i := 0; i < toDelete; i++ {
		delete(m.agents, terminals[i].id)
	}
	debug.Log("subagent", "purgeTerminalAgents: removed %d old terminal agents, %d remaining", toDelete, len(terminals)-toDelete)
	m.mu.Unlock()
}
