package swarm

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/task"
)

// AgentFactory creates an agent with the given provider, tool set, system prompt, and max turns.
type AgentFactory func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner

// AgentRunner is the minimal interface a teammate agent must satisfy.
type AgentRunner interface {
	RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent)) error
}

type usageHandlerSetter interface {
	SetUsageHandler(func(provider.TokenUsage))
}

// ToolBuilder constructs a tool set for a teammate based on allowed tool names.
type ToolBuilder func(allowedTools []string) interface{}

// Manager manages swarm teams: creation, teammate spawning, lifecycle.
// streamBatchInterval controls how often accumulated teammate text/reasoning
// events are flushed to the TUI. Without batching, each LLM streaming token
// (~50-100/s per teammate) triggers a separate callback → program.Send →
// Bubble Tea Update(), compounding with sub-agent flooding.
const swarmStreamBatchInterval = 80 * time.Millisecond

type Manager struct {
	teams    map[string]*Team
	provider provider.Provider
	cfg      config.SwarmConfig

	agentFactory AgentFactory
	toolBuilder  ToolBuilder
	onUpdate     func(Event)
	onUsage      func(provider.TokenUsage)

	// results stores the most recent task output per teammate (key=teammateID).
	// Written on teammate_idle events, cleared on teammate shutdown.
	results map[string]string

	// workingDir is the project directory injected into teammate system prompts
	// so teammates know where they are without having to discover it via pwd/ls.
	workingDir string

	// systemPromptBuilder, if set, builds a rich system prompt for teammates
	// with full project context (tools, memory, git status, etc.).
	// When nil, falls back to the minimal buildTeammateSystemPrompt().
	systemPromptBuilder func(name, teamName, workingDir string) string

	rootCtx    context.Context
	rootCancel context.CancelFunc
	mu         sync.Mutex
	nextTeamID int

	// streamBatch accumulates teammate_text/teammate_reasoning events per
	// teammate and flushes them at swarmStreamBatchInterval to prevent
	// TUI message flooding. Guarded by streamBatchMu.
	streamBatchMu   sync.Mutex
	streamTextBuf   map[string]*strings.Builder // teammateID → accumulated text
	streamRsnBuf    map[string]*strings.Builder // teammateID → accumulated reasoning
	streamBatchDone chan struct{}               // closed to stop the ticker goroutine
}

// NewManager creates a swarm Manager.
func NewManager(cfg config.SwarmConfig, prov provider.Provider, factory AgentFactory, builder ToolBuilder) *Manager {
	if cfg.MaxTeammatesPerTeam <= 0 {
		cfg.MaxTeammatesPerTeam = 8
	}
	// TeammateTimeout defaults to 0 (no timeout). Set in config to enforce a deadline.
	if cfg.InboxSize <= 0 {
		cfg.InboxSize = 32
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 1 * time.Second
	}
	rootCtx, rootCancel := context.WithCancel(context.Background())
	return &Manager{
		teams:           make(map[string]*Team),
		provider:        prov,
		cfg:             cfg,
		agentFactory:    factory,
		toolBuilder:     builder,
		results:         make(map[string]string),
		rootCtx:         rootCtx,
		rootCancel:      rootCancel,
		streamTextBuf:   make(map[string]*strings.Builder),
		streamRsnBuf:    make(map[string]*strings.Builder),
		streamBatchDone: make(chan struct{}),
	}
}

func (m *Manager) SetUsageHandler(fn func(provider.TokenUsage)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onUsage = fn
}

// RootContext returns the manager's lifecycle context.
func (m *Manager) RootContext() context.Context {
	if m.rootCtx == nil {
		return context.Background()
	}
	return m.rootCtx
}

// Shutdown cancels all running teammates and stops the manager.
func (m *Manager) Shutdown() {
	// Stop the stream batch ticker and flush remaining text.
	close(m.streamBatchDone)
	m.flushStreamBatch()

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, team := range m.teams {
		team.mu.Lock()
		for _, tm := range team.Teammates {
			if tm.cancel != nil {
				tm.cancel()
			}
			tm.setStatus(TeammateShuttingDown)
		}
		team.mu.Unlock()
	}
	if m.rootCancel != nil {
		m.rootCancel()
	}
}

// CreateTeam creates a new team with the given name and returns its snapshot.
func (m *Manager) CreateTeam(name, leaderID string) TeamSnapshot {
	m.mu.Lock()
	m.nextTeamID++
	id := fmt.Sprintf("team-%d", m.nextTeamID)
	m.mu.Unlock()

	team := &Team{
		ID:        id,
		Name:      name,
		LeaderID:  leaderID,
		Teammates: make(map[string]*Teammate),
		Tasks:     nil, // lazily created when first task is added
		CreatedAt: time.Now(),
	}

	m.mu.Lock()
	m.teams[id] = team
	m.mu.Unlock()

	m.emit(Event{Type: "team_created", TeamID: id, Timestamp: time.Now()})
	return team.snapshot()
}

// DeleteTeam shuts down all teammates and removes the team.
func (m *Manager) DeleteTeam(teamID string) error {
	m.mu.Lock()
	team, ok := m.teams[teamID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("team %q not found", teamID)
	}

	// Shutdown all teammates before removing from map so goroutines
	// that hold a reference to the team can drain gracefully.
	team.mu.Lock()
	for _, tm := range team.Teammates {
		if tm.cancel != nil {
			tm.cancel()
		}
		tm.setStatus(TeammateShuttingDown)
	}
	team.mu.Unlock()

	delete(m.teams, teamID)
	m.mu.Unlock()

	m.emit(Event{Type: "team_deleted", TeamID: teamID, Timestamp: time.Now()})
	return nil
}

// GetTeam returns a snapshot of the team if it exists.
func (m *Manager) GetTeam(id string) (TeamSnapshot, bool) {
	m.mu.Lock()
	team, ok := m.teams[id]
	m.mu.Unlock()
	if !ok {
		return TeamSnapshot{}, false
	}
	return team.snapshot(), true
}

// TeamStatusInfo is a lightweight snapshot of a team with only teammate status
// info (no events). Use this instead of ListTeams() when only the strip display
// needs updating.
type TeamStatusInfo struct {
	ID        string
	Name      string
	LeaderID  string
	Teammates []TeammateStatusInfo
	TaskCount int
}

// ListTeamStatuses returns lightweight status info for all teams.
// Unlike ListTeams(), this does NOT copy teammate events and is safe to call
// at high frequency (e.g. on every streaming token event) without contending
// the Teammate.mu lock that the runner uses for appendEvent.
func (m *Manager) ListTeamStatuses() []TeamStatusInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TeamStatusInfo, 0, len(m.teams))
	for _, t := range m.teams {
		taskCount := 0
		if t.Tasks != nil {
			taskCount = len(t.Tasks.List())
		}
		out = append(out, TeamStatusInfo{
			ID:        t.ID,
			Name:      t.Name,
			LeaderID:  t.LeaderID,
			Teammates: t.teammateStatuses(),
			TaskCount: taskCount,
		})
	}
	return out
}

// ListTeams returns full snapshots of all teams (including events).
// Prefer ListTeamStatuses() for strip/status display to avoid copying events.
func (m *Manager) ListTeams() []TeamSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TeamSnapshot, 0, len(m.teams))
	for _, t := range m.teams {
		out = append(out, t.snapshot())
	}
	return out
}

// TeamBoardSnapshot is a lightweight read-only snapshot for desktop/team board UIs.
type TeamBoardSnapshot struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	LeaderID  string              `json:"leaderID"`
	Teammates []TeamBoardTeammate `json:"teammates"`
	Tasks     []TeamBoardTask     `json:"tasks"`
	CreatedAt time.Time           `json:"createdAt"`
}

// TeamBoardTeammate summarizes a teammate for board display without copying event logs.
type TeamBoardTeammate struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color,omitempty"`
	Status      string `json:"status"`
	CurrentTask string `json:"currentTask,omitempty"`
	LastResult  string `json:"lastResult,omitempty"`
}

// TeamBoardTask summarizes a shared team-board task for desktop display.
type TeamBoardTask struct {
	ID          string            `json:"id"`
	Subject     string            `json:"subject"`
	Description string            `json:"description,omitempty"`
	ActiveForm  string            `json:"activeForm,omitempty"`
	Status      string            `json:"status"`
	Owner       string            `json:"owner,omitempty"`
	Assignee    string            `json:"assignee,omitempty"`
	Blocks      []string          `json:"blocks,omitempty"`
	BlockedBy   []string          `json:"blockedBy,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

// ListTeamBoards returns snapshots of all teams with teammate status and task board state.
func (m *Manager) ListTeamBoards() []TeamBoardSnapshot {
	m.mu.Lock()
	teams := make([]*Team, 0, len(m.teams))
	for _, t := range m.teams {
		teams = append(teams, t)
	}
	m.mu.Unlock()

	out := make([]TeamBoardSnapshot, 0, len(teams))
	for _, t := range teams {
		out = append(out, t.boardSnapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// EmitBoardUpdated notifies UI subscribers that a team's shared task board changed.
func (m *Manager) EmitBoardUpdated(teamID string) {
	m.emit(Event{Type: "team_board_updated", TeamID: teamID, Timestamp: time.Now()})
}

// SpawnTeammate creates a new teammate in the given team and starts its idle loop.
func (m *Manager) SpawnTeammate(teamID, name, color string, allowedTools []string) (TeammateSnapshot, error) {
	m.mu.Lock()
	team, ok := m.teams[teamID]
	if !ok {
		m.mu.Unlock()
		return TeammateSnapshot{}, fmt.Errorf("team %q not found", teamID)
	}
	// Generate teammate ID while holding m.mu to avoid TOCTOU with concurrent spawns.
	m.nextTeamID++
	tmID := fmt.Sprintf("tm-%d", m.nextTeamID)
	m.mu.Unlock()

	// Hold team.mu for both the max check and the insert to close the TOCTOU window.
	team.mu.Lock()
	if len(team.Teammates) >= m.cfg.MaxTeammatesPerTeam {
		team.mu.Unlock()
		return TeammateSnapshot{}, fmt.Errorf("team %q already has max %d teammates", teamID, m.cfg.MaxTeammatesPerTeam)
	}

	ctx, cancel := context.WithCancel(m.rootCtx)

	tm := &Teammate{
		ID:        tmID,
		Name:      name,
		Color:     color,
		Status:    TeammateIdle,
		Inbox:     make(chan MailMessage, m.cfg.InboxSize),
		CreatedAt: time.Now(),
		ctx:       ctx,
		cancel:    cancel,
	}

	team.Teammates[tmID] = tm
	team.mu.Unlock()

	// Build tool set for this teammate
	var toolSet interface{}
	if m.toolBuilder != nil {
		toolSet = m.toolBuilder(allowedTools)
	}

	// Build system prompt: use rich builder if available, otherwise fall back to simple prompt
	var systemPrompt string
	if m.systemPromptBuilder != nil {
		systemPrompt = m.systemPromptBuilder(name, team.Name, m.workingDir)
	} else {
		systemPrompt = buildTeammateSystemPrompt(name, team.Name, m.workingDir)
	}
	var agent AgentRunner
	if m.agentFactory != nil {
		agent = m.agentFactory(m.provider, toolSet, systemPrompt, 0)
		if m.onUsage != nil {
			if usageAware, ok := agent.(usageHandlerSetter); ok {
				usageAware.SetUsageHandler(m.onUsage)
			}
		}
	}

	// Start idle loop in a goroutine
	safego.Go("swarm.teammateLoop", func() {
		runTeammateLoop(ctx, tm, team, agent, m, m.emit, m.cfg.TeammateTimeout)
	})

	m.emit(Event{
		Type:         "teammate_spawned",
		TeamID:       teamID,
		TeammateID:   tmID,
		TeammateName: name,
		Timestamp:    time.Now(),
	})

	return tm.snapshot(), nil
}

// ShutdownTeammate stops a specific teammate.
func (m *Manager) ShutdownTeammate(teamID, tmID string) error {
	m.mu.Lock()
	team, ok := m.teams[teamID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("team %q not found", teamID)
	}
	m.mu.Unlock()

	tm, ok := team.getTeammate(tmID)
	if !ok {
		return fmt.Errorf("teammate %q not found in team %q", tmID, teamID)
	}

	tm.mu.Lock()
	if tm.cancel != nil {
		tm.cancel()
	}
	tm.Status = TeammateShuttingDown
	tm.EndedAt = time.Now()
	tm.mu.Unlock()

	m.emit(Event{
		Type:       "teammate_shutdown",
		TeamID:     teamID,
		TeammateID: tmID,
		Timestamp:  time.Now(),
	})
	return nil
}

// CancelAll cancels all live teammates across all teams.
// Used when the main agent is interrupted (ctrl+c/esc) to avoid orphaned work
// or queued work starting after the UI has already marked the team as cancelled.
func (m *Manager) CancelAll() {
	m.mu.Lock()
	teams := make([]*Team, 0, len(m.teams))
	for _, t := range m.teams {
		teams = append(teams, t)
	}
	m.mu.Unlock()

	for _, team := range teams {
		team.mu.Lock()
		for _, tm := range team.Teammates {
			tm.mu.Lock()
			if tm.Status != TeammateShuttingDown {
				if tm.cancel != nil {
					tm.cancel()
				}
				tm.Status = TeammateShuttingDown
				tm.EndedAt = time.Now()
			}
			tm.mu.Unlock()
		}
		team.mu.Unlock()
	}
}

// SendToTeammate sends a message to a specific teammate's inbox.
func (m *Manager) SendToTeammate(teamID, tmID string, msg MailMessage) error {
	m.mu.Lock()
	team, ok := m.teams[teamID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("team %q not found", teamID)
	}
	m.mu.Unlock()

	tm, ok := team.getTeammate(tmID)
	if !ok {
		return fmt.Errorf("teammate %q not found in team %q", tmID, teamID)
	}

	select {
	case tm.Inbox <- msg:
		return nil
	default:
		return fmt.Errorf("teammate %q inbox is full", tmID)
	}
}

// BroadcastToTeam sends a message to all idle teammates in the team.
func (m *Manager) BroadcastToTeam(teamID string, msg MailMessage) []string {
	m.mu.Lock()
	team, ok := m.teams[teamID]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	var sent []string
	var dropped []string
	for _, tm := range team.listTeammates() {
		if tm.getStatus() == TeammateIdle || tm.getStatus() == TeammateWorking {
			select {
			case tm.Inbox <- msg:
				sent = append(sent, tm.ID)
			default:
				dropped = append(dropped, tm.ID)
			}
		}
	}
	if len(dropped) > 0 {
		debug.Log("swarm", "broadcast dropped %d messages for teammates %v (inbox full)", len(dropped), dropped)
	}
	return sent
}

// EnsureTaskManager returns the team's task manager, creating it if needed.
func (m *Manager) EnsureTaskManager(teamID string) (*task.Manager, error) {
	m.mu.Lock()
	team, ok := m.teams[teamID]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("team %q not found", teamID)
	}
	m.mu.Unlock()

	team.mu.Lock()
	defer team.mu.Unlock()
	if team.Tasks == nil {
		team.Tasks = task.NewManager()
	}
	return team.Tasks, nil
}

// GetTaskManager returns the team's task manager without creating one.
// Returns nil if the team has no task board yet.
func (m *Manager) GetTaskManager(teamID string) *task.Manager {
	m.mu.Lock()
	team, ok := m.teams[teamID]
	m.mu.Unlock()
	if !ok {
		return nil
	}
	team.mu.RLock()
	defer team.mu.RUnlock()
	return team.Tasks
}

// SetOnUpdate sets the callback for swarm state changes (used by TUI).
func (m *Manager) SetOnUpdate(fn func(Event)) {
	m.mu.Lock()
	m.onUpdate = fn
	m.mu.Unlock()
}

// SetWorkingDir sets the working directory injected into teammate system prompts.
func (m *Manager) SetWorkingDir(dir string) {
	m.workingDir = dir
}

// SetSystemPromptBuilder sets a function that builds a rich system prompt for
// teammates with full project context. When not set, a minimal prompt is used.
func (m *Manager) SetSystemPromptBuilder(fn func(name, teamName, workingDir string) string) {
	m.systemPromptBuilder = fn
}

// GetTeammateResult returns the most recent task output for a teammate.
// Returns (result, true) if a result exists, ("", false) otherwise.
func (m *Manager) GetTeammateResult(teamID, tmID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Verify the teammate still exists in its team.
	team, ok := m.teams[teamID]
	if !ok {
		return "", false
	}
	tm, ok := team.getTeammate(tmID)
	if !ok {
		return "", false
	}

	// Prefer the live result from the teammate (most up-to-date).
	if r := tm.getResults(); r != "" {
		return r, true
	}

	// Fall back to the stored result (survives if teammate was removed).
	r, ok := m.results[tmID]
	return r, ok && r != ""
}

// GetTeamResults returns a map of teammateID → last result for all teammates
// in the team that have a result.
func (m *Manager) GetTeamResults(teamID string) map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()

	team, ok := m.teams[teamID]
	if !ok {
		return nil
	}

	out := make(map[string]string)
	for _, tm := range team.listTeammates() {
		if r := tm.getResults(); r != "" {
			out[tm.ID] = r
		}
	}
	return out
}

func (m *Manager) emit(ev Event) {
	// Persist teammate results in the results store.
	// Hold m.mu to protect concurrent access to m.results.
	switch ev.Type {
	case "teammate_idle":
		if ev.Result != "" {
			debug.Log("swarm", "emit: storing result for %s len=%d", ev.TeammateID, len(ev.Result))
			m.mu.Lock()
			m.results[ev.TeammateID] = ev.Result
			m.mu.Unlock()
		} else {
			debug.Log("swarm", "emit: teammate_idle for %s but Result is empty", ev.TeammateID)
		}
	case "teammate_shutdown":
		// Keep the last result available even after shutdown so the
		// leader can still retrieve it via GetTeammateResult.
	}

	// Batch high-frequency text/reasoning events to prevent TUI flooding.
	// These arrive at ~50-100/s per teammate from the LLM streaming callback.
	switch ev.Type {
	case "teammate_text":
		m.streamBatchMu.Lock()
		buf, ok := m.streamTextBuf[ev.TeammateID]
		if !ok {
			buf = &strings.Builder{}
			m.streamTextBuf[ev.TeammateID] = buf
		}
		buf.WriteString(ev.Result)
		m.streamBatchMu.Unlock()
		return
	case "teammate_reasoning":
		m.streamBatchMu.Lock()
		buf, ok := m.streamRsnBuf[ev.TeammateID]
		if !ok {
			buf = &strings.Builder{}
			m.streamRsnBuf[ev.TeammateID] = buf
		}
		buf.WriteString(ev.Result)
		m.streamBatchMu.Unlock()
		return
	}

	// Non-batched events go directly to the callback.
	m.mu.Lock()
	fn := m.onUpdate
	m.mu.Unlock()
	if fn != nil {
		fn(ev)
	}
}

// StartStreamBatcher starts the background goroutine that periodically
// flushes accumulated teammate text/reasoning events. Must be called
// after SetOnUpdate.
func (m *Manager) StartStreamBatcher() {
	go func() {
		ticker := time.NewTicker(swarmStreamBatchInterval)
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
	}()
}

// flushStreamBatch delivers accumulated text/reasoning as single batched
// events per teammate, then clears the buffers.
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
	fn := m.onUpdate
	m.mu.Unlock()
	if fn == nil {
		return
	}

	for id, buf := range textBufs {
		if buf.Len() > 0 {
			fn(Event{
				Type:       "teammate_text",
				TeammateID: id,
				Result:     buf.String(),
				Timestamp:  time.Now(),
			})
		}
	}
	for id, buf := range rsnBufs {
		if buf.Len() > 0 {
			fn(Event{
				Type:       "teammate_reasoning",
				TeammateID: id,
				Result:     buf.String(),
				Timestamp:  time.Now(),
			})
		}
	}
}

// FlushStreamBatch exports flushStreamBatch for use in tests.
func (m *Manager) FlushStreamBatch() {
	m.flushStreamBatch()
}

// NotifyIdleRunners sends a task-available hint to all idle teammates,
// triggering them to poll the task board immediately instead of waiting
// for the next poller tick.
func (m *Manager) NotifyIdleRunners(teamID string) {
	m.mu.Lock()
	team, ok := m.teams[teamID]
	m.mu.Unlock()
	if !ok {
		return
	}
	hint := MailMessage{Type: "task_available"}
	for _, tm := range team.listTeammates() {
		if tm.getStatus() == TeammateIdle {
			select {
			case tm.Inbox <- hint:
			default:
			}
		}
	}
}

func buildTeammateSystemPrompt(name, teamName, workingDir string) string {
	prompt := fmt.Sprintf(
		"You are a teammate named %q in team %q. "+
			"Work like a professional collaborative team member. "+
			"Use the shared task board as the source of truth for tracked work. "+
			"If a task is assigned to you directly via inbox, start it immediately and do not re-claim it from the board first. "+
			"If you choose unassigned work from the board, claim it before starting. "+
			"Before creating a new follow-up task, check whether related work is already tracked so you do not duplicate effort. "+
			"Share intermediate findings when they materially unblock another teammate, but avoid repetitive back-and-forth or message loops. "+
			"If you need help or discover specialized follow-up work, send one targeted request or create one clear handoff task with enough context. "+
			"Only claim tasks that match your role and capabilities. "+
			"If a task does not match your role, hand it off cleanly instead of doing partial low-quality work. "+
			"Do not spawn further sub-agents or teammates. "+
			"Do not use emoji with Variation Selector-16 (U+FE0F, e.g. warning_sign+VS16) — use plain text instead to avoid terminal rendering issues. "+
			"Report results concisely and mark tracked tasks complete when done.",
		name, teamName,
	)
	if workingDir != "" {
		prompt += fmt.Sprintf("\n\nWorking directory: %s", workingDir)
	}
	return prompt
}

// TeammateSnapshot returns a snapshot of a teammate by ID across all teams.
func (m *Manager) TeammateSnapshot(tmID string) (TeammateSnapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, team := range m.teams {
		team.mu.RLock()
		tm, ok := team.Teammates[tmID]
		team.mu.RUnlock()
		if ok {
			return tm.snapshot(), true
		}
	}
	return TeammateSnapshot{}, false
}

// TeammateEventsSince returns incremental events for a teammate starting from
// fromIdx, along with the total event count. Unlike TeammateSnapshot, this
// avoids copying the full event history and is safe to call at high frequency.
func (m *Manager) TeammateEventsSince(tmID string, fromIdx int) ([]TeammateEvent, int, bool) {
	m.mu.Lock()
	for _, team := range m.teams {
		team.mu.RLock()
		tm, ok := team.Teammates[tmID]
		team.mu.RUnlock()
		if ok {
			events, total := tm.EventsSince(fromIdx)
			m.mu.Unlock()
			return events, total, true
		}
	}
	m.mu.Unlock()
	return nil, 0, false
}
