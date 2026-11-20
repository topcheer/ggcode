package swarm

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/task"
)

// ---------------------------------------------------------------------------
// D1: teammate early-compact wiring
// ---------------------------------------------------------------------------

// earlyCompactRecorder records SetEarlyCompactRatio invocations issued by
// Manager.SpawnTeammate on factory-constructed agents.
type earlyCompactRecorder struct {
	mu     sync.Mutex
	ratio  float64
	called bool
}

func (a *earlyCompactRecorder) RunStream(context.Context, string, func(provider.StreamEvent), ...string) error {
	return nil
}

func (a *earlyCompactRecorder) SetEarlyCompactRatio(r float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ratio = r
	a.called = true
}

func (a *earlyCompactRecorder) get() (float64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ratio, a.called
}

func newEarlyCompactManager(t *testing.T, cfg config.SwarmConfig, rec *earlyCompactRecorder) (*Manager, TeamSnapshot) {
	t.Helper()
	m := NewManager(cfg, "sess-t", nil, func(provider.Provider, interface{}, string, int) AgentRunner {
		return rec
	}, nil)
	m.BoardRoot = t.TempDir()
	team := m.CreateTeam("ec-team", "leader-1")
	return m, team
}

func TestSpawnTeammateWiresDefaultEarlyCompactRatio(t *testing.T) {
	rec := &earlyCompactRecorder{}
	// Mirror the production path: cfg.Swarm comes from config.Load, which
	// unmarshals over the 0.70 default preset by config.DefaultConfig.
	cfg := config.DefaultConfig().Swarm
	cfg.MaxTeammatesPerTeam = 4
	m, team := newEarlyCompactManager(t, cfg, rec)
	defer m.Shutdown()

	if _, err := m.SpawnTeammate(team.ID, "worker", "blue", nil); err != nil {
		t.Fatalf("SpawnTeammate: %v", err)
	}
	ratio, called := rec.get()
	if !called {
		t.Fatal("SpawnTeammate must call SetEarlyCompactRatio on factory agents implementing it (default 0.70 wiring, mirroring internal/subagent/runner.go)")
	}
	if ratio != 0.70 {
		t.Fatalf("default teammate early-compact ratio = %v, want 0.70", ratio)
	}
}

func TestSpawnTeammateConfigOverridesEarlyCompactRatio(t *testing.T) {
	rec := &earlyCompactRecorder{}
	m, team := newEarlyCompactManager(t, config.SwarmConfig{
		MaxTeammatesPerTeam:       4,
		TeammateEarlyCompactRatio: 0.5,
	}, rec)
	defer m.Shutdown()

	if _, err := m.SpawnTeammate(team.ID, "worker", "blue", nil); err != nil {
		t.Fatalf("SpawnTeammate: %v", err)
	}
	ratio, called := rec.get()
	if !called {
		t.Fatal("configured teammate_early_compact_ratio must be forwarded to the spawned agent")
	}
	if ratio != 0.5 {
		t.Fatalf("teammate early-compact ratio = %v, want configured 0.5", ratio)
	}
}

func TestSpawnTeammateZeroOrNegativeConfigDisablesEarlyCompact(t *testing.T) {
	for name, ratio := range map[string]float64{"explicit zero": 0, "negative": -1} {
		t.Run(name, func(t *testing.T) {
			rec := &earlyCompactRecorder{}
			m, team := newEarlyCompactManager(t, config.SwarmConfig{
				MaxTeammatesPerTeam:       4,
				TeammateEarlyCompactRatio: ratio,
			}, rec)
			defer m.Shutdown()

			if _, err := m.SpawnTeammate(team.ID, "worker", "blue", nil); err != nil {
				t.Fatalf("SpawnTeammate: %v", err)
			}
			if _, called := rec.get(); called {
				t.Fatalf("teammate_early_compact_ratio %v must disable the early trigger (no SetEarlyCompactRatio call)", ratio)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// D2 + D3 harness: scripted agents, blocked leader
// ---------------------------------------------------------------------------

// scriptedAgent is a deterministic AgentRunner: it emits one text event (or
// one error event), optionally blocking in RunStream until blockCh closes.
type scriptedAgent struct {
	mu      sync.Mutex
	calls   int
	text    string
	err     error
	blockCh <-chan struct{}
	started chan struct{}
}

func (a *scriptedAgent) RunStream(_ context.Context, _ string, onEvent func(provider.StreamEvent), _ ...string) error {
	a.mu.Lock()
	a.calls++
	first := a.calls == 1
	a.mu.Unlock()
	if a.started != nil && first {
		select {
		case <-a.started:
		default:
			close(a.started)
		}
	}
	if a.blockCh != nil {
		<-a.blockCh
	}
	if a.err != nil {
		if onEvent != nil {
			onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: a.err})
		}
		return a.err
	}
	if onEvent != nil && a.text != "" {
		onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: a.text})
	}
	return nil
}

func (a *scriptedAgent) getCalls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

// newDoneMailTeam builds a team whose leader is a real spawned teammate
// (LeaderID repointed in-package) whose agent blocks on the returned
// channel, so its inbox is guaranteed to retain anything sent to it.
func newDoneMailTeam(t *testing.T, pollInterval time.Duration, worker *scriptedAgent) (*Manager, TeamSnapshot, TeammateSnapshot, *scriptedAgent, chan struct{}) {
	t.Helper()
	leaderBlock := make(chan struct{})
	leaderAgent := &scriptedAgent{blockCh: leaderBlock, started: make(chan struct{})}
	var mu sync.Mutex
	leaderSpawned := false
	m := NewManager(config.SwarmConfig{
		MaxTeammatesPerTeam: 4,
		InboxSize:           16,
		TeammateTimeout:     30 * time.Second,
		PollInterval:        pollInterval,
	},
		"sess-t", nil, func(provider.Provider, interface{}, string, int) AgentRunner {
			mu.Lock()
			defer mu.Unlock()
			if !leaderSpawned {
				leaderSpawned = true
				return leaderAgent
			}
			return worker
		}, nil)
	m.BoardRoot = t.TempDir()
	m.SessionRoot = t.TempDir() // journal store isolation (#5860 tests)
	teamSnap := m.CreateTeam("done-team", "leader-1")

	leadSnap, err := m.SpawnTeammate(teamSnap.ID, "leader", "white", nil)
	if err != nil {
		t.Fatalf("spawn leader: %v", err)
	}
	workSnap, err := m.SpawnTeammate(teamSnap.ID, "worker", "blue", nil)
	if err != nil {
		t.Fatalf("spawn worker: %v", err)
	}
	m.mu.Lock()
	team := m.teams[teamSnap.ID]
	m.mu.Unlock()
	team.mu.Lock()
	team.LeaderID = leadSnap.ID
	team.mu.Unlock()

	// Occupy the leader's loop with a blocking first task so its inbox is
	// not drained while assertions run against it.
	if err := m.SendToTeammate(teamSnap.ID, leadSnap.ID, MailMessage{Content: "hold the line", Type: "task"}); err != nil {
		t.Fatalf("seed leader task: %v", err)
	}
	select {
	case <-leaderAgent.started:
	case <-time.After(5 * time.Second):
		t.Fatal("leader agent never started its seed task")
	}
	return m, teamSnap, workSnap, leaderAgent, leaderBlock
}

func leaderTeammate(t *testing.T, m *Manager, teamID string) *Teammate {
	t.Helper()
	m.mu.Lock()
	team := m.teams[teamID]
	m.mu.Unlock()
	team.mu.RLock()
	defer team.mu.RUnlock()
	for _, tm := range team.Teammates {
		if tm.ID == team.LeaderID {
			return tm
		}
	}
	t.Fatal("leader teammate not found in team")
	return nil
}

// waitForDoneSummary polls the leader's inbox for a done-summary mail.
func waitForDoneSummary(t *testing.T, leaderTm *Teammate, timeout time.Duration) (MailMessage, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var got MailMessage
		select {
		case got = <-leaderTm.Inbox:
			if strings.HasPrefix(got.Summary, "[done ") {
				return got, true
			}
		default:
		}
		if time.Now().After(deadline) {
			return MailMessage{}, false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDoneSummaryMailedToLeaderOnDirectTaskSuccess(t *testing.T) {
	worker := &scriptedAgent{text: "first line of the result\nsecond line is ignored"}
	m, teamSnap, workSnap, leaderAgent, leaderBlock := newDoneMailTeam(t, 5*time.Second, worker)
	defer m.Shutdown()
	defer close(leaderBlock)

	reply := make(chan TaskResult, 1)
	if err := m.SendToTeammate(teamSnap.ID, workSnap.ID, MailMessage{Content: "do the work", Type: "task", ReplyTo: reply}); err != nil {
		t.Fatalf("send worker task: %v", err)
	}
	select {
	case res := <-reply:
		if res.Error != nil {
			t.Fatalf("worker task failed: %v", res.Error)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no TaskResult from worker")
	}

	got, ok := waitForDoneSummary(t, leaderTeammate(t, m, teamSnap.ID), 5*time.Second)
	if !ok {
		t.Fatal("leader inbox never received the done-summary after successful task")
	}
	if !strings.HasPrefix(got.Summary, "[done worker]") {
		t.Fatalf("done-summary = %q, want prefix [done worker]", got.Summary)
	}
	if !strings.Contains(got.Summary, "first line of the result") {
		t.Fatalf("done-summary = %q, want result first line", got.Summary)
	}
	if strings.Contains(got.Summary, "second line") {
		t.Fatalf("done-summary = %q, must only carry the first line", got.Summary)
	}
	if leaderAgent.getCalls() != 1 {
		t.Fatalf("leader agent calls = %d, want 1 (still blocked on seed task)", leaderAgent.getCalls())
	}
}

func TestDoneSummaryNotMailedOnTaskFailure(t *testing.T) {
	worker := &scriptedAgent{err: errors.New("boom")}
	m, teamSnap, workSnap, _, leaderBlock := newDoneMailTeam(t, 5*time.Second, worker)
	defer m.Shutdown()
	defer close(leaderBlock)

	reply := make(chan TaskResult, 1)
	if err := m.SendToTeammate(teamSnap.ID, workSnap.ID, MailMessage{Content: "do the work", Type: "task", ReplyTo: reply}); err != nil {
		t.Fatalf("send worker task: %v", err)
	}
	select {
	case res := <-reply:
		if res.Error == nil {
			t.Fatal("worker task unexpectedly succeeded; want failure")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no TaskResult from worker")
	}

	if got, ok := waitForDoneSummary(t, leaderTeammate(t, m, teamSnap.ID), 2*time.Second); ok {
		t.Fatalf("failure path must not mail a done-summary, got %q", got.Summary)
	}
}

func TestBoardTaskDoneSummaryCarriesTaskID(t *testing.T) {
	worker := &scriptedAgent{text: "board result line 1"}
	m, teamSnap, _, _, leaderBlock := newDoneMailTeam(t, 50*time.Millisecond, worker)
	defer m.Shutdown()
	defer close(leaderBlock)

	board, err := m.EnsureTaskManager(teamSnap.ID)
	if err != nil {
		t.Fatalf("EnsureTaskManager: %v", err)
	}
	tk := board.Create("Implement X", "do X", "", nil)

	// Wait for the idle runner to claim and complete the board task.
	deadline := time.Now().Add(10 * time.Second)
	for {
		cur, ok := board.Get(tk.ID)
		if ok && cur.Status == task.StatusCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("board task %s never completed (status=%v)", tk.ID, cur.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	got, ok := waitForDoneSummary(t, leaderTeammate(t, m, teamSnap.ID), 5*time.Second)
	if !ok {
		t.Fatalf("leader inbox never received the done-summary for board task %s", tk.ID)
	}
	want := "[done worker] task " + tk.ID + ": board result line 1"
	if got.Summary != want {
		t.Fatalf("done-summary = %q, want %q", got.Summary, want)
	}
}

// ---------------------------------------------------------------------------
// D3: LastActiveAt
// ---------------------------------------------------------------------------

func TestLastActiveAtAdvancesAfterTaskExecution(t *testing.T) {
	worker := &scriptedAgent{text: "done"}
	var mu sync.Mutex
	workerSpawned := false
	m := NewManager(config.SwarmConfig{
		MaxTeammatesPerTeam: 4,
		InboxSize:           16,
		TeammateTimeout:     30 * time.Second,
		PollInterval:        5 * time.Second,
	},
		"sess-t", nil, func(provider.Provider, interface{}, string, int) AgentRunner {
			mu.Lock()
			defer mu.Unlock()
			workerSpawned = true
			return worker
		}, nil)
	m.BoardRoot = t.TempDir()
	defer m.Shutdown()
	_ = workerSpawned

	teamSnap := m.CreateTeam("active-team", "leader-1")
	workSnap, err := m.SpawnTeammate(teamSnap.ID, "worker", "blue", nil)
	if err != nil {
		t.Fatalf("SpawnTeammate: %v", err)
	}

	snap0, ok := m.TeammateSnapshot(workSnap.ID)
	if !ok {
		t.Fatal("teammate snapshot not found")
	}
	if snap0.LastActiveAt.IsZero() {
		t.Fatal("LastActiveAt must be initialized at spawn / loop start")
	}

	time.Sleep(2 * time.Millisecond)
	reply := make(chan TaskResult, 1)
	if err := m.SendToTeammate(teamSnap.ID, workSnap.ID, MailMessage{Content: "ping", Type: "task", ReplyTo: reply}); err != nil {
		t.Fatalf("send task: %v", err)
	}
	select {
	case res := <-reply:
		if res.Error != nil {
			t.Fatalf("task failed: %v", res.Error)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no TaskResult")
	}

	snap1, ok := m.TeammateSnapshot(workSnap.ID)
	if !ok {
		t.Fatal("teammate snapshot not found after task")
	}
	if !snap1.LastActiveAt.After(snap0.LastActiveAt) {
		t.Fatalf("LastActiveAt did not advance after task execution: %v -> %v", snap0.LastActiveAt, snap1.LastActiveAt)
	}
}
