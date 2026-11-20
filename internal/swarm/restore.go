package swarm

import (
	"context"
	"fmt"
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
	m.mu.Lock()
	if _, active := m.teams[teamID]; active {
		m.mu.Unlock()
		return TeamSnapshot{}, fmt.Errorf("team %q is already active in this manager", teamID)
	}
	m.mu.Unlock()

	mf, err := m.loadManifest(teamID)
	if err != nil {
		return TeamSnapshot{}, fmt.Errorf("team %q has no persisted manifest: %w", teamID, err)
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
	if mf.OwnerSessionID == "" {
		// #5852 user ruling: no legacy concept. An unstamped manifest is
		// not adoptable - stranded-adoption applies only to teams that
		// HAD a legitimate owner (stamped foreign session, owner dead).
		return TeamSnapshot{}, fmt.Errorf("team %q has no session ownership stamp; refusing (abnormal data)", teamID)
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
