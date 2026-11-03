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

type AgentFactory func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner

type AgentRunner interface {
	RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent), origin ...string) error
}

type usageHandlerSetter interface {
	SetUsageHandler(func(provider.TokenUsage))
}

type ToolBuilder func(allowedTools []string) interface{}

const swarmStreamBatchInterval = 500 * time.Millisecond

const swarmCancelTimeout = 5 * time.Second

type Manager struct {
	teams          map[string]*Team
	provider       provider.Provider
	providerGetter func() provider.Provider
	cfg            config.SwarmConfig

	agentFactory AgentFactory
	toolBuilder  ToolBuilder
	onUpdate     func(Event)
	onUsage      func(provider.TokenUsage)

	results map[string]string

	resultsOrder []string

	workingDir string

	systemPromptBuilder func(name, teamName, workingDir string) string

	rootCtx    context.Context
	rootCancel context.CancelFunc
	mu         sync.Mutex
	nextTeamID int

	streamBatchMu   sync.Mutex
	streamTextBuf   map[string]*strings.Builder
	streamRsnBuf    map[string]*strings.Builder
	streamBatchDone chan struct{}
	shutdownOnce    sync.Once
}

func NewManager(cfg config.SwarmConfig, prov provider.Provider, factory AgentFactory, builder ToolBuilder) *Manager {
	return newManager(cfg, prov, nil, factory, builder)
}

func NewManagerWithProviderGetter(cfg config.SwarmConfig, prov provider.Provider, getter func() provider.Provider, factory AgentFactory, builder ToolBuilder) *Manager {
	return newManager(cfg, prov, getter, factory, builder)
}

func newManager(cfg config.SwarmConfig, prov provider.Provider, getter func() provider.Provider, factory AgentFactory, builder ToolBuilder) *Manager {
	if cfg.MaxTeammatesPerTeam <= 0 {
		cfg.MaxTeammatesPerTeam = 16
	}

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
		providerGetter:  getter,
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

func (m *Manager) RootContext() context.Context {
	if m.rootCtx == nil {
		return context.Background()
	}
	return m.rootCtx
}

func (m *Manager) Shutdown() {
	m.shutdownOnce.Do(func() {
		close(m.streamBatchDone)
	})
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
		Tasks:     nil,
		CreatedAt: time.Now(),
	}

	m.mu.Lock()
	m.teams[id] = team
	m.mu.Unlock()

	m.emit(Event{Type: "team_created", TeamID: id, Timestamp: time.Now()})
	return team.snapshot()
}

func (m *Manager) DeleteTeam(teamID string) error {
	m.mu.Lock()
	team, ok := m.teams[teamID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("team %q not found", teamID)
	}

	var doneChs []<-chan struct{}
	team.mu.Lock()
	for _, tm := range team.Teammates {
		tm.mu.Lock()
		if tm.cancel != nil {
			tm.cancel()
		}

		tm.Status = TeammateShuttingDown
		if tm.done != nil {
			doneChs = append(doneChs, tm.done)
		}
		tm.mu.Unlock()

		delete(m.results, tm.ID)
	}
	team.mu.Unlock()
	m.mu.Unlock()

	if len(doneChs) > 0 {
		timedOut := false
		for _, ch := range doneChs {
			if timedOut {
				select {
				case <-ch:
				default:
				}
				continue
			}
			timer := time.NewTimer(swarmCancelTimeout)
			select {
			case <-ch:
			case <-timer.C:
				timedOut = true
			}
			timer.Stop()
		}
	}

	m.mu.Lock()
	if current, still := m.teams[teamID]; still && current == team {

		team.mu.Lock()
		for _, tm := range team.Teammates {
			tm.mu.Lock()
			if tm.cancel != nil {
				tm.cancel()
			}
			tm.Status = TeammateShuttingDown
			tm.mu.Unlock()
			delete(m.results, tm.ID)
		}
		team.mu.Unlock()
		delete(m.teams, teamID)
	}
	m.mu.Unlock()

	m.emit(Event{Type: "team_deleted", TeamID: teamID, Timestamp: time.Now()})
	return nil
}

func (m *Manager) GetTeam(id string) (TeamSnapshot, bool) {
	m.mu.Lock()
	team, ok := m.teams[id]
	m.mu.Unlock()
	if !ok {
		return TeamSnapshot{}, false
	}
	return team.snapshot(), true
}

type TeamStatusInfo struct {
	ID        string
	Name      string
	LeaderID  string
	Teammates []TeammateStatusInfo
	TaskCount int
}

func (m *Manager) ListTeamStatuses() []TeamStatusInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TeamStatusInfo, 0, len(m.teams))
	for _, t := range m.teams {
		taskCount := 0
		t.mu.RLock()
		if t.Tasks != nil {
			taskCount = len(t.Tasks.List())
		}
		t.mu.RUnlock()
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

func (m *Manager) ListTeams() []TeamSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TeamSnapshot, 0, len(m.teams))
	for _, t := range m.teams {
		out = append(out, t.snapshot())
	}
	return out
}

type TeamBoardSnapshot struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	LeaderID  string              `json:"leaderID"`
	Teammates []TeamBoardTeammate `json:"teammates"`
	Tasks     []TeamBoardTask     `json:"tasks"`
	CreatedAt time.Time           `json:"createdAt"`
}

type TeamBoardTeammate struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color,omitempty"`
	Status      string `json:"status"`
	CurrentTask string `json:"currentTask,omitempty"`
	LastResult  string `json:"lastResult,omitempty"`
}

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

func (m *Manager) EmitBoardUpdated(teamID string) {
	m.emit(Event{Type: "team_board_updated", TeamID: teamID, Timestamp: time.Now()})
	if tm := m.GetTaskManager(teamID); tm != nil {
		persistTeamBoardAsync(teamID, tm)
	}
}

func (m *Manager) currentProvider() provider.Provider {
	if m.providerGetter != nil {
		return m.providerGetter()
	}
	return m.provider
}

func (m *Manager) SpawnTeammate(teamID, name, color string, allowedTools []string) (TeammateSnapshot, error) {
	m.mu.Lock()
	team, ok := m.teams[teamID]
	if !ok {
		m.mu.Unlock()
		return TeammateSnapshot{}, fmt.Errorf("team %q not found", teamID)
	}

	m.nextTeamID++
	tmID := fmt.Sprintf("tm-%d", m.nextTeamID)
	m.mu.Unlock()

	m.mu.Lock()
	if _, ok := m.teams[teamID]; !ok {
		m.mu.Unlock()
		return TeammateSnapshot{}, fmt.Errorf("team %q not found", teamID)
	}
	team.mu.Lock()
	m.mu.Unlock()
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
		done:      make(chan struct{}),
	}

	team.Teammates[tmID] = tm
	team.mu.Unlock()

	var toolSet interface{}
	if m.toolBuilder != nil {
		toolSet = m.toolBuilder(allowedTools)
	}

	m.mu.Lock()
	builder := m.systemPromptBuilder
	wd := m.workingDir
	onUsage := m.onUsage
	m.mu.Unlock()
	var systemPrompt string
	if builder != nil {
		systemPrompt = builder(name, team.Name, wd)
	} else {
		systemPrompt = buildTeammateSystemPrompt(name, team.Name, wd)
	}
	var agent AgentRunner
	if m.agentFactory != nil {
		agent = m.agentFactory(m.currentProvider(), toolSet, systemPrompt, 0)
		if onUsage != nil {
			if usageAware, ok := agent.(usageHandlerSetter); ok {
				usageAware.SetUsageHandler(onUsage)
			}
		}

		if tmID != "" {
			if sid, ok := agent.(interface{ SetSessionID(string) }); ok {
				sid.SetSessionID(tmID)
			}
		}
	}

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

	team.removeTeammate(tmID)

	m.emit(Event{
		Type:       "teammate_shutdown",
		TeamID:     teamID,
		TeammateID: tmID,
		Timestamp:  time.Now(),
	})
	return nil
}

func (m *Manager) CancelAll() {
	m.mu.Lock()
	teams := make([]*Team, 0, len(m.teams))
	for _, t := range m.teams {
		teams = append(teams, t)
	}
	m.mu.Unlock()

	var doneChs []<-chan struct{}
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
			if tm.done != nil {
				doneChs = append(doneChs, tm.done)
			}
			tm.mu.Unlock()
		}
		team.mu.Unlock()
	}

	if len(doneChs) > 0 {
		timedOut := false
		for _, ch := range doneChs {
			if timedOut {
				select {
				case <-ch:
				default:
				}
				continue
			}
			timer := time.NewTimer(swarmCancelTimeout)
			select {
			case <-ch:
			case <-timer.C:
				timedOut = true
			}
			timer.Stop()
		}
	}
}

func (m *Manager) SendToTeammate(teamID, tmID string, msg MailMessage) error {

	m.mu.Lock()
	defer m.mu.Unlock()
	team, ok := m.teams[teamID]
	if !ok {
		return fmt.Errorf("team %q not found", teamID)
	}
	team.mu.RLock()
	defer team.mu.RUnlock()
	tm, ok := team.Teammates[tmID]
	if !ok {
		return fmt.Errorf("teammate %q not found in team %q", tmID, teamID)
	}

	tm.mu.Lock()
	shutdown := tm.Status == TeammateShuttingDown || (tm.ctx != nil && tm.ctx.Err() != nil)
	tm.mu.Unlock()
	if shutdown {
		return fmt.Errorf("teammate %q is shutting down, message not delivered", tmID)
	}
	select {
	case tm.Inbox <- msg:
		return nil
	default:
		return fmt.Errorf("teammate %q inbox is full", tmID)
	}
}

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

		tm.mu.Lock()
		if tm.Status == TeammateShuttingDown || (tm.ctx != nil && tm.ctx.Err() != nil) {
			tm.mu.Unlock()
			dropped = append(dropped, tm.ID)
			debug.Log("swarm", "broadcast skipped teammate %s (shutting down)", tm.ID)
			continue
		}
		if tm.Status == TeammateIdle || tm.Status == TeammateWorking {
			select {
			case tm.Inbox <- msg:
				sent = append(sent, tm.ID)
			default:
				dropped = append(dropped, tm.ID)
			}
		}
		tm.mu.Unlock()
	}
	if len(dropped) > 0 {

		debug.Log("swarm", "broadcast dropped %d messages for teammates %v (inbox full or shutting down)", len(dropped), dropped)
	}
	return sent
}

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

		team.Tasks = loadTeamBoard(teamID)
	}
	return team.Tasks, nil
}

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

func (m *Manager) SetOnUpdate(fn func(Event)) {
	m.mu.Lock()
	m.onUpdate = fn
	m.mu.Unlock()
}

func (m *Manager) SetWorkingDir(dir string) {
	m.mu.Lock()
	m.workingDir = dir
	m.mu.Unlock()
}

func (m *Manager) SetSystemPromptBuilder(fn func(name, teamName, workingDir string) string) {
	m.mu.Lock()
	m.systemPromptBuilder = fn
	m.mu.Unlock()
}

func (m *Manager) GetTeammateResult(teamID, tmID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if tm, ok := m.teams[teamID]; ok {
		if live, ok := tm.getTeammate(tmID); ok {
			if r := live.getResults(); r != "" {
				return r, true
			}
		}
	}
	r, ok := m.results[tmID]
	return r, ok && r != ""
}

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

func (m *Manager) teammateGoverned(tmID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, team := range m.teams {
		team.mu.RLock()
		_, ok := team.Teammates[tmID]
		team.mu.RUnlock()
		if ok {
			return true
		}
	}
	return false
}

const maxStoredResults = 256

func (m *Manager) storeResultLocked(tmID, result string) {
	if _, exists := m.results[tmID]; !exists {
		m.resultsOrder = append(m.resultsOrder, tmID)
	}
	m.results[tmID] = result
	for overflow := len(m.results) - maxStoredResults; overflow > 0 && len(m.resultsOrder) > 0; {
		oldest := m.resultsOrder[0]
		m.resultsOrder = m.resultsOrder[1:]
		if _, still := m.results[oldest]; still {
			delete(m.results, oldest)
			overflow--
		}
	}
}

func (m *Manager) emit(ev Event) {

	switch ev.Type {
	case "teammate_idle":
		if ev.Result != "" {

			if m.teammateGoverned(ev.TeammateID) {
				debug.Log("swarm", "emit: storing result for %s len=%d", ev.TeammateID, len(ev.Result))
				m.mu.Lock()
				m.storeResultLocked(ev.TeammateID, ev.Result)
				m.mu.Unlock()

				appendTeammateExperience(m.workingDirForExp(), ev.TeamID, ev.TeammateID, ev.TeammateName, ev.Result)
			} else {
				debug.Log("swarm", "emit: dropping late result for ungoverned %s", ev.TeammateID)
			}
		} else {
			debug.Log("swarm", "emit: teammate_idle for %s but Result is empty", ev.TeammateID)
		}
	case "teammate_shutdown":

	}

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

	m.mu.Lock()
	fn := m.onUpdate
	m.mu.Unlock()
	if fn != nil {
		fn(ev)
	}
}

func (m *Manager) StartStreamBatcher() {
	safego.Go("swarm.streamBatcher", func() {
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

func (m *Manager) FlushStreamBatch() {
	m.flushStreamBatch()
}

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
