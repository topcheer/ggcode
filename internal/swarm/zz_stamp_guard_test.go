package swarm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// #5858: the resume path once bound an EMPTY session ID, wiping the
// auto-mint - every later team_create landed unstamped and became
// unrestorable. Guards: SetOwnerSession ignores empty; CreateTeam
// re-mints at write time.
func TestEmptySessionBindNeverProducesUnstampedTeam(t *testing.T) {
	root := t.TempDir()
	m := newLeaseTestManager(t, root)
	m.mu.Lock()
	m.ownerSessionID = "" // simulate the resume race wiping the mint
	m.mu.Unlock()
	m.SetOwnerSession("") // the faulty bind must be a no-op

	s := m.CreateTeam("stampguard", "leader-1")
	p := filepath.Join(root, "swarm", "teams", s.ID, "manifest.json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var raw map[string]any
	json.Unmarshal(data, &raw)
	v, _ := raw["ownerSessionID"].(string)
	if v == "" {
		t.Fatalf("manifest written without ownerSessionID (field regression): %s", data)
	}
	// And the stamp must be a real bind: a manager bound to the SAME
	// session (the post-restart process) must pass the ownership check.
	m.mu.Lock()
	stamped := m.ownerSessionID
	m.mu.Unlock()
	if stamped == "" {
		t.Fatalf("stamp must be a bound session id, got %q", stamped)
	}
	m2 := newLeaseTestManager(t, root)
	m2.SetOwnerSession(stamped)
	if err := m2.SessionOwnsTeam(s.ID); err != nil {
		t.Fatalf("same-session manager must own the stamped team: %v", err)
	}
}
