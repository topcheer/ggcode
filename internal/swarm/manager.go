package swarm

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/task"
)

// AgentFactory creates an agent with the given provider, tool set, system prompt, and max turns.
type AgentFactory func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner

// AgentRunner is the minimal interface a teammate agent must satisfy.
type AgentRunner interface {
	RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent)) error
}

// ToolBuilder constructs a tool set for a teammate based on allowed tool names.
type ToolBuilder func(allowedTools []string) interface{}

// Manager manages swarm teams: creation, teammate spawning, lifecycle.
type Manager struct {
	teams    map[string]*Team
	provider provider.Provider
	cfg      config.SwarmConfig

	agentFactory AgentFactory
	toolBuilder  ToolBuilder
	onUpdate     func(Event)

	rootCtx    context.Context
	rootCancel context.CancelFunc
	mu         sync.Mutex
	nextTeamID int
}

// NewManager creates a swarm Manager.
func NewManager(cfg config.SwarmConfig, prov provider.Provider, factory AgentFactory, builder ToolBuilder) *Manager {
	if cfg.MaxTeammatesPerTeam <= 0 {
		cfg.MaxTeammatesPerTeam = 8
	}
	if cfg.TeammateTimeout <= 0 {
		cfg.TeammateTimeout = 30 * time.Minute
	}
	if cfg.InboxSize <= 0 {
		cfg.InboxSize = 32
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 1 * time.Second
	}
	rootCtx, rootCancel := context.WithCancel(context.Background())
	return &Manager{
		teams:        make(map[string]*Team),
		provider:     prov,
		cfg:          cfg,
		agentFactory: factory,
		toolBuilder:  builder,
		rootCtx:      rootCtx,
		rootCancel:   rootCancel,
	}
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

// ListTeams returns snapshots of all teams.
func (m *Manager) ListTeams() []TeamSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TeamSnapshot, 0, len(m.teams))
	for _, t := range m.teams {
		out = append(out, t.snapshot())
	}
	return out
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

	// Create independent agent for this teammate
	systemPrompt := buildTeammateSystemPrompt(name, team.Name)
	var agent AgentRunner
	if m.agentFactory != nil {
		agent = m.agentFactory(m.provider, toolSet, systemPrompt, 0)
	}

	// Start idle loop in a goroutine
	go runTeammateLoop(ctx, tm, team, agent, m, m.emit, m.cfg.TeammateTimeout)

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
	for _, tm := range team.listTeammates() {
		if tm.getStatus() == TeammateIdle || tm.getStatus() == TeammateWorking {
			select {
			case tm.Inbox <- msg:
				sent = append(sent, tm.ID)
			default:
				// inbox full, skip
			}
		}
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
	m.onUpdate = fn
}

func (m *Manager) emit(ev Event) {
	if m.onUpdate != nil {
		m.onUpdate(ev)
	}
}

func buildTeammateSystemPrompt(name, teamName string) string {
	return fmt.Sprintf(
		"You are a teammate named %q in team %q. "+
			"Complete tasks assigned to you via messages. "+
			"Use send_message to communicate results or ask questions. "+
			"Do not spawn further sub-agents or teammates.",
		name, teamName,
	)
}
