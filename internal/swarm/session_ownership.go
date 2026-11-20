package swarm

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
)

// Session-scoped ownership for persistent teams (#5837).
//
// Design intent (user direction 2026-11-20): a persistent team belongs to
// the SESSION that created it. The same session resuming must get its
// teams rebuilt immediately and automatically; a DIFFERENT session in the
// same workspace must not even see them (cross-session isolation). The
// PID/hostname lease machinery stays as a machine-level liveness guard,
// but ownership itself is decided by ownerSessionID in the manifest.

// SetOwnerSession binds this manager to a session ID. Called when the
// real session ID is adopted (REPL loadSession/createSession via
// Manager.BindSessionAndRestore); teams created after this stamp their
// manifest, and restore/visibility checks filter by it. An empty owner
// session (unset) keeps legacy behavior: everything is visible and
// restorable (pre-#5837 manifests, tests).
func (m *Manager) SetOwnerSession(sessionID string) {
	m.mu.Lock()
	m.ownerSessionID = sessionID
	m.mu.Unlock()
}

// ownerSession returns the bound session ID under m.mu.
func (m *Manager) ownerSession() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ownerSessionID
}

// BindSessionAndRestore binds this manager AND the process prompt surface
// to sessionID, then auto-restores that session's teams. This is the
// adoption-time hook (#5837 residual): the bootstrap-time
// ag.SessionID() is empty because the session is only created or loaded
// inside the REPL run loop, so the bind must happen when the real session
// ID becomes known. Re-binding on a mid-run session switch re-scopes
// ownership; teams already active in this manager stay active.
func (m *Manager) BindSessionAndRestore(sessionID string) []TeamSnapshot {
	if sessionID == "" {
		return nil
	}
	m.SetOwnerSession(sessionID)
	SetProcessOwnerSession(sessionID)
	return m.AutoRestoreSessionTeams()
}

type sessionOwnership int

const (
	ownershipOurs    sessionOwnership = iota // owned by this session (auto-restore + manage)
	ownershipLegacy                          // no ownerSessionID on disk yet (adoptable via explicit restore)
	ownershipForeign                         // owned by a different session: invisible, unrestorable
)

// sessionOwnershipFor classifies a persisted team relative to this
// manager's session. An unset manager session treats everything as ours
// (legacy/tests mode).
func (m *Manager) sessionOwnershipFor(mf *TeamManifest) sessionOwnership {
	m.mu.Lock()
	own := m.ownerSessionID
	m.mu.Unlock()
	if own == "" {
		return ownershipOurs
	}
	switch {
	case mf.OwnerSessionID == "":
		return ownershipLegacy
	case mf.OwnerSessionID == own:
		return ownershipOurs
	default:
		return ownershipForeign
	}
}

// FilterTeamsForSession filters a set of manifest summaries down to what
// this session may see: its own teams plus legacy (unclaimed) teams.
func (m *Manager) FilterTeamsForSession(infos []PersistedTeamInfo) []PersistedTeamInfo {
	out := make([]PersistedTeamInfo, 0, len(infos))
	for _, info := range infos {
		mf, err := readManifestFile(m.manifestPath(info.TeamID))
		if err != nil {
			continue
		}
		switch m.sessionOwnershipFor(mf) {
		case ownershipOurs, ownershipLegacy:
			out = append(out, info)
		case ownershipForeign:
			debug.Log("swarm", "team %s hidden from session %s (owned by %s)", info.TeamID, m.ownerSession(), mf.OwnerSessionID)
		}
	}
	return out
}

// AutoRestoreSessionTeams rebuilds THIS session's persisted teams
// immediately (called from process bootstrap after SetOwnerSession):
// ownership-ours teams are restored right away; legacy teams are NOT
// auto-restored (explicit team_restore adopts them, stamping the session).
// Lease liveness still guards machine-level double-restore.
func (m *Manager) AutoRestoreSessionTeams() []TeamSnapshot {
	var restored []TeamSnapshot
	// Scan THIS manager's board root (m.boardRoot()), not the process
	// global util.ConfigDir(): a Manager with a BoardRoot override (tests,
	// custom layouts) would otherwise silently find nothing to restore.
	// sessionOwnershipFor below applies the per-manager session filter.
	for _, info := range summarizePersistedTeamsUnder(m.boardRoot()) {
		m.mu.Lock()
		_, active := m.teams[info.TeamID]
		m.mu.Unlock()
		if active {
			continue
		}
		mf, err := m.loadManifest(info.TeamID)
		if err != nil {
			continue
		}
		if m.sessionOwnershipFor(mf) != ownershipOurs {
			continue
		}
		switch v := m.checkOwnerLease(info.TeamID); v {
		case leaseAliveForeign, leaseDirMissing:
			debug.Log("swarm", "session auto-restore skips %s (lease %s)", info.TeamID, v)
			continue
		case leaseLegacy, leaseDead, leaseOurs:
		}
		snap, err := m.RestoreTeam(info.TeamID)
		if err != nil {
			debug.Log("swarm", "session auto-restore of %s failed: %v", info.TeamID, err)
			continue
		}
		restored = append(restored, snap)
	}
	return restored
}

// stampOwnerSessionOnAdopt writes this session's ID into a legacy
// manifest at explicit-restore time (one-time adoption).
func (m *Manager) stampOwnerSessionOnAdopt(teamID string) {
	own := m.ownerSession()
	if own == "" {
		return
	}
	err := m.updateManifest(teamID, func(mf *TeamManifest) {
		if mf.OwnerSessionID == "" {
			mf.OwnerSessionID = own
		}
	})
	if err != nil {
		debug.Log("swarm", "session adoption stamp for %s failed: %v", teamID, err)
	}
}

// SessionOwnsTeam reports whether teamID is manageable from this session
// (ours or legacy); used by tool-layer guards.
func (m *Manager) SessionOwnsTeam(teamID string) error {
	mf, err := m.loadManifest(teamID)
	if err != nil {
		return err
	}
	if m.sessionOwnershipFor(mf) == ownershipForeign {
		return fmt.Errorf("team %q belongs to another session; it is not visible or loadable from this session", teamID)
	}
	return nil
}
