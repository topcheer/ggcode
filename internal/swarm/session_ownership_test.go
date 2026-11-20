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

func TestLegacyTeamAdoptedOnExplicitRestore(t *testing.T) {
	root := t.TempDir()
	m1 := newLeaseTestManager(t, root) // no session (legacy creation)
	s := m1.CreateTeam("legacyteam", "leader-1")
	if _, err := m1.SpawnTeammate(s.ID, "bob", "", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	m2 := newLeaseTestManager(t, root)
	m2.SetOwnerSession("sess-X")
	// Legacy team is visible to every session...
	infos := m2.FilterTeamsForSession(SummarizePersistedTeamsForTest(t, root))
	found := false
	for _, info := range infos {
		if info.TeamID == s.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("legacy team should be visible for adoption")
	}
	// ...and an explicit restore adopts it (stamps the session).
	if _, err := m2.RestoreTeam(s.ID); err != nil {
		t.Fatalf("legacy adopt-restore failed: %v", err)
	}
	mf, err := m2.loadManifest(s.ID)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if mf.OwnerSessionID != "sess-X" {
		t.Fatalf("adoption did not stamp session: %q", mf.OwnerSessionID)
	}
	// After adoption, a third session can no longer see it.
	m3 := newLeaseTestManager(t, root)
	m3.SetOwnerSession("sess-Y")
	if err := m3.SessionOwnsTeam(s.ID); err == nil {
		t.Fatal("post-adoption, foreign session must be denied")
	}
}

// SummarizePersistedTeamsForTest re-scans a given board root for tests
// (the production helper always scans util.ConfigDir()).
func SummarizePersistedTeamsForTest(t *testing.T, boardRoot string) []PersistedTeamInfo {
	return summarizePersistedTeamsUnder(boardRoot)
}
