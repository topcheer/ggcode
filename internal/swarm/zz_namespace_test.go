package swarm

import (
	"github.com/topcheer/ggcode/internal/config"
	"path/filepath"
	"testing"
)

// #5860: the confirmed session id IS the storage namespace. Four pins:
// round-trip, cross-session invisibility, unconfirmed-create panic,
// re-confirm panic.
func nsManager(t *testing.T, root, sid string) *Manager {
	t.Helper()
	m := NewManager(config.SwarmConfig{MaxTeammatesPerTeam: 4}, sid, nil, nil, nil)
	m.BoardRoot = root
	m.JournalsEnabled = true
	return m
}

func TestSessionNamespaceRoundTrip(t *testing.T) {
	root := t.TempDir()
	m1 := nsManager(t, root, "sess-A")
	snap := m1.CreateTeam("ops", "leader-1")
	if snap.ID == "" {
		t.Fatal("create failed")
	}

	// A fresh manager bound to the SAME session restores it by listdir.
	m2 := nsManager(t, root, "sess-A")
	restored := m2.AutoRestoreSessionTeams()
	if len(restored) != 1 || restored[0].ID != snap.ID {
		t.Fatalf("round-trip: got %v, want [%s]", restored, snap.ID)
	}
}

func TestForeignSessionInvisible(t *testing.T) {
	root := t.TempDir()
	m1 := nsManager(t, root, "sess-A")
	snap := m1.CreateTeam("ops", "leader-1")

	mB := nsManager(t, root, "sess-B")
	if got := mB.AutoRestoreSessionTeams(); len(got) != 0 {
		t.Fatalf("foreign session restored %v; must be invisible", got)
	}
	if _, err := mB.RestoreTeam(snap.ID); err == nil {
		t.Fatal("explicit restore of a foreign-session team must fail")
	}
}

func TestUnconfirmedCreatePanics(t *testing.T) {
	root := t.TempDir()
	m := nsManager(t, root, "") // fresh session: nothing confirmed yet
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("team_create before confirmation must panic")
		}
	}()
	_ = m.CreateTeam("ops", "leader-1")
}

func TestConfirmSessionOnce(t *testing.T) {
	root := t.TempDir()
	m := nsManager(t, root, "")
	m.ConfirmSession("sess-X")
	m.ConfirmSession("sess-X") // idempotent
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("re-confirm with a different id must panic")
		}
	}()
	m.ConfirmSession("sess-Y")
	_ = filepath.Separator
}

func TestRebindSessionSwitchesNamespace(t *testing.T) {
	root := t.TempDir()
	m := nsManager(t, root, "")
	m.ConfirmSession("sess-X")
	m.RebindSession("sess-X") // same id: no-op
	if got := m.SessionNamespace(); got != "sess-X" {
		t.Fatalf("same-id rebind changed namespace: %q", got)
	}
	m.RebindSession("sess-Y") // different id: switch, no panic
	if got := m.SessionNamespace(); got != "sess-Y" {
		t.Fatalf("rebind did not switch namespace: %q", got)
	}
	// empty id is ignored, namespace intact
	m.RebindSession("")
	if got := m.SessionNamespace(); got != "sess-Y" {
		t.Fatalf("empty rebind changed namespace: %q", got)
	}
	// rebinding back to a previously used id is allowed too (session cycling)
	m.RebindSession("sess-X")
	if got := m.SessionNamespace(); got != "sess-X" {
		t.Fatalf("rebind back failed: %q", got)
	}
}
