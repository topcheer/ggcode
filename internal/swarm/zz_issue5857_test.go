package swarm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// #5857 regression battery: pre-scoping teams (created before owner-session
// stamps existed) stuck in the store must stay reachable through an
// EXPLICIT restore, which is the one-shot migration channel: adopt (stamp
// the session) and, for scoped board roots, move the team directory out of
// the global store. The strict #5853 scoping surface (visibility,
// auto-restore) must not change. Every test below uses only pre-existing
// symbols so the battery compiles at the pre-fix parent for red pins.

// write5857Manifest hand-writes a persisted team manifest (the pre-scoping
// shape when ownerSession is empty: the omitempty tag drops the stamp).
func write5857Manifest(t *testing.T, root, teamID, name, ownerSession string) string {
	t.Helper()
	dir := filepath.Join(root, "swarm", "teams", teamID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mf := TeamManifest{
		TeamID:         teamID,
		Name:           name,
		LeaderID:       "lead-1",
		OwnerSessionID: ownerSession,
		CreatedAt:      time.Now().Round(0).UTC(),
		UpdatedAt:      time.Now().Round(0).UTC(),
		Teammates: []ManifestTeammate{
			{ID: "tm-1", Name: "worker-1", LastActiveAt: time.Now().Round(0).UTC()},
		},
	}
	data, err := json.MarshalIndent(&mf, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Explicit restore-by-ID of an unstamped team in the SAME store adopts it
// into the restoring session (previously: hard refusal as abnormal data).
func TestIssue5857_UnstampedTeamAdoptedOnRestoreByID(t *testing.T) {
	root := t.TempDir()
	m := newLeaseTestManager(t, root)
	write5857Manifest(t, root, "team-92530c", "et-ops", "")

	snap, err := m.RestoreTeam("team-92530c")
	if err != nil {
		t.Fatalf("explicit restore must migrate-adopt an unstamped pre-scoping team: %v", err)
	}
	if snap.ID != "team-92530c" || snap.Name != "et-ops" {
		t.Fatalf("unexpected snapshot %+v", snap)
	}
	mf, err := m.loadManifest("team-92530c")
	if err != nil {
		t.Fatal(err)
	}
	if mf.OwnerSessionID == "" {
		t.Fatal("migration adoption must stamp the adopting session into the manifest")
	}
	if err := m.SessionOwnsTeam("team-92530c"); err != nil {
		t.Fatalf("adopted team must be manageable from this session: %v", err)
	}
}

// Explicit restore-by-name of an unstamped team adopts it too (the
// team_restore tool path).
func TestIssue5857_UnstampedTeamAdoptedOnRestoreByName(t *testing.T) {
	root := t.TempDir()
	m := newLeaseTestManager(t, root)
	write5857Manifest(t, root, "team-92530c", "et-ops", "")

	snap, err := m.RestoreTeamByName("et-ops")
	if err != nil {
		t.Fatalf("restore by name must migrate-adopt an unstamped pre-scoping team: %v", err)
	}
	if snap.ID != "team-92530c" {
		t.Fatalf("unexpected snapshot %+v", snap)
	}
	mf, err := m.loadManifest("team-92530c")
	if err != nil {
		t.Fatal(err)
	}
	if mf.OwnerSessionID == "" {
		t.Fatal("migration adoption must stamp the adopting session")
	}
}

// A scoped board root that lacks the team pulls it out of the global store
// on explicit restore; the global copy is removed (single-store uniqueness).
func TestIssue5857_GlobalTeamMigratesIntoScopedBoardRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	global := filepath.Join(home, ".ggcode")
	cur := t.TempDir()
	m := newLeaseTestManager(t, cur)
	gdir := write5857Manifest(t, global, "team-92530c", "et-ops", "")

	if _, err := m.RestoreTeamByName("et-ops"); err != nil {
		t.Fatalf("explicit restore must migrate the global pre-scoping team into the scoped store: %v", err)
	}
	mf, err := m.loadManifest("team-92530c")
	if err != nil {
		t.Fatalf("migrated manifest missing from the scoped store: %v", err)
	}
	if mf.OwnerSessionID == "" {
		t.Fatal("migration must stamp the adopting session")
	}
	if _, err := os.Stat(filepath.Join(cur, "swarm", "teams", "team-92530c", "manifest.json")); err != nil {
		t.Fatalf("migrated manifest must live in the scoped store: %v", err)
	}
	if _, err := os.Stat(gdir); !os.IsNotExist(err) {
		t.Fatalf("global copy must be removed after migration (single-store uniqueness), stat err = %v", err)
	}
}

// Restore by bare team ID also migrates from the global store.
func TestIssue5857_GlobalTeamMigratesByTeamID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cur := t.TempDir()
	m := newLeaseTestManager(t, cur)
	write5857Manifest(t, filepath.Join(home, ".ggcode"), "team-4d2f01", "et-ops", "")

	if _, err := m.RestoreTeam("team-4d2f01"); err != nil {
		t.Fatalf("restore by ID must migrate the global pre-scoping team: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cur, "swarm", "teams", "team-4d2f01", "manifest.json")); err != nil {
		t.Fatalf("migrated manifest missing from the scoped store: %v", err)
	}
}

// The task board travels with the migration (the field loss that matters).
func TestIssue5857_MigrationCarriesTheTaskBoard(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	global := filepath.Join(home, ".ggcode")
	cur := t.TempDir()

	// Build the pre-scoping team with real persistence code, then rewind its
	// manifest to the pre-stamp shape.
	gm := newLeaseTestManager(t, global)
	snap := gm.CreateTeam("et-ops", "lead-1")
	board, err := gm.EnsureTaskManager(snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	created := board.Create("ship the fix", "d", "", nil)
	if err := gm.persistTeamBoardNow(snap.ID, board); err != nil {
		t.Fatal(err)
	}
	gmf, err := gm.loadManifest(snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	gmf.OwnerSessionID = ""
	if err := gm.writeManifest(snap.ID, gmf); err != nil {
		t.Fatal(err)
	}

	m := newLeaseTestManager(t, cur)
	if _, err := m.RestoreTeamByName("et-ops"); err != nil {
		t.Fatalf("migration restore failed: %v", err)
	}
	restored, err := m.EnsureTaskManager(snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := restored.Get(created.ID); !ok {
		t.Fatalf("task %s must survive the migration on the board", created.ID)
	}
}

// A live foreign instance holding the global copy blocks migration and no
// data is mutated (PID 1 answers signal 0 via EPERM, so the lease is
// provably alive on the same host).
func TestIssue5857_LiveForeignLeaseStillRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	global := filepath.Join(home, ".ggcode")
	cur := t.TempDir()
	m := newLeaseTestManager(t, cur)
	gdir := write5857Manifest(t, global, "team-live9", "et-ops", "")
	if err := writeOwnerLeaseFile(filepath.Join(gdir, "owner.json"), &ownerLease{
		BootID:    "foreign-boot-5857",
		PID:       1,
		Hostname:  m.hostname,
		Heartbeat: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.RestoreTeamByName("et-ops"); err == nil {
		t.Fatal("a live instance holding the board must never be migrated or restored")
	}
	if _, serr := os.Stat(gdir); serr != nil {
		t.Fatalf("global copy must stay in place on refused migration: %v", serr)
	}
	mf, err := readManifestFile(filepath.Join(gdir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if mf.OwnerSessionID != "" {
		t.Fatal("refused migration must not stamp the manifest")
	}
}

// Same-store variant: an unstamped team held by a live foreign instance is
// refused by the explicit restore as well.
func TestIssue5857_SameStoreLiveForeignLeaseRefused(t *testing.T) {
	root := t.TempDir()
	m := newLeaseTestManager(t, root)
	dir := write5857Manifest(t, root, "team-live8", "et-ops", "")
	if err := writeOwnerLeaseFile(filepath.Join(dir, "owner.json"), &ownerLease{
		BootID:    "foreign-boot-5857",
		PID:       1,
		Hostname:  m.hostname,
		Heartbeat: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.RestoreTeamByName("et-ops"); err == nil {
		t.Fatal("the live-instance guard must hold for unstamped teams too")
	}
	mf, err := readManifestFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if mf.OwnerSessionID != "" {
		t.Fatal("refused restore must not adopt-stamp the manifest")
	}
}

// When the current store already has the team, restore uses it and the
// stale global copy is untouched (no migration, no stamping).
func TestIssue5857_CurrentStoreTeamWinsOverGlobalStale(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	global := filepath.Join(home, ".ggcode")
	cur := t.TempDir()
	m := newLeaseTestManager(t, cur)
	own := "test-sess-" + t.Name()
	write5857Manifest(t, cur, "team-dup7", "et-ops", own)
	gdir := write5857Manifest(t, global, "team-dup7", "et-ops-stale", "")

	snap, err := m.RestoreTeamByName("et-ops")
	if err != nil {
		t.Fatalf("current-store team must restore normally: %v", err)
	}
	if snap.Name != "et-ops" {
		t.Fatalf("must restore the current-store team, got %q", snap.Name)
	}
	if _, serr := os.Stat(gdir); serr != nil {
		t.Fatalf("global stale copy must stay untouched: %v", serr)
	}
	if mf, _ := readManifestFile(filepath.Join(gdir, "manifest.json")); mf.OwnerSessionID != "" {
		t.Fatal("global stale copy must stay unstamped")
	}
}

// A foreign-STAMPED team in the global store is another session data, not
// migration material: the migration channel must not become an adoption
// loophole around #5853 visibility.
func TestIssue5857_ForeignStampedGlobalTeamNotMigratable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	global := filepath.Join(home, ".ggcode")
	cur := t.TempDir()
	m := newLeaseTestManager(t, cur)
	gdir := write5857Manifest(t, global, "team-sec1", "secret-squad", "another-session-9")

	if _, err := m.RestoreTeamByName("secret-squad"); err == nil {
		t.Fatal("another session team must not be adoptable via the migration channel")
	}
	if _, serr := os.Stat(gdir); serr != nil {
		t.Fatalf("global copy must stay untouched: %v", serr)
	}
	if _, serr := os.Stat(filepath.Join(cur, "swarm", "teams", "team-sec1")); !os.IsNotExist(serr) {
		t.Fatalf("nothing may land in the scoped store, stat err = %v", serr)
	}
}

// A naked manager (auto-minted session, no explicit SetOwnerSession) also
// adopts unstamped teams - the production CLI bind is never required for
// the migration channel to work.
func TestIssue5857_AutoSessionAdoptsUnstampedTeam(t *testing.T) {
	root := t.TempDir()
	m := NewManager(config.SwarmConfig{}, nil, nil, nil)
	m.BoardRoot = root
	write5857Manifest(t, root, "team-auto1", "et-ops", "")

	if _, err := m.RestoreTeam("team-auto1"); err != nil {
		t.Fatalf("explicit restore must adopt under the auto-minted session: %v", err)
	}
	mf, err := m.loadManifest("team-auto1")
	if err != nil {
		t.Fatal(err)
	}
	if mf.OwnerSessionID == "" {
		t.Fatal("adoption must stamp the auto-minted session")
	}
}
