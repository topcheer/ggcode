package swarm

import (
	"strings"
	"testing"
	"time"
)

// #5837: teams belong to their creating SESSION. Cross-session isolation:
// a foreign-session team is invisible and unrestorable; a legacy team
// (no ownerSessionID) is adoptable via explicit restore; an own team
// restores freely and auto-restores on session resume.

func TestSessionOwnershipIsolation(t *testing.T) {
	root := t.TempDir()
	mine := newLeaseTestManager(t, root)
	mine.SetOwnerSession("sess-A")
	other := newLeaseTestManager(t, root)
	other.SetOwnerSession("sess-B")

	s := mine.CreateTeam("team-a", "leader-1")
	if _, err := mine.SpawnTeammate(s.ID, "alice", "", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	// A REAL live foreign owner runs under a DIFFERENT PID (same-process
	// managers share the test PID, which the self-PID short-circuit in
	// alive() deliberately treats as inheritable - the exec-restart case).
	// Stamp PID 1 (init, always alive) with a fresh heartbeat.
	live := ownerLease{BootID: "live-foreign-boot", PID: 1, Hostname: mine.hostname, Heartbeat: time.Now()}
	if err := writeOwnerLeaseFile(mine.ownerLeasePath(s.ID), &live); err != nil {
		t.Fatalf("write live lease: %v", err)
	}

	// Foreign session: restore must refuse with the session-isolation error.
	if _, err := other.RestoreTeam(s.ID); err == nil || !strings.Contains(err.Error(), "another session") {
		t.Fatalf("foreign restore should refuse with session error, got: %v", err)
	}
	// Foreign session: visibility filter hides the team.
	infos := other.FilterTeamsForSession(SummarizePersistedTeamsForTest(t, root))
	for _, info := range infos {
		if info.TeamID == s.ID {
			t.Fatal("foreign session must not see team-a")
		}
	}
	// Owning session resuming (a NEW manager bound to the same session ID,
	// i.e. the post-restart process): restore succeeds and the manifest
	// carries the session.
	// Before the own-session resume, the live foreign process exits too
	// (the real restart): stamp the dead-lease shape it leaves behind.
	dead := ownerLease{BootID: "live-foreign-boot", PID: 4194304, Hostname: mine.hostname, Heartbeat: time.Now().Add(-4 * ownerLeaseTTL)}
	if err := writeOwnerLeaseFile(mine.ownerLeasePath(s.ID), &dead); err != nil {
		t.Fatalf("write dead lease: %v", err)
	}

	mine2 := newLeaseTestManager(t, root)
	mine2.SetOwnerSession("sess-A")
	if _, err := mine2.RestoreTeam(s.ID); err != nil {
		t.Fatalf("own-session restore failed: %v", err)
	}
	mf, err := mine2.loadManifest(s.ID)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if mf.OwnerSessionID != "sess-A" {
		t.Fatalf("manifest ownerSessionID = %q, want sess-A", mf.OwnerSessionID)
	}
}

// #5852 user ruling: NO legacy concept. An unstamped manifest is foreign
// by definition: invisible and unrestorable. Normal flow always stamps.
func TestUnstampedManifestIsForeign(t *testing.T) {
	root := t.TempDir()
	m1 := newLeaseTestManager(t, root)
	m1.SetOwnerSession("sess-maker")
	s := m1.CreateTeam("unstamped", "leader-1")
	if _, err := m1.SpawnTeammate(s.ID, "alice", "", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := m1.updateManifest(s.ID, func(mf *TeamManifest) { mf.OwnerSessionID = "" }); err != nil {
		t.Fatalf("strip: %v", err)
	}
	m2 := newLeaseTestManager(t, root)
	m2.SetOwnerSession("sess-other")
	if err := m2.SessionOwnsTeam(s.ID); err == nil {
		t.Fatal("unstamped manifest must be foreign (refused)")
	}
	for _, info := range SummarizeVisibleTeams("sess-other") {
		if info.TeamID == s.ID {
			t.Fatal("unstamped manifest surfaced to a foreign session")
		}
	}
	// #5857 supersedes the old hard refusal: the strict surface guards
	// above stay, but an EXPLICIT restore is the one-shot migration
	// channel for pre-scoping teams - it adopts the team into the
	// restoring session and stamps it.
	snap, err := m2.RestoreTeam(s.ID)
	if err != nil {
		t.Fatalf("explicit restore must migrate-adopt an unstamped pre-scoping team: %v", err)
	}
	if snap.ID != s.ID {
		t.Fatalf("unexpected snapshot %+v", snap)
	}
	mf, err := m2.loadManifest(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mf.OwnerSessionID != "sess-other" {
		t.Fatalf("adoption must stamp the restoring session, got %q", mf.OwnerSessionID)
	}
	if err := m2.SessionOwnsTeam(s.ID); err != nil {
		t.Fatalf("adopted team must be manageable from the adopting session: %v", err)
	}
}

// Review B-5: #5839 changed the lease liveness contract (same-host dead
// PID is dead REGARDLESS of heartbeat freshness) without a dedicated
// test. Pin it.
func TestSameHostDeadPIDFreshHeartbeatIsDead(t *testing.T) {
	root := t.TempDir()
	m1 := newLeaseTestManager(t, root)
	m2 := newLeaseTestManager(t, root)

	s := m1.CreateTeam("freshttl", "leader-1")
	if _, err := m1.SpawnTeammate(s.ID, "eve", "", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	dead := ownerLease{
		BootID:    "fresh-but-dead",
		PID:       4194304,
		Hostname:  m1.hostname,
		Heartbeat: time.Now(),
	}
	if err := writeOwnerLeaseFile(m1.ownerLeasePath(s.ID), &dead); err != nil {
		t.Fatalf("write lease: %v", err)
	}
	if v := m2.checkOwnerLease(s.ID); v != leaseDead {
		t.Fatalf("same-host dead PID with fresh heartbeat must be leaseDead, got %v", v)
	}
}

// SummarizePersistedTeamsForTest re-scans a given board root for tests
// (the production helper always scans util.ConfigDir()).
func SummarizePersistedTeamsForTest(t *testing.T, boardRoot string) []PersistedTeamInfo {
	return summarizePersistedTeamsUnder(boardRoot)
}
