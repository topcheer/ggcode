package swarm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/task"
)

func newBoardTestManager(t *testing.T) *Manager {
	t.Helper()
	m := NewManager(config.SwarmConfig{}, nil,
		func(_ provider.Provider, _ interface{}, _ string, _ int) AgentRunner {
			return &mockAgentRunner{
				runStreamFn: func(ctx context.Context, _ string, _ func(provider.StreamEvent)) error {
					<-ctx.Done()
					return nil
				},
			}
		},
		func(_ []string) interface{} { return nil },
	)
	t.Cleanup(m.Shutdown)
	return m
}

func waitForFile(t *testing.T, path string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestBoardPersistenceRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	m := newBoardTestManager(t)
	team := m.CreateTeam("persist-team", "leader-1")

	tm, err := m.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatalf("EnsureTaskManager: %v", err)
	}
	t1 := tm.Create("alpha", "first task", "", nil)
	t2 := tm.Create("beta", "second task", "", nil)
	ip := task.StatusInProgress
	if _, err := tm.Update(t1.ID, task.UpdateOptions{Status: &ip}); err != nil {
		t.Fatalf("claim t1: %v", err)
	}

	m.EmitBoardUpdated(team.ID)
	boardFile := boardPersistPath(team.ID)
	if !waitForFile(t, boardFile, 3*time.Second) {
		t.Fatalf("board snapshot never landed at %s", boardFile)
	}

	m2 := newBoardTestManager(t)
	team2 := m2.CreateTeam("persist-team", "leader-2")
	_ = team2
	restored, err := m2.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatalf("EnsureTaskManager(restart): %v", err)
	}
	got1, ok1 := restored.Get(t1.ID)
	got2, ok2 := restored.Get(t2.ID)
	if !ok1 || !ok2 {
		t.Fatalf("restored board lost tasks: t1=%v t2=%v", ok1, ok2)
	}

	if got1.Status != task.StatusPending {
		t.Errorf("in_progress task not rolled back to pending: got %s", got1.Status)
	}
	if got2.Status != task.StatusPending {
		t.Errorf("pending task changed status: got %s", got2.Status)
	}
	if got1.Subject != "alpha" || got2.Subject != "beta" {
		t.Errorf("restored subjects wrong: %q %q", got1.Subject, got2.Subject)
	}
}

func TestBoardPersistenceCorruptFileStartsEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path := boardPersistPath("team-99")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":999,"garbage":`), 0o644); err != nil {
		t.Fatal(err)
	}

	tm := loadTeamBoard("team-99")
	if tasks := tm.List(); len(tasks) != 0 {
		t.Errorf("corrupt board should start empty, got %d task(s)", len(tasks))
	}
}

func TestBoardPersistenceMissingFileStartsEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tm := loadTeamBoard("team-404")
	if tasks := tm.List(); len(tasks) != 0 {
		t.Errorf("missing board should start empty, got %d task(s)", len(tasks))
	}
}

// #5540 companion pins: queue-time path capture + deterministic drain.
func TestBoardPersistAsyncDrainAndPathCapture(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := NewManager(config.SwarmConfig{}, nil, nil, nil)
	defer m.Shutdown()
	team := m.CreateTeam("drain-pin", "leader")
	tm, err := m.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatalf("task manager: %v", err)
	}
	created := tm.Create("pin task", "desc", "", nil)
	if created.ID == "" {
		t.Fatal("create must yield an ID")
	}

	// Queue an async persist, then drain: after DrainBoardPersists the
	// write MUST be complete (deterministic barrier, not a poll guess).
	persistTeamBoardAsync(team.ID, tm)
	DrainBoardPersists()
	path := boardPersistPath(team.ID)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("drained persist must have written %s: %v", path, err)
	}

	// Path capture: the queued write resolves its target at queue time.
	// Change HOME mid-flight (simulating the next test's env); draining a
	// second persist must still land at the ORIGINAL captured path.
	before, _ := os.ReadFile(path)
	persistTeamBoardAsync(team.ID, tm)
	t.Setenv("HOME", t.TempDir()) // env now differs from queue time
	DrainBoardPersists()
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("persist must land at the queue-time-captured path: %v", err)
	}
	if string(before) != string(after) || !strings.Contains(string(after), created.ID) {
		t.Fatalf("board content must round-trip at the captured path (task %s)", created.ID)
	}
}
