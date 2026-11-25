package swarm

import (
	"github.com/topcheer/ggcode/internal/debug"
)

// AutoRestorePersistedTeams (#5829 follow-up G2b): bring restorable
// persisted teams back to life immediately when the manager becomes
// available - NOT waiting for the user to mention them.
//
// Design intent (user direction 2026-11-20): "when a session resumes and a
// persisted team exists, the team must be rebuilt right away". The earlier
// prompt-surface approach (G2) only made teams DISCOVERABLE; this makes
// them PRESENT: teammate_list / swarm_task_list work from the first turn.
//
// Safety: only teams whose owner lease is inheritable (dead / legacy /
// ours / self-PID exec-successor) are restored. A team owned by a LIVE
// foreign instance is left alone (cross-instance safety unchanged). The
// first restorer re-stamps its lease, so concurrent starters naturally
// converge (the loser sees alive-foreign and skips).
//
// Explicit method (not a NewManager side effect) so existing tests and
// non-swarm deployments keep their construction semantics.
func (m *Manager) AutoRestorePersistedTeams() []TeamSnapshot {
	var restored []TeamSnapshot
	for _, info := range SummarizePersistedTeams() {
		m.mu.Lock()
		_, active := m.teams[info.TeamID]
		m.mu.Unlock()
		if active {
			continue
		}
		switch v := m.checkOwnerLease(info.TeamID); v {
		case leaseAliveForeign, leaseDirMissing:
			debug.Log("swarm", "auto-restore skips %s (%s)", info.TeamID, v)
			continue
		case leaseLegacy, leaseDead, leaseOurs:
		}
		snap, err := m.RestoreTeam(info.TeamID)
		if err != nil {
			debug.Log("swarm", "auto-restore of %s failed: %v", info.TeamID, err)
			continue
		}
		restored = append(restored, snap)
		debug.Log("swarm", "auto-restored %s (%s): %d member(s)", info.TeamID, snap.Name, len(snap.Teammates))
	}
	return restored
}
