package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func newLeaseTestManager(t *testing.T, boardRoot string) *Manager {
	t.Helper()
	m := NewManager(config.SwarmConfig{}, nil, nil, nil)
	m.BoardRoot = boardRoot
	return m
}

// P0 pin 1: two Managers (same process, distinct boot IDs) create teams with
// distinct random IDs, distinct board directories, and distinct owner leases.
func TestRandomTeamIDs_TwoManagersDistinctPaths(t *testing.T) {
	root := t.TempDir()
	m1 := newLeaseTestManager(t, root)
	m2 := newLeaseTestManager(t, root)

	s1 := m1.CreateTeam("alpha", "leader-1")
	s2 := m2.CreateTeam("alpha", "leader-2")

	if s1.ID == s2.ID {
		t.Fatalf("two managers produced the same team ID %q", s1.ID)
	}
	if !strings.HasPrefix(s1.ID, "team-") || len(s1.ID) != len("team-")+6 {
		t.Fatalf("team ID %q is not team-<6hex>", s1.ID)
	}
	d1, d2 := m1.teamDir(s1.ID), m2.teamDir(s2.ID)
	if d1 == d2 {
		t.Fatalf("board paths collide: %s", d1)
	}
	for _, c := range []struct {
		m  *Manager
		id string
	}{
		{m1, s1.ID}, {m2, s2.ID},
	} {
		lease, err := readOwnerLeaseFile(c.m.ownerLeasePath(c.id))
		if err != nil {
			t.Fatalf("owner.json missing for %s: %v", c.id, err)
		}
		if lease.BootID != c.m.bootID {
			t.Fatalf("owner.json bootID %q != manager bootID %q", lease.BootID, c.m.bootID)
		}
	}
	if m1.bootID == m2.bootID {
		t.Fatal("managers share a boot ID")
	}
}

// P0 pin 2: a live foreign owner (same host, PID answers signal-0 - the
// same-process two-manager case) must be refused with an error.
func TestLiveOwnerLeaseRefused(t *testing.T) {
	root := t.TempDir()
	m1 := newLeaseTestManager(t, root)
	m2 := newLeaseTestManager(t, root)

	s := m1.CreateTeam("owned", "leader-1")
	_, err := m1.SpawnTeammate(s.ID, "alice", "", nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	if v := m2.checkOwnerLease(s.ID); v != leaseAliveForeign {
		t.Fatalf("verdict = %v, want leaseAliveForeign", v)
	}
	if _, err := m2.RestoreTeam(s.ID); err == nil {
		t.Fatal("RestoreTeam on a live-foreign board must be refused")
	} else if !strings.Contains(err.Error(), "live instance") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// P0 pin 3a: a dead lease (stale heartbeat, foreign boot) is inheritable
// via RestoreTeam, which re-stamps the lease.
func TestDeadLeaseInheritedViaRestore(t *testing.T) {
	root := t.TempDir()
	m1 := newLeaseTestManager(t, root)
	m2 := newLeaseTestManager(t, root)

	s := m1.CreateTeam("deadowner", "leader-1")
	if _, err := m1.SpawnTeammate(s.ID, "bob", "", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	stale := ownerLease{
		BootID:    "foreign-boot",
		PID:       4194304,
		Hostname:  "not-this-host",
		Heartbeat: time.Now().Add(-2 * ownerLeaseTTL),
	}
	if err := writeOwnerLeaseFile(m1.ownerLeasePath(s.ID), &stale); err != nil {
		t.Fatalf("write stale lease: %v", err)
	}

	if v := m2.checkOwnerLease(s.ID); v != leaseDead {
		t.Fatalf("verdict = %v, want leaseDead", v)
	}
	snap, err := m2.RestoreTeam(s.ID)
	if err != nil {
		t.Fatalf("RestoreTeam with dead lease should inherit: %v", err)
	}
	if snap.ID != s.ID || len(snap.Teammates) != 1 {
		t.Fatalf("restored snapshot mismatch: %+v", snap)
	}
	lease, err := readOwnerLeaseFile(m2.ownerLeasePath(s.ID))
	if err != nil {
		t.Fatalf("re-stamped lease missing: %v", err)
	}
	if lease.BootID != m2.bootID {
		t.Fatalf("lease not re-stamped to inheritor: %q", lease.BootID)
	}
}

// P0 pin 3b: legacy directories without owner.json are dead-owner semantics:
// inheritable via RestoreTeam only.
func TestLegacyDirNoOwnerInheritsViaRestore(t *testing.T) {
	root := t.TempDir()
	m1 := newLeaseTestManager(t, root)
	s := m1.CreateTeam("legacy", "leader-1")
	if _, err := m1.SpawnTeammate(s.ID, "carol", "", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := os.Remove(m1.ownerLeasePath(s.ID)); err != nil {
		t.Fatalf("remove owner.json: %v", err)
	}

	m2 := newLeaseTestManager(t, root)
	if v := m2.checkOwnerLease(s.ID); v != leaseLegacy {
		t.Fatalf("verdict = %v, want leaseLegacy", v)
	}
	if _, err := m2.RestoreTeam(s.ID); err != nil {
		t.Fatalf("legacy restore should inherit: %v", err)
	}
}

// P0 pin 4: CreateTeam never lands on an existing directory (fresh dirs
// only), and collision-checked allocation produces unique IDs.
func TestCreateTeamNeverPicksExistingDir(t *testing.T) {
	root := t.TempDir()
	m := newLeaseTestManager(t, root)

	seen := make(map[string]bool)
	for i := 0; i < 24; i++ {
		s := m.CreateTeam("many", "leader")
		if seen[s.ID] {
			t.Fatalf("duplicate team ID %q", s.ID)
		}
		seen[s.ID] = true
		dir := m.teamDir(s.ID)
		if _, err := os.Stat(filepath.Join(dir, "owner.json")); err != nil {
			t.Fatalf("team %d: owner.json not stamped: %v", i, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
			t.Fatalf("team %d: manifest not written: %v", i, err)
		}
	}
}
