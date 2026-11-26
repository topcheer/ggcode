package swarm

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
)

// RestoreTeam is the authoritative board-entry path for a persisted team
// (#5829): it loads the manifest (P2), enforces the owner lease (P0), and
// rehydrates every member from its journal (P1) before starting the idle
// loop. A fresh CreateTeam never lands here - inheriting a foreign board is
// always an explicit decision.
func (m *Manager) RestoreTeam(teamID string) (TeamSnapshot, error) {
	if m.SessionNamespace() == "" {
		// teamDir() would resolve to the shared root and adopt a legacy
		// teams/<teamID>/ board (errNoConfirmedSession).
		return TeamSnapshot{}, errNoConfirmedSession
	}
	m.mu.Lock()
	if _, active := m.teams[teamID]; active {
		// Field symptom (user screenshot, burrow/android-port): a team
		// torn down mid-review leaves every member in ShuttingDown while
		// the team entry stays in the map - "already active" then blocks
		// team_restore forever. A team whose members are ALL shutting
		// down is a dead-state husk: evict it and restore fresh from the
		// manifest (journals intact). Any live (idle/working) member
		// keeps the refusal.
		team := m.teams[teamID]
		dead := true
		team.mu.Lock()
		for _, tm := range team.Teammates {
			tm.mu.Lock()
			if tm.Status != TeammateShuttingDown {
				dead = false
			}
			tm.mu.Unlock()
			if !dead {
				break
			}
		}
		team.mu.Unlock()
		if !dead {
			m.mu.Unlock()
			return TeamSnapshot{}, fmt.Errorf("team %q is already active in this manager", teamID)
		}
		debug.Log("swarm", "restore of %s: evicting dead-state husk (all members shutting_down)", teamID)
		for _, tm := range team.Teammates {
			if tm.cancel != nil {
				tm.cancel()
			}
			tm.mu.Lock()
			tm.Status = TeammateShuttingDown
			tm.mu.Unlock()
			delete(m.results, tm.ID)
		}
		delete(m.teams, teamID)
	}
	m.mu.Unlock()

	mf, err := m.loadManifest(teamID)
	if err != nil {
		return TeamSnapshot{}, fmt.Errorf("team %q has no persisted manifest: %w", teamID, err)
	}

	team := &Team{
		ID:        mf.TeamID,
		Name:      mf.Name,
		LeaderID:  mf.LeaderID,
		Teammates: make(map[string]*Teammate),
		CreatedAt: mf.CreatedAt,
	}
	team.Tasks = m.loadTeamBoard(teamID)

	m.mu.Lock()
	m.teams[teamID] = team
	m.mu.Unlock()

	for i := range mf.Teammates {
		mt := mf.Teammates[i]
		m.restoreTeammate(team, &mt)
	}

	// NOTE: do NOT rewrite the manifest from the loaded `mf` here -
	// restoreTeammate's upsertManifestTeammate calls have already rebound
	// sessionFile to the freshly allocated journals on disk; writing the
	// stale `mf` back would erase those session bindings and break
	// journal replay on the next restore (#5831 E2E finding).

	// Review B-1 (sa-1 full audit): the per-process teammate-ID counter
	// starts at 0, but restored teammates keep their original IDs
	// (tm-1..tm-N). Without raising the counter, the FIRST SpawnTeammate
	// after a restore would mint "tm-1" again - silently replacing the
	// restored member (goroutine still running, manifest entry
	// overwritten, journal binding lost). Lift nextTeamID above the
	// largest restored teammate ID.
	team.mu.Lock()
	for id := range team.Teammates {
		var n int
		if _, err := fmt.Sscanf(id, "tm-%d", &n); err == nil && n > m.nextTeamID {
			m.nextTeamID = n
		}
	}
	team.mu.Unlock()

	m.emit(Event{Type: "team_restored", TeamID: teamID, Timestamp: time.Now()})
	return team.snapshot(), nil
}

// RestoreTeamByName resolves a persisted team by display name (or ID) and
// restores it - the team_restore tool entry point.
func (m *Manager) RestoreTeamByName(name string) (TeamSnapshot, error) {
	mf, err := m.findTeamManifestByName(name)
	if err != nil {
		return TeamSnapshot{}, err
	}
	return m.RestoreTeam(mf.TeamID)
}

// restoreTeammate rehydrates one member: rebuild the Teammate record with
// its original ID (board task ownership survives), replay the journal into
// a fresh agent, then start the idle loop. A missing journal restores the
// member with fresh memory + WARN; an unclean journal footer WARNs as
// crashed.
func (m *Manager) restoreTeammate(team *Team, mt *ManifestTeammate) {
	ctx, cancel := context.WithCancel(m.rootCtx)
	tm := &Teammate{
		ID:           mt.ID,
		Name:         mt.Name,
		Color:        mt.Color,
		Status:       TeammateIdle,
		AllowedTools: sortedToolsCopy(mt.AllowedTools),
		Inbox:        make(chan MailMessage, m.cfg.InboxSize),
		CreatedAt:    time.Now(),
		LastActiveAt: mt.LastActiveAt,
		LastResult:   mt.LastResultSummary,
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
	}

	var ses *session.Session
	if !m.JournalsEnabled {
		// Journaling disabled (unit-test managers): members restore with
		// fresh memory; no journal files are read or created.
	} else if mt.SessionFile != "" {
		loaded, err := m.loadTeammateJournal(mt.SessionFile)
		switch {
		case err != nil:
			debug.Log("swarm", "WARN restore of %s/%s: journal %s missing/unreadable (%v); member restored with fresh memory", team.ID, mt.Name, mt.SessionFile, err)
		default:
			ses = loaded
			if footer := m.journalLastFooter(mt.SessionFile); footer == nil || footer.Status != "clean" {
				debug.Log("swarm", "WARN restore of %s/%s: journal has no clean shutdown footer (crashed or killed); replaying best-effort", team.ID, mt.Name)
			}
		}
	}

	team.mu.Lock()
	team.Teammates[tm.ID] = tm
	team.mu.Unlock()

	agent := m.constructTeammateAgent(team, tm)

	if ses != nil {
		seedAgentHistory(agent, ses.Messages)
		tm.mu.Lock()
		tm.journal = ses
		tm.mu.Unlock()
	} else if m.JournalsEnabled {
		// Fresh memory still gets a journal so the restored member keeps
		// persisting from here on.
		fresh := m.allocateTeammateJournal(team.ID, mt.Name)
		if fresh != nil {
			tm.mu.Lock()
			tm.journal = fresh
			tm.mu.Unlock()
		}
		if err := m.upsertManifestTeammate(team.ID, ManifestTeammate{
			ID:                tm.ID,
			Name:              tm.Name,
			Role:              mt.Role,
			Color:             tm.Color,
			AllowedTools:      sortedToolsCopy(mt.AllowedTools),
			SessionFile:       teammateJournalID(tm),
			LastActiveAt:      mt.LastActiveAt,
			LastResultSummary: mt.LastResultSummary,
		}); err != nil {
			debug.Log("swarm", "manifest session rebind for %s/%s failed: %v", team.ID, tm.Name, err)
		}
	}

	// Attach the journaler AFTER seeding so replayed history is not
	// re-appended to its own journal.
	if store, err := m.journalStore(); err == nil {
		attachTeammateJournaler(agent, store, teammateJournalSession(tm))
	}

	safego.Go("swarm.teammateLoop", func() {
		runTeammateLoop(ctx, tm, team, agent, m, m.emit, m.cfg.TeammateTimeout)
	})

	m.emit(Event{
		Type:         "teammate_spawned",
		TeamID:       team.ID,
		TeammateID:   tm.ID,
		TeammateName: tm.Name,
		Timestamp:    time.Now(),
	})
}

// AutoRestoreSessionTeams (#5860 rewrite): with the confirmed session id
// as the directory namespace, restoring is a plain listdir of
// swarm/teams/<sessionID>/ - every directory there IS ours by construction.
// No ownership stamps, no lease taxonomy, no foreign filtering. The
// summary line always lands in the debug ring (empty scan included).
func (m *Manager) AutoRestoreSessionTeams() []TeamSnapshot {
	var restored []TeamSnapshot
	sid := m.SessionNamespace()
	if sid == "" {
		// Fresh session before its first message: the namespace is not
		// minted yet and sessionTeamsDir() would be the SHARED root. Scanning
		// it restored every pre-#5860 legacy team (teams/<teamID>/) of every
		// workspace into this instance. Restore nothing; the session's own
		// teams (if any) are restored once it is confirmed.
		debug.Log("swarm", "AUTO-RESTORE SUMMARY: session= (unconfirmed) scanned=0 restored=0 (no confirmed session: shared root not scanned)")
		return nil
	}
	root := m.sessionTeamsDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		debug.Log("swarm", "AUTO-RESTORE SUMMARY: store=%s scanned=0 restored=0 (no namespace dir yet)", root)
		return nil
	}
	scanned := 0
	failed := 0
	ids := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		scanned++
		snap, err := m.RestoreTeam(e.Name())
		if err != nil {
			failed++
			debug.Log("swarm", "auto-restore of %s failed: %v", e.Name(), err)
			continue
		}
		restored = append(restored, snap)
		ids = append(ids, snap.ID)
	}
	debug.Log("swarm", "AUTO-RESTORE SUMMARY: session=%s store=%s scanned=%d restored=%d (%v) failed=%d",
		sid, root, scanned, len(restored), ids, failed)
	return restored
}
