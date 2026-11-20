package swarm

import (
	"strings"
	"testing"
	"time"
)

// Symptom pins for the user's two field reproductions of the
// persistent-teams failure (goal: fix both, prove with these tests).

// Symptom 2 - "dead instance's lease blocks restore": the owner process is
// PROVABLY dead (same-host dead PID, or foreign-host heartbeat long
// expired), yet a persisted team owned by that dead session is refused for
// every other session forever. Expected: a provably-dead owner's team is
// adoptable via explicit RestoreTeam (session rebind + lease re-stamp).
func TestDeadInstanceLeaseDoesNotBlockRestore(t *testing.T) {
	root := t.TempDir()
	deadOwner := newLeaseTestManager(t, root)
	deadOwner.SetOwnerSession("sess-dead")

	s := deadOwner.CreateTeam("orphaned", "leader-1")
	if _, err := deadOwner.SpawnTeammate(s.ID, "alice", "", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	// Simulate the dead instance on disk: foreign session, same host,
	// provably-dead PID, stale heartbeat (well past TTL).
	deadLease := ownerLease{
		BootID:    "dead-boot",
		PID:       4194304,
		Hostname:  deadOwner.hostname,
		Heartbeat: time.Now().Add(-4 * ownerLeaseTTL),
	}
	if err := writeOwnerLeaseFile(deadOwner.ownerLeasePath(s.ID), &deadLease); err != nil {
		t.Fatalf("write dead lease: %v", err)
	}

	// A fresh session (e.g. the user restarted WITHOUT --resume after the
	// crash) must be able to adopt the dead instance's team explicitly.
	heir := newLeaseTestManager(t, root)
	heir.SetOwnerSession("sess-heir")
	snap, err := heir.RestoreTeam(s.ID)
	if err != nil {
		t.Fatalf("dead-owner team must be adoptable via explicit restore (user symptom: lease blocks restore): %v", err)
	}
	if snap.ID != s.ID || len(snap.Teammates) != 1 {
		t.Fatalf("adopted snapshot mismatch: %+v", snap)
	}
	// Adoption rebinds ownership to the adopting session.
	mf, err := heir.loadManifest(s.ID)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if mf.OwnerSessionID != "sess-heir" {
		t.Fatalf("adoption must rebind ownerSessionID to the adopting session, got %q", mf.OwnerSessionID)
	}
}

// Symptom 1 - "team cannot be recovered after restart" (same session,
// fresh process): AutoRestoreSessionTeams must rebuild the session's team
// with zero user action, and the session ownership classification must
// stay "ours" across the restart boundary.
func TestRestoreAfterRestartRecoversTeams(t *testing.T) {
	root := t.TempDir()
	first := newLeaseTestManager(t, root)
	first.SetOwnerSession("sess-resume")

	s := first.CreateTeam("resumable", "leader-1")
	if _, err := first.SpawnTeammate(s.ID, "bob", "", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	// Simulate the restart: the first process is GONE (in-process tests
	// share one PID, so we stamp the dead-instance lease shape the real
	// restart leaves behind: same host, dead PID, stale heartbeat).
	deadLease := ownerLease{
		BootID:    "restarted-away",
		PID:       4194304,
		Hostname:  first.hostname,
		Heartbeat: time.Now().Add(-4 * ownerLeaseTTL),
	}
	if err := writeOwnerLeaseFile(first.ownerLeasePath(s.ID), &deadLease); err != nil {
		t.Fatalf("write dead lease: %v", err)
	}

	// The restarted process: same session ID, brand-new manager.
	second := newLeaseTestManager(t, root)
	second.SetOwnerSession("sess-resume")
	restored := second.AutoRestoreSessionTeams()
	if len(restored) == 0 {
		t.Fatal("same-session restart must auto-restore the team with no user action")
	}
	if restored[0].ID != s.ID || len(restored[0].Teammates) != 1 {
		t.Fatalf("auto-restored snapshot mismatch: %+v", restored[0])
	}
}

// Cross-session isolation must SURVIVE the adoption change: a LIVE owner's
// foreign team stays refused even under explicit restore.
func TestLiveForeignSessionStillRefusedAfterAdoptionChange(t *testing.T) {
	root := t.TempDir()
	owner := newLeaseTestManager(t, root)
	owner.SetOwnerSession("sess-live")

	s := owner.CreateTeam("guarded", "leader-1")
	if _, err := owner.SpawnTeammate(s.ID, "carol", "", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	// Real live foreign owner: different PID (init), fresh heartbeat -
	// in-process managers share the test PID, which alive() treats as the
	// exec-restart self case.
	live := ownerLease{BootID: "live-foreign-boot", PID: 1, Hostname: owner.hostname, Heartbeat: time.Now()}
	if err := writeOwnerLeaseFile(owner.ownerLeasePath(s.ID), &live); err != nil {
		t.Fatalf("write live lease: %v", err)
	}

	other := newLeaseTestManager(t, root)
	other.SetOwnerSession("sess-other")
	_, err := other.RestoreTeam(s.ID)
	if err == nil || !strings.Contains(err.Error(), "another session") {
		t.Fatalf("LIVE owner's foreign team must stay refused, got: %v", err)
	}
}
