package swarm

import (
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

func boardPersistPath(teamID string) string {
	return filepath.Join(util.ConfigDir(), "swarm", "teams", teamID, "board.json")
}

func boardLockPath(teamID string) string {
	return filepath.Join(util.ConfigDir(), "swarm", "teams", teamID, "board.lock")
}

func (m *Manager) ClaimTaskSingleWinner(teamID, taskID, owner string) (*task.Task, error) {
	tm, err := m.EnsureTaskManager(teamID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(boardLockPath(teamID)), 0o755); err != nil {
		return nil, fmt.Errorf("claim lock dir: %w", err)
	}
	unlock, err := util.FileLock(boardLockPath(teamID))
	if err != nil {
		return nil, fmt.Errorf("claim lock: %w", err)
	}
	defer unlock()

	if disk := loadTeamBoardRaw(teamID); disk != nil {
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

	if err := persistTeamBoardNow(teamID, tm); err != nil {
		debug.Log("swarm", "claim persist failed for %s/%s: %v (async snapshot will retry)", teamID, taskID, err)
	}
	return &updated, nil
}

func persistTeamBoardNow(teamID string, tm *task.Manager) error {
	data, err := tm.SnapshotJSON()
	if err != nil {
		return err
	}
	path := boardPersistPath(teamID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func persistTeamBoardAsync(teamID string, tm *task.Manager) {
	// #5540: resolve the target path and snapshot the board SYNCHRONOUSLY,
	// at queue time. The goroutine only performs disk I/O against the
	// captured path/data. Resolving ConfigDir() (HOME-derived) inside the
	// goroutine made the write target depend on which test's environment
	// happened to be active when the goroutine got scheduled — a late
	// persist from test N-1 would land in test N's sandbox and trip its
	// TempDir cleanup (environment-dependent flake, CI-observed on
	// TestSwarmTaskClaimTool).
	data, err := tm.SnapshotJSON()
	if err != nil {
		debug.Log("swarm", "board snapshot for %s: %v", teamID, err)
		return
	}
	path := boardPersistPath(teamID)
	persistWg.Add(1)
	safego.Go("swarm.boardPersist", func() {
		defer persistWg.Done()
		persistMu.Lock()
		defer persistMu.Unlock()
		if err := writeTeamBoardAtomically(path, data); err != nil {
			debug.Log("swarm", "board persist for %s: %v", teamID, err)
		}
	})
}

// persistWg tracks in-flight async board persists so tests (and any future
// graceful-stop path) can wait for the goroutines deterministically (#5540).
var persistWg sync.WaitGroup

// DrainBoardPersists blocks until every queued async board persist has
// finished its disk write. Test helpers call this before removing the
// sandboxed HOME tree.
func DrainBoardPersists() {
	persistWg.Wait()
}

// writeTeamBoardAtomically writes data to path via tmp+rename.
func writeTeamBoardAtomically(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadTeamBoard(teamID string) *task.Manager {
	tm := loadTeamBoardRaw(teamID)

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
	return tm
}

func loadTeamBoardRaw(teamID string) *task.Manager {
	tm := task.NewManager()
	data, err := os.ReadFile(boardPersistPath(teamID))
	if err != nil {
		return tm
	}
	if err := tm.RestoreJSON(data); err != nil {
		src := boardPersistPath(teamID)
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
