package swarm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/task"
	"github.com/topcheer/ggcode/internal/util"
)

var persistMu sync.Mutex

// errNoConfirmedSession is returned by every read / restore / delete entry
// point while the session namespace is still empty. With no namespace,
// sessionTeamsDir() collapses to the SHARED root <configDir>/swarm/teams,
// which also holds other sessions' namespaces and the pre-#5860 legacy
// layout teams/<teamID>/ (no session at all). Scanning it restored every
// legacy team of every workspace into a fresh instance, which then
// re-persisted them under its own new session id (field report 2026-11-26:
// a new instance at a parent directory came up with 5 foreign teams).
// CreateTeam already panics on an empty namespace; these are the read-side
// twins.
var errNoConfirmedSession = errors.New("swarm: no confirmed session - teams are session-scoped and cannot be listed, restored or deleted before the first message")

// boardRoot returns the persist root for team boards (#5540). Default is
// the user config dir; tests override the BoardRoot field with a t.TempDir()
// so per-Manager deterministic team IDs (team-1) cannot collide across
// tests/packages on the shared on-disk path.
func (m *Manager) boardRoot() string {
	if m.BoardRoot != "" {
		return m.BoardRoot
	}
	return util.ConfigDir()
}

// #5860: teams/<sessionID>/<teamID>/ - the confirmed session id is the
// namespace; the directory is the ownership. An empty namespace (session
// never confirmed) must never create, persist, list, restore or delete a
// team: CreateTeam panics on it and the read paths return
// errNoConfirmedSession. NOTE: with an empty namespace this path collapses
// onto the SHARED root, so no caller may use it before checking
// SessionNamespace() != "". The legacy layout teams/<teamID>/ (no session,
// pre-#5860 binaries, #5867) is never adopted by this code; upgrading the
// processes is the cure, not re-allowing unsessioned boards here.
func (m *Manager) sessionTeamsDir() string {
	return filepath.Join(m.boardRoot(), "swarm", "teams", m.SessionNamespace())
}

func (m *Manager) teamDir(teamID string) string {
	return filepath.Join(m.sessionTeamsDir(), teamID)
}

func (m *Manager) boardPersistPath(teamID string) string {
	return filepath.Join(m.teamDir(teamID), "board.json")
}

func (m *Manager) boardLockPath(teamID string) string {
	return filepath.Join(m.teamDir(teamID), "board.lock")
}

func (m *Manager) ClaimTaskSingleWinner(teamID, taskID, owner string) (*task.Task, error) {
	tm, err := m.EnsureTaskManager(teamID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(m.boardLockPath(teamID)), 0o755); err != nil {
		return nil, fmt.Errorf("claim lock dir: %w", err)
	}
	unlock, err := util.FileLock(m.boardLockPath(teamID))
	if err != nil {
		return nil, fmt.Errorf("claim lock: %w", err)
	}
	defer unlock()

	if disk := m.loadTeamBoardRaw(teamID); disk != nil {
		if dt, ok := disk.Get(taskID); ok && dt.Status == task.StatusInProgress && dt.Owner != "" {
			if dt.Owner != owner {
				return nil, fmt.Errorf("task %s was already claimed by %s (in_progress on the shared board); pick another task or coordinate with the owner (#4781)", taskID, dt.Owner)
			}

			res := dt
			return &res, nil
		}
	}

	pending := task.TaskStatus(task.StatusPending)
	inProgress := task.TaskStatus(task.StatusInProgress)
	updated, err := tm.Update(taskID, task.UpdateOptions{
		Status:         &inProgress,
		Owner:          &owner,
		ExpectedStatus: &pending,
	})
	if err != nil {

		if cur, ok := tm.Get(taskID); ok && cur.Status == task.StatusInProgress && cur.Owner != "" && cur.Owner != owner {
			return nil, fmt.Errorf("task %s was already claimed by %s (in_progress on the shared board); pick another task or coordinate with the owner (#4781)", taskID, cur.Owner)
		}
		return nil, err
	}

	if err := m.persistTeamBoardNow(teamID, tm); err != nil {
		debug.Log("swarm", "claim persist failed for %s/%s: %v (async snapshot will retry)", teamID, taskID, err)
	}
	return &updated, nil
}

func (m *Manager) persistTeamBoardNow(teamID string, tm *task.Manager) error {
	data, err := tm.SnapshotJSON()
	if err != nil {
		return err
	}
	path := m.boardPersistPath(teamID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return nil
}

func (m *Manager) persistTeamBoardAsync(teamID string, tm *task.Manager) {
	safego.Go("swarm.boardPersist", func() {
		persistMu.Lock()
		defer persistMu.Unlock()
		if err := m.persistTeamBoardNow(teamID, tm); err != nil {
			debug.Log("swarm", "board persist for %s: %v", teamID, err)
		}
	})
}

func (m *Manager) loadTeamBoard(teamID string) *task.Manager {
	tm := m.loadTeamBoardRaw(teamID)

	rolled := 0
	for _, t := range tm.List() {
		if t.Status == task.StatusInProgress {
			st := task.StatusPending
			if _, err := tm.Update(t.ID, task.UpdateOptions{Status: &st}); err == nil {
				rolled++
			}
		}
	}
	if rolled > 0 {
		debug.Log("swarm", "board restore for %s: rolled back %d in_progress task(s) to pending", teamID, rolled)
	}

	// Root cause of "teammate re-runs finished tasks after restart": board
	// persistence used to fire only from EmitBoardUpdated, whose callers
	// are all tool-layer (leader-issued swarm_task_*). The teammate idle
	// loop's own transitions (claim -> in_progress, complete, rollback)
	// mutated the in-memory board only, so board.json on disk stayed at the
	// creation snapshot (all pending) and the next restart re-claimed
	// everything. Hook every later mutation to a synchronous flush so no
	// call site can forget, and a kill right after a transition still finds
	// the new state on disk.
	//
	// Wired AFTER the in_progress -> pending rollback on purpose: that
	// rollback is an in-memory assumption ("the previous owner died") that
	// must not clobber the disk record while another live seat may still
	// own the task - ClaimTaskSingleWinner's disk check relies on it
	// (#4781). Once this seat really claims, the claim itself persists.
	// loadTeamBoardRaw (throwaway reads) deliberately stays unwired.
	tm.SetOnChange(func() {
		persistMu.Lock()
		defer persistMu.Unlock()
		if err := m.persistTeamBoardNow(teamID, tm); err != nil {
			debug.Log("swarm", "board persist for %s: %v", teamID, err)
		}
	})

	return tm
}

func (m *Manager) loadTeamBoardRaw(teamID string) *task.Manager {
	tm := task.NewManager()
	data, err := os.ReadFile(m.boardPersistPath(teamID))
	if err != nil {
		return tm
	}
	if err := tm.RestoreJSON(data); err != nil {
		src := m.boardPersistPath(teamID)
		q := fmt.Sprintf("%s.corrupt-%d", src, time.Now().Unix())
		if rerr := os.Rename(src, q); rerr == nil {
			debug.Log("swarm", "board restore for %s FAILED (%v): original board quarantined at %s, starting empty - inspect the quarantine file before any board change", teamID, err, q)
		} else {
			debug.Log("swarm", "board restore for %s FAILED (%v); quarantine rename also failed (%v), starting empty - the original file may be overwritten by the next board change", teamID, err, rerr)
		}
		return task.NewManager()
	}
	return tm
}
