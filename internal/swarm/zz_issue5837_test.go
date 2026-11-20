package swarm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #5837 residual battery (takeOwnership seat, on top of owner fix
// 85cbc3ad2 "bind persistent teams to their creating SESSION").
//
// Residuals pinned here:
//   R0/R1 - AutoRestoreSessionTeams must scan the MANAGER's board root
//           (m.boardRoot()), not the process-global util.ConfigDir(); the
//           bootstrap-time bind also saw ag.SessionID()=="" because the
//           session ID is only adopted inside the REPL run loop
//           (loadSession/createSession -> model.SetSession), so session
//           binding now happens at adoption time via
//           Manager.BindSessionAndRestore.
//   R2    - findTeamManifestByName must prefer this session's (or legacy)
//           teams over foreign-session teams when names collide.
//   R3    - team_restore of an already-active own team is idempotent
//           (auto-restored teams would otherwise make the hinted
//           team_restore call fail with "already active").
//   Design point 7 of the issue - teammate_spawn/teammate_shutdown
//           manifest upsert/remove must preserve ownerSessionID across
//           the member lifecycle, and a foreign session must stay locked
//           out at every stage.

// T1 (R1 red at owner fix head): auto-restore must use the manager's
// BoardRoot - with the pre-residual code the scan went to
// util.ConfigDir() and a BoardRoot-bound manager restored nothing.
func TestZZ5837AutoRestoreUsesManagerBoardRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate util.ConfigDir() (pre-fix scan path)
	root := t.TempDir()
	m1 := newLeaseTestManager(t, root)
	m1.SetOwnerSession("sess-A")
	s := m1.CreateTeam("team-a", "leader-1")
	if _, err := m1.SpawnTeammate(s.ID, "alice", "", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	m2 := newLeaseTestManager(t, root)
	m2.SetOwnerSession("sess-A")
	restored := m2.AutoRestoreSessionTeams()
	if len(restored) != 1 {
		t.Fatalf("auto-restore under manager BoardRoot restored %d team(s), want 1", len(restored))
	}
	snap, ok := m2.GetTeam(s.ID)
	if !ok {
		t.Fatal("own-session team not active after auto-restore (teammate_list unavailable at the first moment)")
	}
	if len(snap.Teammates) != 1 {
		t.Fatalf("restored team has %d teammate(s), want 1", len(snap.Teammates))
	}
	mf, err := m2.loadManifest(s.ID)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if mf.OwnerSessionID != "sess-A" {
		t.Fatalf("manifest ownerSessionID after restore = %q, want sess-A", mf.OwnerSessionID)
	}
}

// T2: a session resume auto-restores ONLY that session's teams; foreign
// teams are neither activated nor adopted.
func TestZZ5837AutoRestoreSessionIsolation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	ma := newLeaseTestManager(t, root)
	ma.SetOwnerSession("sess-A")
	sa := ma.CreateTeam("alpha", "leader-1")
	mb := newLeaseTestManager(t, root)
	mb.SetOwnerSession("sess-B")
	sb := mb.CreateTeam("beta", "leader-1")

	resumed := newLeaseTestManager(t, root)
	resumed.SetOwnerSession("sess-A")
	for _, snap := range resumed.AutoRestoreSessionTeams() {
		if snap.ID == sb.ID {
			t.Fatalf("auto-restore adopted foreign-session team %s (owned by sess-B)", sb.ID)
		}
	}
	if _, ok := resumed.GetTeam(sa.ID); !ok {
		t.Fatal("own-session team was not auto-restored")
	}
	if _, ok := resumed.GetTeam(sb.ID); ok {
		t.Fatal("foreign-session team became active via auto-restore")
	}
	mf, err := resumed.loadManifest(sb.ID)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if mf.OwnerSessionID != "sess-B" {
		t.Fatalf("foreign manifest ownerSessionID mutated to %q, want sess-B", mf.OwnerSessionID)
	}
}

// T3 (design point 7): teammate_spawn/teammate_shutdown manifest upsert
// and remove preserve ownerSessionID; a foreign session stays invisible
// and unrestorable at every lifecycle stage.
func TestZZ5837TeammateLifecyclePreservesOwnerSession(t *testing.T) {
	root := t.TempDir()
	mine := newLeaseTestManager(t, root)
	mine.SetOwnerSession("sess-A")
	s := mine.CreateTeam("lifecycle", "leader-1")

	tm, err := mine.SpawnTeammate(s.ID, "alice", "", nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	mf, err := mine.loadManifest(s.ID)
	if err != nil {
		t.Fatalf("manifest after spawn: %v", err)
	}
	if mf.OwnerSessionID != "sess-A" {
		t.Fatalf("ownerSessionID lost after spawn upsert: %q", mf.OwnerSessionID)
	}
	if len(mf.Teammates) != 1 {
		t.Fatalf("manifest teammates after spawn = %d, want 1", len(mf.Teammates))
	}

	other := newLeaseTestManager(t, root)
	other.SetOwnerSession("sess-B")
	for _, info := range other.FilterTeamsForSession(SummarizePersistedTeamsForTest(t, root)) {
		if info.TeamID == s.ID {
			t.Fatal("foreign session sees the team after spawn")
		}
	}
	if _, err := other.RestoreTeamByName("lifecycle"); err == nil || !strings.Contains(err.Error(), "another session") {
		t.Fatalf("foreign restore-by-name after spawn must refuse with session isolation, got: %v", err)
	}

	if err := mine.ShutdownTeammate(s.ID, tm.ID); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	mf, err = mine.loadManifest(s.ID)
	if err != nil {
		t.Fatalf("manifest after shutdown: %v", err)
	}
	if mf.OwnerSessionID != "sess-A" {
		t.Fatalf("ownerSessionID lost after shutdown remove: %q", mf.OwnerSessionID)
	}
	if len(mf.Teammates) != 0 {
		t.Fatalf("manifest teammates after shutdown = %d, want 0", len(mf.Teammates))
	}
	if _, err := other.RestoreTeam(s.ID); err == nil || !strings.Contains(err.Error(), "another session") {
		t.Fatalf("foreign restore after shutdown must refuse with session isolation, got: %v", err)
	}
}

// T4 (R2 red at owner fix head AND at pre-#5837 parent): name resolution
// must prefer this session's team when a foreign team shares the name.
// Fixtures are hand-built with deterministic directory order so the
// foreign team sorts first (os.ReadDir is name-sorted).
func TestZZ5837NameResolutionPrefersOwnSession(t *testing.T) {
	root := t.TempDir()
	writeManifestFixture5837(t, root, "team-aaaaaa", "shared", "sess-B")
	writeManifestFixture5837(t, root, "team-zzzzzz", "shared", "sess-A")

	m := newLeaseTestManager(t, root)
	m.SetOwnerSession("sess-A")
	snap, err := m.RestoreTeamByName("shared")
	if err != nil {
		t.Fatalf("restore-by-name must resolve this session's team, got: %v", err)
	}
	if snap.ID != "team-zzzzzz" {
		t.Fatalf("name resolution picked %s; want team-zzzzzz (own session)", snap.ID)
	}

	// A session that owns no match gets the session-isolation error.
	foreign := newLeaseTestManager(t, root)
	foreign.SetOwnerSession("sess-C")
	if _, err := foreign.RestoreTeamByName("shared"); err == nil || !strings.Contains(err.Error(), "another session") {
		t.Fatalf("foreign-only name must fail with session isolation, got: %v", err)
	}
}

// T5 (R3 red at owner fix head): team_restore of an already-active own
// team returns the current snapshot instead of erroring - auto-restored
// teams of a resumed session are listed by the prompt hint, and the
// hinted team_restore call must not fail.
func TestZZ5837RestoreIdempotentForActiveOwnTeam(t *testing.T) {
	root := t.TempDir()
	m := newLeaseTestManager(t, root)
	m.SetOwnerSession("sess-A")
	s := m.CreateTeam("idem", "leader-1")
	if _, err := m.SpawnTeammate(s.ID, "alice", "", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	snap, err := m.RestoreTeam(s.ID)
	if err != nil {
		t.Fatalf("re-restore of an active own team must be idempotent, got: %v", err)
	}
	if snap.ID != s.ID || len(snap.Teammates) != 1 {
		t.Fatalf("idempotent restore returned ID=%s teammates=%d", snap.ID, len(snap.Teammates))
	}
	snap2, err := m.RestoreTeamByName("idem")
	if err != nil || snap2.ID != s.ID {
		t.Fatalf("re-restore by name: err=%v ID=%s", err, snap2.ID)
	}
}

// T6 (R0 API pin): BindSessionAndRestore binds manager + process surface
// and restores exactly the bound session's teams.
func TestZZ5837BindSessionAndRestore(t *testing.T) {
	prev := processOwnerSession()
	defer SetProcessOwnerSession(prev)
	t.Setenv("HOME", t.TempDir())

	root := t.TempDir()
	m1 := newLeaseTestManager(t, root)
	m1.SetOwnerSession("sess-A")
	sa := m1.CreateTeam("bindteam", "leader-1")
	m1b := newLeaseTestManager(t, root)
	m1b.SetOwnerSession("sess-B")
	sb := m1b.CreateTeam("otherteam", "leader-1")

	m2 := newLeaseTestManager(t, root)
	restored := m2.BindSessionAndRestore("sess-A")
	if len(restored) != 1 || restored[0].ID != sa.ID {
		t.Fatalf("BindSessionAndRestore restored %+v, want exactly [%s]", restored, sa.ID)
	}
	if m2.ownerSession() != "sess-A" {
		t.Fatalf("manager session after bind = %q, want sess-A", m2.ownerSession())
	}
	if processOwnerSession() != "sess-A" {
		t.Fatalf("process session after bind = %q, want sess-A", processOwnerSession())
	}
	if _, ok := m2.GetTeam(sb.ID); ok {
		t.Fatal("BindSessionAndRestore activated a foreign-session team")
	}
	if got := m2.BindSessionAndRestore(""); got != nil {
		t.Fatalf("empty bind must be a no-op, got %+v", got)
	}
}

// writeManifestFixture5837 hand-writes a persisted-team manifest with a
// chosen team ID and owner session (deterministic directory order).
func writeManifestFixture5837(t *testing.T, root, teamID, name, ownerSession string) {
	t.Helper()
	dir := filepath.Join(root, "swarm", "teams", teamID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := struct {
		TeamID         string   `json:"teamID"`
		Name           string   `json:"name"`
		LeaderID       string   `json:"leaderID"`
		OwnerSessionID string   `json:"ownerSessionID"`
		CreatedAt      string   `json:"createdAt"`
		UpdatedAt      string   `json:"updatedAt"`
		Teammates      []string `json:"teammates"`
	}{teamID, name, "leader-1", ownerSession, "2026-11-20T00:00:00Z", "2026-11-20T00:00:00Z", []string{}}
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
