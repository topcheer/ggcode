package swarm

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"crypto/rand"
	"encoding/hex"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/task"
)

type AgentFactory func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner

type AgentRunner interface {
	RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent), origin ...string) error
}

type usageHandlerSetter interface {
	SetUsageHandler(func(provider.TokenUsage))
}

// permissionPolicySetter is satisfied by agent runners whose tool
// invocations are permission-gated (AgentRunner wrappers around the main
// Agent). #5464: teammates previously ran tools unguarded while the
// primary agent was permission-gated.
type permissionPolicySetter interface {
	SetPermissionPolicy(permission.PermissionPolicy)
}

type ToolBuilder func(allowedTools []string) interface{}

const swarmStreamBatchInterval = 500 * time.Millisecond

const swarmCancelTimeout = 5 * time.Second

type Manager struct {
	// BoardRoot overrides the on-disk root for team board persistence
	// (#5540). Empty (production default) uses util.ConfigDir(); tests set
	// it to a t.TempDir() so deterministic team IDs cannot collide on the
	// shared path across tests and packages.
	BoardRoot string
	// SessionRoot overrides the directory for teammate session journals
	// (persistent teams P1). Empty (production default) uses the user's
	// sessions dir; tests point it at a t.TempDir().
	SessionRoot string

	// JournalsEnabled gates teammate journal persistence (P1). Production
	// wiring (CLI/TUI + headless runtime) sets it to true; unit tests that
	// do not exercise persistence leave it false so they never touch the
	// user's real sessions directory.
	JournalsEnabled bool

	teams          map[string]*Team
	provider       provider.Provider
	providerGetter func() provider.Provider
	cfg            config.SwarmConfig

	agentFactory AgentFactory
	toolBuilder  ToolBuilder
	onUpdate     func(Event)
	onUsage      func(provider.TokenUsage)

	// policyGetter supplies the active PermissionPolicy for newly spawned
	// teammates (#5464). Nil = no wiring (teammates keep the factory
	// default); wired call sites (CLI + desktop) always provide it.
	policyGetter func() permission.PermissionPolicy

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

	// Persistent teams (#5829). #5860 rewrite: the CONFIRMED session id is
	// the single namespace - teams live under swarm/teams/<sessionID>/ and
	// every read/write goes through it. No lease taxonomy, no ownership
	// stamp field, no auto-mint: the directory IS the ownership. sessionID
	// is set once (constructor for resumed sessions, ConfirmSession once
	// for fresh sessions); teams cannot be created before it is confirmed.
	sessionID         atomic.Value // string; set once, read lock-free
	journalMu         sync.Mutex
	journalStoreCache *session.JSONLStore
	manifestMu        sync.Mutex
}

func NewManager(cfg config.SwarmConfig, sessionID string, prov provider.Provider, factory AgentFactory, builder ToolBuilder) *Manager {
	return newManager(cfg, sessionID, prov, nil, factory, builder)
}

func NewManagerWithProviderGetter(cfg config.SwarmConfig, sessionID string, prov provider.Provider, getter func() provider.Provider, factory AgentFactory, builder ToolBuilder) *Manager {
	return newManager(cfg, sessionID, prov, getter, factory, builder)
}

// ConfirmSession sets the confirmed session id exactly once (fresh-session
// path: the id is minted when the first message creates the session). A
// second, DIFFERENT confirmation is a programming error and panics loudly.
func (m *Manager) ConfirmSession(sessionID string) {
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, _ := m.sessionID.Load().(string); cur == sessionID {
		return
	}
	if cur, _ := m.sessionID.Load().(string); cur != "" {
		panic("swarm: session re-confirmed with a different id: " + cur + " -> " + sessionID)
	}
	m.sessionID.Store(sessionID)
}

// RebindSession moves the confirmed session id when the TUI legitimately
// switches sessions (Ctrl+L new session, /session switch). Unlike
// ConfirmSession it does NOT panic on a different id - the caller asserts
// the switch is intentional. Live teammates are expected to be torn down by
// the caller (handleClearChat cancels the swarm before switching); both
// sessions' on-disk namespaces are untouched, this only changes which
// namespace this process addresses next (#5872).
func (m *Manager) RebindSession(sessionID string) {
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, _ := m.sessionID.Load().(string); cur != sessionID {
		debug.Log("swarm", "session rebind: %s -> %s (#5872 session switch)", cur, sessionID)
		m.sessionID.Store(sessionID)
	}
}

// SessionNamespace reads the confirmed session id lock-free: it is set
// exactly once (constructor or ConfirmSession) and never mutated after,
// and taking m.mu here inverted the lock order against Shutdown's
// m.mu -> manifestMu path from a teammate loop (#5860 deadlock pin).
func (m *Manager) SessionNamespace() string {
	v, _ := m.sessionID.Load().(string)
	return v
}

func newManager(cfg config.SwarmConfig, sessionID string, prov provider.Provider, getter func() provider.Provider, factory AgentFactory, builder ToolBuilder) *Manager {
	if sessionID != "" && !validSessionID(sessionID) {
		panic("swarm: invalid session id: " + sessionID)
	}
	// sessionID stored via atomic.Value in the struct literal below
	if cfg.MaxTeammatesPerTeam <= 0 {
		cfg.MaxTeammatesPerTeam = 16
	}

	if cfg.InboxSize <= 0 {
		cfg.InboxSize = 32
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 1 * time.Second
	}
	// NOTE: TeammateEarlyCompactRatio is intentionally NOT defaulted here.
	// The 0.70 default lives in config.DefaultConfig so an explicit
	// teammate_early_compact_ratio: 0 can disable the early trigger
	// (a manager-side zero-default would make that indistinguishable).
	rootCtx, rootCancel := context.WithCancel(context.Background())
	mgr := &Manager{
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
	if sessionID != "" {
		mgr.sessionID.Store(sessionID)
	}
	return mgr
}
func (m *Manager) SetUsageHandler(fn func(provider.TokenUsage)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onUsage = fn
}

// SetPermissionPolicyGetter wires the permission-policy source used to
// gate tools on teammates spawned afterwards (#5464). Mirrors the main
// agent path (Agent.SetPermissionPolicy).
func (m *Manager) SetPermissionPolicyGetter(fn func() permission.PermissionPolicy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policyGetter = fn
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
	// Persistent teams P1: graceful shutdown flushes a clean journal footer
	// for every teammate so RestoreTeam can distinguish clean vs crashed.
	// (Takes no Manager locks; journals use their own store mutex.)
	for _, team := range m.teams {
		for _, tm := range team.listTeammates() {
			m.flushTeammateFooter(tm, "clean")
		}
	}
	if m.rootCancel != nil {
		m.rootCancel()
	}

	// Drain any in-flight manifest write: a stamp that already passed the
	// rootCtx liveness gate must finish (or be blocked out) before callers
	// proceed to workspace cleanup, or it could recreate a team directory
	// mid-removal. manifestMu is never held while acquiring m.mu, so this
	// cannot deadlock against the lock held here.
	m.manifestMu.Lock()
	m.manifestMu.Unlock()
}

func (m *Manager) CreateTeam(name, leaderID string) TeamSnapshot {
	// #5829: random collision-checked team ID (team-<6hex>). Display Name
	// stays human-readable. A fresh CreateTeam NEVER lands on an existing
	// directory — inheriting a foreign (even dead) board is an explicit
	// RestoreTeam decision, never an accident.
	id := m.newTeamID()

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

	// P0: own the directory from birth. P2: persist the manifest so the
	// #5860: no session confirmed yet (fresh session before its first
	// message) = teams cannot be created. Loud, not silent.
	if m.SessionNamespace() == "" {
		panic("swarm: team_create before the session was confirmed")
	}
	if err := m.writeManifest(id, &TeamManifest{
		TeamID:    id,
		Name:      name,
		LeaderID:  leaderID,
		CreatedAt: team.CreatedAt,
		Teammates: []ManifestTeammate{},
	}); err != nil {
		debug.Log("swarm", "manifest write for %s failed: %v", id, err)
	}

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

	// Persistent teams: flush clean footers, then remove the persisted team
	// directory (board + manifest + owner lease) — a deleted team is not
	// restorable (#5829).
	for _, tm := range team.listTeammates() {
		m.flushTeammateFooter(tm, "clean")
	}
	if err := os.RemoveAll(m.teamDir(teamID)); err != nil {
		debug.Log("swarm", "team dir removal for %s failed: %v", teamID, err)
	}

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
		m.persistTeamBoardAsync(teamID, tm)
	}
}

func (m *Manager) currentProvider() provider.Provider {
	if m.providerGetter != nil {
		return m.providerGetter()
	}
	return m.provider
}

// sortedToolsCopy returns a sorted defensive copy; nil stays nil.
func sortedToolsCopy(tools []string) []string {
	if len(tools) == 0 {
		return nil
	}
	out := append([]string(nil), tools...)
	sort.Strings(out)
	return out
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
		ID:           tmID,
		Name:         name,
		Color:        color,
		Status:       TeammateIdle,
		AllowedTools: sortedToolsCopy(allowedTools),
		Inbox:        make(chan MailMessage, m.cfg.InboxSize),
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
	}

	team.Teammates[tmID] = tm
	team.mu.Unlock()

	// Persistent teams P1: allocate the journal session before the agent
	// starts so every turn is written through from the first one.
	var journalSes *session.Session
	if m.JournalsEnabled {
		journalSes = m.allocateTeammateJournal(teamID, name)
	}
	tm.mu.Lock()
	tm.journal = journalSes
	tm.mu.Unlock()

	agent := m.constructTeammateAgent(team, tm)

	// P1: wire the journaler (same writer surface as the main loop).
	if journalSes != nil {
		if store, err := m.journalStore(); err == nil {
			attachTeammateJournaler(agent, store, journalSes)
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

	// P2: record the member (with its journal) in the persisted manifest.
	if err := m.upsertManifestTeammate(teamID, ManifestTeammate{
		ID:           tmID,
		Name:         name,
		Color:        color,
		AllowedTools: sortedToolsCopy(allowedTools),
		SessionFile:  teammateJournalID(tm),
		LastActiveAt: time.Now(),
	}); err != nil {
		debug.Log("swarm", "manifest upsert for %s/%s failed: %v", teamID, tmID, err)
	}

	return tm.snapshot(), nil
}

// constructTeammateAgent builds the teammate's agent runner with the shared
// wiring (tool set, system prompt, usage handler, permission policy,
// session id, early-compact ratio). Used by both SpawnTeammate and
// RestoreTeam so a restored teammate behaves identically to a fresh one.
func (m *Manager) constructTeammateAgent(team *Team, tm *Teammate) AgentRunner {
	var toolSet interface{}
	if m.toolBuilder != nil {
		toolSet = m.toolBuilder(tm.AllowedTools)
	}

	m.mu.Lock()
	builder := m.systemPromptBuilder
	wd := m.workingDir
	onUsage := m.onUsage
	m.mu.Unlock()
	var systemPrompt string
	if builder != nil {
		systemPrompt = builder(tm.Name, team.Name, wd)
	} else {
		systemPrompt = buildTeammateSystemPrompt(tm.Name, team.Name, wd)
	}
	var agent AgentRunner
	if m.agentFactory != nil {
		agent = m.agentFactory(m.currentProvider(), toolSet, systemPrompt, 0)
		if onUsage != nil {
			if usageAware, ok := agent.(usageHandlerSetter); ok {
				usageAware.SetUsageHandler(onUsage)
			}
		}
		// #5464: gate teammate tools with the active permission policy,
		// same as the primary agent path. No-op when no getter is wired
		// (tests / headless runners) or the runner has no policy setter.
		m.mu.Lock()
		policyGetter := m.policyGetter
		m.mu.Unlock()
		if policyGetter != nil {
			if pol := policyGetter(); pol != nil {
				if polSetter, ok := agent.(permissionPolicySetter); ok {
					polSetter.SetPermissionPolicy(pol)
				}
			}
		}

		if tm.ID != "" {
			if sid, ok := agent.(interface{ SetSessionID(string) }); ok {
				sid.SetSessionID(tm.ID)
			}
		}

		// #5002 gap 2 parity with the sub-agent runner: teammates run long
		// single-task sessions with no user-turn pacing, so wire the early
		// pre-compact trigger at ~70% of the window (default preset by
		// config.DefaultConfig; configurable via
		// swarm.teammate_early_compact_ratio, where 0 or a negative value
		// disables it, leaving only the reactive compaction path).
		if ratio := m.cfg.TeammateEarlyCompactRatio; ratio > 0 {
			if ec, ok := agent.(interface{ SetEarlyCompactRatio(float64) }); ok {
				ec.SetEarlyCompactRatio(ratio)
			} else if cm, ok := agent.(interface {
				ContextManager() interface {
					SetEarlyCompactRatio(float64)
				}
			}); ok && cm.ContextManager() != nil {
				cm.ContextManager().SetEarlyCompactRatio(ratio)
			}
		}
	}
	return agent
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

	// P1: graceful per-member shutdown marker. P2: drop the member from
	// the persisted manifest.
	m.flushTeammateFooter(tm, "clean")
	team.removeTeammate(tmID)
	if err := m.removeManifestTeammate(teamID, tmID); err != nil {
		debug.Log("swarm", "manifest member removal for %s/%s failed: %v", teamID, tmID, err)
	}

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

		team.Tasks = m.loadTeamBoard(teamID)
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

// GetTeammateTaskResult returns the archived result of tmID's most
// recently completed board task (persistTaskResult stores it in the
// task Metadata). Callers should prefer this over the session-level
// LastResult: a teammate that finished further work after the task you
// are asking about would otherwise return that newer (unrelated)
// output - the misattribution behind duplicate-looking reports.
func (m *Manager) GetTeammateTaskResult(teamID, tmID string) (taskID, result string, ok bool) {
	m.mu.Lock()
	team, exists := m.teams[teamID]
	m.mu.Unlock()
	if !exists || team.Tasks == nil {
		return "", "", false
	}
	var bestAt time.Time
	for _, tk := range team.Tasks.List() {
		if tk.Owner != tmID || tk.Status != task.StatusCompleted {
			continue
		}
		r := strings.TrimSpace(tk.Metadata[resultMetaKey])
		if r == "" || !tk.UpdatedAt.After(bestAt) {
			continue
		}
		bestAt, taskID, result, ok = tk.UpdatedAt, tk.ID, r, true
	}
	return taskID, result, ok
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

				// P2: stamp the done-mail digest into the manifest
				// (#5822 hook). Synchronous: the teammate loop can afford
				// one small locked JSON write per task, and an async
				// goroutine here raced test/workspace teardown.
				m.stampTeammateResult(ev.TeamID, ev.TeammateID, ev.Result, time.Now())

				outcome := "success"
				if ev.IsError || ev.Error != nil {
					outcome = "failed"
				}
				appendTeammateExperience(m.workingDirForExp(), ev.TeamID, ev.TeammateID, ev.TeammateName, ev.Result, outcome)
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

// randomHex recovered from the deleted owner_lease.go (#5860).
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%0*x", n*2, time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// newTeamID recovered from the deleted owner_lease.go (#5860): random
// team-<6hex>, collision-checked in memory and on disk.
func (m *Manager) newTeamID() string {
	for attempt := 0; attempt < 8; attempt++ {
		id := "team-" + randomHex(3)
		m.mu.Lock()
		_, inMem := m.teams[id]
		m.mu.Unlock()
		if inMem {
			continue
		}
		if _, err := os.Stat(m.teamDir(id)); err == nil {
			debug.Log("swarm", "newTeamID: %s collides with an existing directory, retrying", id)
			continue
		}
		return id
	}
	return "team-" + randomHex(8)
}

// validSessionID: timestamps with an optional trailing token (the session
// store's "<date>-<time>-<hex>" family plus plain run IDs).
func validSessionID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c == '-' || c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			continue
		}
		return false
	}
	return true
}
