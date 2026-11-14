package a2a

import (
	"testing"
	"time"
)

// TestDiscoverMergeE2E proves the full runtime path behind the introspect list
// Seat face: two real registries over real mDNS, seat A registers its TXT
// BEFORE its identity lands (the production ordering: Register happens at
// daemon start, SetSelfIdentity once the lanchat hub exists), so A's
// advertisement is identity-frozen. Seat B's Discover() must backfill A's nick
// from the file-backed warm roster via mergeRosterIdentities.
func TestDiscoverMergeE2E(t *testing.T) {
	ws := t.TempDir()

	regA, err := NewRegistry()
	if err != nil {
		t.Fatalf("registry A: %v", err)
	}
	defer regA.Unregister()
	regB, err := NewRegistry()
	if err != nil {
		t.Fatalf("registry B: %v", err)
	}
	defer regB.Unregister()

	// Seat A: TXT frozen WITHOUT nick (identity not yet known).
	if err := regA.Register(InstanceInfo{ID: "seat-e2e-a", PID: 30211, Workspace: ws, Endpoint: "http://127.0.0.1:30211", Status: "ready"}); err != nil {
		t.Fatalf("register A: %v", err)
	}
	// Identity lands AFTER registration: roster entry gets the nick, the
	// already-built mDNS TXT does not (the gap this fix closes on read side).
	regA.SetSelfIdentity("CalmKoala_developer_agent", "developer", "ggcode")

	if err := regB.Register(InstanceInfo{ID: "seat-e2e-b", PID: 30212, Workspace: ws, Endpoint: "http://127.0.0.1:30212", Status: "ready"}); err != nil {
		t.Fatalf("register B: %v", err)
	}

	deadline := time.Now().Add(12 * time.Second)
	nick := ""
	for time.Now().Before(deadline) {
		insts, derr := regB.Discover()
		if derr == nil {
			for _, in := range insts {
				if in.ID == "seat-e2e-a" && in.Nick != "" {
					nick = in.Nick
				}
			}
		}
		if nick != "" {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if nick != "CalmKoala_developer_agent" {
		t.Fatalf("Seat face not merged via real mDNS+roster Discover path: nick=%q", nick)
	}
}
