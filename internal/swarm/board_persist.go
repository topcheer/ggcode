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

// board_persist.go - durable swarm task boards (#348-style durable queues:
// boards must survive daemon restarts instead of being process state).
// The task.Manager persistence primitives (SnapshotJSON/RestoreJSON, used by
// the TUI session board since TasksJSON) are reused verbatim; every board
// mutation funnels through EmitBoardUpdated (see tool/swarm_task_tools.go),
// which snapshots asynchronously after notifying UI subscribers.

// persistMu serializes board snapshots so a slow write cannot let an older
// snapshot overwrite a newer one (last-write-wins must be the latest state).
var persistMu sync.Mutex

// boardPersistPath returns the durable location for a team's task board.
// teamID is generated as "team-N" (manager.go:174), so it is path-safe.
func boardPersistPath(teamID string) string {
	return filepath.Join(util.ConfigDir(), "swarm", "teams", teamID, "board.json")
}

// boardLockPath returns the advisory lock file guarding a team board's
// cross-process claim section (#4781 / S-10).
func boardLockPath(teamID string) string {
	return filepath.Join(util.ConfigDir(), "swarm", "teams", teamID, "board.lock")
}

// ClaimTaskSingleWinner (#4781 / S-10): claim a pending board task with
// CROSS-PROCESS single-winner semantics.
//
// The #861 ExpectedStatus CAS already makes racing claims single-winner
// INSIDE one process. But each ggcode instance keeps its own in-memory
// task.Manager (EnsureTaskManager loads the disk board once, lazily) and
// persists via an async snapshot - so two SEATS racing to claim both "won"
// their local CAS and the last async write silently stole ownership: the
// broadcast-claim event that produced 7 concurrent claims of 2 items.
//
// This method serializes the claim section across processes with an
// advisory file lock (util.FileLock: flock / LockFileEx):
//  1. acquire the per-team board lock
//  2. re-read the DURABLE board: if the task is already in_progress with a
//     DIFFERENT owner there, the other seat's claim already landed - return
//     an explicit error naming the winner (same-owner re-claim is
//     idempotent)
//  3. CAS the live manager (ExpectedStatus=pending, #861 semantics)
//  4. persist synchronously (persistTeamBoardNow) so the next lock holder
//     observes the claim
//
// The lock guards the claim race specifically; other board mutations keep
// their async snapshot path.
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

	// Cross-seat loser check: what does the durable board say NOW? Raw
	// view - the restart rollback must not erase a live claim from the
	// winning seat.
	if disk := loadTeamBoardRaw(teamID); disk != nil {
		if dt, ok := disk.Get(taskID); ok && dt.Status == task.StatusInProgress && dt.Owner != "" {
			if dt.Owner != owner {
				return nil, fmt.Errorf("task %s was already claimed by %s (in_progress on the shared board); pick another task or coordinate with the owner (#4781)", taskID, dt.Owner)
			}
			// Idempotent same-owner re-claim.
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
		// Same-process race loser (the CAS fired first): enrich the raw
		// status error with the winning owner when visible, so every loser
		// gets the same actionable message (#4781).
		if cur, ok := tm.Get(taskID); ok && cur.Status == task.StatusInProgress && cur.Owner != "" && cur.Owner != owner {
			return nil, fmt.Errorf("task %s was already claimed by %s (in_progress on the shared board); pick another task or coordinate with the owner (#4781)", taskID, cur.Owner)
		}
		return nil, err
	}
	// Synchronous persist: the winner must be visible to the next lock
	// holder, not just to this process's async snapshot.
	if err := persistTeamBoardNow(teamID, tm); err != nil {
		debug.Log("swarm", "claim persist failed for %s/%s: %v (async snapshot will retry)", teamID, taskID, err)
	}
	return &updated, nil
}

// persistTeamBoardNow writes the board snapshot atomically (temp + rename).
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

// persistTeamBoardAsync snapshots the board off the hot path. Snapshot
// failures are logged, never fatal: persistence is best-effort durability.
func persistTeamBoardAsync(teamID string, tm *task.Manager) {
	safego.Go("swarm.boardPersist", func() {
		persistMu.Lock()
		defer persistMu.Unlock()
		if err := persistTeamBoardNow(teamID, tm); err != nil {
			debug.Log("swarm", "board persist for %s: %v", teamID, err)
		}
	})
}

// loadTeamBoard restores a team's board from disk. On any failure (missing
// file, corrupt data - RestoreJSON rejects bad versions/payloads) a fresh
// empty board is returned. in_progress tasks are rolled back to pending:
// after a restart their owners are gone, so the tasks must be re-claimable.
//
// #4363: an undecodable board is QUARANTINED (renamed to
// board.json.corrupt-<ts>) before the empty board loads. Without the
// rename, the very next board change would atomically overwrite the file
// via persistTeamBoardNow (temp+rename full rewrite) and the original
// content - the team's entire task history - would be unrecoverable. The
// rename keeps the scene for manual recovery; the fresh board is identical
// either way.
func loadTeamBoard(teamID string) *task.Manager {
	tm := loadTeamBoardRaw(teamID)
	// Crash recovery: re-open claims. The in_progress owner died with the
	// previous process; rolling back to pending makes the task claimable
	// again while keeping subject/description/dependencies intact.
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

// loadTeamBoardRaw restores the durable board WITHOUT the crash-recovery
// rollback: in_progress tasks keep their owners. The #4781 single-winner
// claim check reads this view - a live claim from another seat must be
// visible as claimed, not erased by the restart rollback.
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
