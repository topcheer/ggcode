package swarm

import (
	"context"
	"fmt"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/util"
)

// RestoreTeam is the authoritative board-entry path for a persisted team
// (#5829): it loads the manifest (P2), enforces the owner lease (P0), and
// rehydrates every member from its journal (P1) before starting the idle
// loop. A fresh CreateTeam never lands here - inheriting a foreign board is
// always an explicit decision.
func (m *Manager) RestoreTeam(teamID string) (TeamSnapshot, error) {
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
		// #5857: pre-scoping teams stuck in the global store. When this
		// manager runs a scoped board root that lacks the team but the
		// global store holds it, an explicit restore migrates the team
		// directory here first (one-shot; the global copy is removed for
		// single-store uniqueness).
		if m.migrateGlobalTeam(teamID) {
			mf, err = m.loadManifest(teamID)
		}
		if err != nil {
			return TeamSnapshot{}, fmt.Errorf("team %q has no persisted manifest: %w", teamID, err)
		}
	}

	// #5837: session-scoped ownership gates the restore itself - a team
	// owned by a different session is never loadable from here; a legacy
	// (unclaimed) team is adopted at explicit-restore time.
	// #5847 (option B, autopilot ruling): a foreign session's team whose
	// owner lease is PROVABLY dead (same-host dead PID, or heartbeat long
	// expired on a foreign host) is adoptable via this explicit restore -
	// otherwise a crashed instance's teams are stranded forever (the
	// user's "dead instance lease blocks restore" symptom). A LIVE owner's
	// foreign team stays refused.
	// #5857 (follow-up to the #5853 strict ruling): an unstamped manifest
	// is a PRE-SCOPING team - real data created before owner stamps
	// existed, not abnormal data. It stays invisible to the session
	// surface and to auto-restore (strict equality there is unchanged),
	// but an EXPLICIT restore is the one-shot migration channel: adopt
	// the team into this session. The owner lease still gates
	// machine-level double-restore below - a live instance holding the
	// board keeps the refusal.
	if mf.OwnerSessionID == "" {
		switch m.checkOwnerLease(teamID) {
		case leaseAliveForeign:
			debug.Log("swarm", "WARN restore of %s refused: unstamped pre-scoping team held by a live instance", teamID)
			return TeamSnapshot{}, fmt.Errorf("team %q predates session stamps and its board is held by a live instance; stop that instance before migrating it", teamID)
		default:
			debug.Log("swarm", "restore of %s: unstamped pre-scoping team; explicit restore adopts it into this session (#5857 one-shot migration)", teamID)
			m.forceAdoptOwnerSession(teamID)
			mf.OwnerSessionID = m.boundOwnerSession()
		}
	}
	if m.sessionOwnershipFor(mf) == ownershipForeign {
		switch v := m.checkOwnerLease(teamID); v {
		case leaseDead, leaseLegacy:
			debug.Log("swarm", "restore of %s: owner session gone (lease %s); adopting the stranded team into this session", teamID, v)
			m.forceAdoptOwnerSession(teamID)
		default:
			return TeamSnapshot{}, fmt.Errorf("team %q belongs to another session; it is not visible or loadable from this session", teamID)
		}
	}

	switch v := m.checkOwnerLease(teamID); v {
	case leaseAliveForeign:
		debug.Log("swarm", "WARN restore of %s refused: board owned by a LIVE instance (lease alive); stop the other instance or use a different team", teamID)
		return TeamSnapshot{}, fmt.Errorf("team %q is owned by a live instance (owner lease alive); restore refused", teamID)
	case leaseLegacy:
		debug.Log("swarm", "WARN restore of %s: directory has no owner.json (legacy pre-persistent-teams board); treating as dead owner and inheriting", teamID)
	case leaseDead:
		debug.Log("swarm", "restore of %s: owner lease dead, inheriting and re-stamping", teamID)
	case leaseOurs, leaseDirMissing:
		// Same-boot resume or missing dir (board empty, journals may exist).
	}
	if err := m.stampOwnerLease(teamID); err != nil {
		debug.Log("swarm", "owner re-stamp for %s failed: %v", teamID, err)
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
		// #5857: the name may only resolve in the global store (a
		// pre-scoping team this manager's scoped store has never seen).
		// An explicit restore is then a migration request; a blocked
		// migration (live instance / another session's stamp) surfaces a
		// pointed error instead of a bare "no match".
		gmf, gerr := findTeamManifestUnder(util.ConfigDir(), name)
		if gerr == nil {
			if !m.migrateGlobalTeam(gmf.TeamID) {
				return TeamSnapshot{}, fmt.Errorf("team %q exists in the global store but cannot be migrated into this store (a live instance or another session's ownership blocks it)", gmf.TeamID)
			}
			mf, err = m.loadManifest(gmf.TeamID)
		}
		if err != nil {
			return TeamSnapshot{}, err
		}
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
