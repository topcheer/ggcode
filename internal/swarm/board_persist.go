package swarm

import (
	"os"
	"path/filepath"
	"sync"

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
func loadTeamBoard(teamID string) *task.Manager {
	tm := task.NewManager()
	data, err := os.ReadFile(boardPersistPath(teamID))
	if err != nil {
		return tm
	}
	if err := tm.RestoreJSON(data); err != nil {
		debug.Log("swarm", "board restore for %s: %v (starting empty)", teamID, err)
		return task.NewManager()
	}
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
