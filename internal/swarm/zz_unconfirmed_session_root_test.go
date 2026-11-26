package swarm

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// seedSharedRoot builds the on-disk shape of a long-lived machine:
//
//	<root>/swarm/teams/<teamID>/manifest.json              <- pre-#5860 legacy
//	<root>/swarm/teams/<otherSession>/<teamID>/manifest.json <- another workspace's session
//
// The legacy directory is produced by a real CreateTeam (namespace
// "sess-legacy-owner") and then moved up one level, which is exactly what
// older binaries left behind. Returns the legacy team id and name.
func seedSharedRoot(t *testing.T, root string) (legacyID, legacyName string) {
	t.Helper()
	owner := nsManager(t, root, "sess-legacy-owner")
	legacy := owner.CreateTeam("et-ops", "leader-1")
	legacyID, legacyName = legacy.ID, legacy.Name
	teamsDir := filepath.Join(root, "swarm", "teams")
	if err := os.Rename(filepath.Join(teamsDir, "sess-legacy-owner", legacyID), filepath.Join(teamsDir, legacyID)); err != nil {
		t.Fatalf("move team to the legacy layout: %v", err)
	}
	_ = os.Remove(filepath.Join(teamsDir, "sess-legacy-owner"))

	foreign := nsManager(t, root, "sess-other-workspace")
	foreign.CreateTeam("docs-audit", "leader-2")

	// Both layouts are on disk.
	if _, err := os.Stat(filepath.Join(teamsDir, legacyID, "manifest.json")); err != nil {
		t.Fatalf("legacy manifest missing: %v", err)
	}
	entries, _ := os.ReadDir(teamsDir)
	if len(entries) != 2 {
		t.Fatalf("fixture: expected legacy dir + one session namespace, got %d entries", len(entries))
	}
	return legacyID, legacyName
}

// Field report 2026-11-26: a new instance started in a parent directory came
// up with five foreign teams. Root cause: a fresh session has no confirmed id
// until its first message, sessionTeamsDir() then collapses onto the SHARED
// root, and AutoRestoreSessionTeams listdir'd it - restoring every pre-#5860
// legacy team of every workspace (and re-persisting them under the new id).
// Without a namespace nothing may be listed, restored or adopted.
func TestUnconfirmedSessionNeverScansSharedRoot(t *testing.T) {
	root := t.TempDir()
	legacyID, legacyName := seedSharedRoot(t, root)

	m := nsManager(t, root, "") // fresh instance, first message not sent yet
	if restored := m.AutoRestoreSessionTeams(); len(restored) != 0 {
		t.Fatalf("unconfirmed session restored %d foreign team(s): %v", len(restored), restored)
	}
	if live := m.ListTeams(); len(live) != 0 {
		t.Fatalf("unconfirmed session must have no live teams, got %v", live)
	}
	if _, err := m.RestoreTeam(legacyID); !errors.Is(err, errNoConfirmedSession) {
		t.Fatalf("RestoreTeam without a namespace: got %v, want errNoConfirmedSession", err)
	}
	if _, err := m.RestoreTeamByName(legacyName); !errors.Is(err, errNoConfirmedSession) {
		t.Fatalf("RestoreTeamByName without a namespace: got %v, want errNoConfirmedSession", err)
	}
	if _, err := m.findTeamManifestByName(legacyName); !errors.Is(err, errNoConfirmedSession) {
		t.Fatalf("findTeamManifestByName without a namespace: got %v, want errNoConfirmedSession", err)
	}
	// Nothing was written into the shared root by the reads.
	entries, _ := os.ReadDir(filepath.Join(root, "swarm", "teams"))
	if len(entries) != 2 {
		t.Fatalf("shared root mutated by an unconfirmed session: %d entries", len(entries))
	}
}

// Once the session is confirmed the namespace is real, but it is a NEW
// session: it still must not inherit the legacy teams or another session's
// teams. Its own teams round-trip under its own namespace.
func TestConfirmedFreshSessionDoesNotAdoptLegacyTeams(t *testing.T) {
	root := t.TempDir()
	legacyID, _ := seedSharedRoot(t, root)

	m := nsManager(t, root, "")
	m.ConfirmSession("sess-NEW")
	if restored := m.AutoRestoreSessionTeams(); len(restored) != 0 {
		t.Fatalf("fresh session adopted %d team(s) on confirmation: %v", len(restored), restored)
	}
	if _, err := m.RestoreTeam(legacyID); err == nil {
		t.Fatal("a confirmed session must not be able to address a legacy teams/<teamID>/ board")
	}
	snap := m.CreateTeam("mine", "leader-9")
	if _, err := os.Stat(filepath.Join(root, "swarm", "teams", "sess-NEW", snap.ID, "manifest.json")); err != nil {
		t.Fatalf("own team must persist under its own namespace: %v", err)
	}
	again := nsManager(t, root, "sess-NEW")
	got := again.AutoRestoreSessionTeams()
	if len(got) != 1 || got[0].ID != snap.ID {
		t.Fatalf("restart of the same session must restore exactly its own team, got %v", got)
	}
	// The shared root still holds exactly: legacy dir, other session, new session.
	entries, _ := os.ReadDir(filepath.Join(root, "swarm", "teams"))
	if len(entries) != 3 {
		t.Fatalf("unexpected shared root layout: %d entries", len(entries))
	}
}
