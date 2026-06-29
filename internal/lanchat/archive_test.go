package lanchat

import (
	"testing"
	"time"
)

func TestArchivePeerRingBuffer(t *testing.T) {
	hub := &Hub{
		peers: make(map[string]*Participant),
	}

	// Fill archive beyond capacity
	for i := 0; i < maxArchiveEntries+10; i++ {
		p := &Participant{
			NodeID:    "node-" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			HumanNick: "user" + string(rune('a'+i%26)),
			Team:      "team-a",
			Role:      "dev",
			LastSeen:  time.Now().Add(-time.Duration(i) * time.Minute).Unix(),
		}
		hub.archivePeer(p)
	}

	// Should be capped at maxArchiveEntries
	if len(hub.archive) != maxArchiveEntries {
		t.Errorf("archive size = %d, want %d", len(hub.archive), maxArchiveEntries)
	}

	// Oldest entries should have been evicted (the first 10)
	// Check that the oldest remaining entry is index 10 of the original
	first := hub.archive[0]
	if first.HumanNick == "usera" {
		t.Error("expected oldest entry (usera) to be evicted")
	}
}

func TestLookupArchiveByNodeID(t *testing.T) {
	hub := &Hub{peers: make(map[string]*Participant)}

	p := &Participant{
		NodeID:    "node-xyz",
		HumanNick: "alice_dev",
		Team:      "platform",
		Role:      "dev",
	}
	hub.archivePeer(p)

	// Found
	ap := hub.LookupArchiveByNodeID("node-xyz")
	if ap == nil {
		t.Fatal("expected to find node-xyz in archive")
	}
	if ap.HumanNick != "alice_dev" {
		t.Errorf("HumanNick = %s, want alice_dev", ap.HumanNick)
	}
	if ap.Team != "platform" || ap.Role != "dev" {
		t.Errorf("Team=%s Role=%s, want platform/dev", ap.Team, ap.Role)
	}

	// Not found
	ap = hub.LookupArchiveByNodeID("nonexistent")
	if ap != nil {
		t.Error("expected nil for nonexistent node")
	}
}

func TestLookupArchiveByTeamRole(t *testing.T) {
	hub := &Hub{peers: make(map[string]*Participant)}

	// Add two peers with same team+role (e.g., two frontend devs)
	hub.archivePeer(&Participant{
		NodeID: "node-old", HumanNick: "alice_fe", Team: "web", Role: "frontend",
	})
	hub.archivePeer(&Participant{
		NodeID: "node-newer", HumanNick: "bob_fe", Team: "web", Role: "frontend",
	})

	// Should return the most recent (last added)
	ap := hub.LookupArchiveByTeamRole("web", "frontend")
	if ap == nil {
		t.Fatal("expected to find web/frontend in archive")
	}
	if ap.NodeID != "node-newer" {
		t.Errorf("NodeID = %s, want node-newer (most recent)", ap.NodeID)
	}

	// Not found
	ap = hub.LookupArchiveByTeamRole("nonexistent", "role")
	if ap != nil {
		t.Error("expected nil for nonexistent team/role")
	}
}

func TestLookupArchiveByNick(t *testing.T) {
	hub := &Hub{peers: make(map[string]*Participant)}

	hub.archivePeer(&Participant{
		NodeID: "node-1", HumanNick: "Alice_Dev", AgentNick: "Alice_Dev_agent",
		Team: "dev-team", Role: "dev",
	})

	// Match by human_nick (case-insensitive)
	ap := hub.LookupArchiveByNick("alice_dev")
	if ap == nil {
		t.Fatal("expected to find alice_dev in archive")
	}
	if ap.NodeID != "node-1" {
		t.Errorf("NodeID = %s, want node-1", ap.NodeID)
	}

	// Match by agent_nick (case-insensitive)
	ap = hub.LookupArchiveByNick("alice_dev_agent")
	if ap == nil {
		t.Fatal("expected to find alice_dev_agent in archive")
	}

	// Not found
	ap = hub.LookupArchiveByNick("nobody")
	if ap != nil {
		t.Error("expected nil for nonexistent nick")
	}
}

func TestArchiveEmpty(t *testing.T) {
	hub := &Hub{peers: make(map[string]*Participant)}

	if hub.LookupArchiveByNodeID("x") != nil {
		t.Error("expected nil on empty archive")
	}
	if hub.LookupArchiveByTeamRole("t", "r") != nil {
		t.Error("expected nil on empty archive")
	}
	if hub.LookupArchiveByNick("nick") != nil {
		t.Error("expected nil on empty archive")
	}

	archive := hub.Archive()
	if len(archive) != 0 {
		t.Errorf("expected empty archive, got %d", len(archive))
	}
}

func TestArchivePeerFIFOOrder(t *testing.T) {
	hub := &Hub{peers: make(map[string]*Participant)}

	for i, name := range []string{"alpha", "beta", "gamma"} {
		hub.archivePeer(&Participant{
			NodeID:    "node-" + name,
			HumanNick: name,
			Team:      "team",
			Role:      "role",
		})
		_ = i
	}

	archive := hub.Archive()
	if len(archive) != 3 {
		t.Fatalf("archive size = %d, want 3", len(archive))
	}
	// FIFO: alpha should be first, gamma last
	if archive[0].HumanNick != "alpha" {
		t.Errorf("first entry = %s, want alpha", archive[0].HumanNick)
	}
	if archive[2].HumanNick != "gamma" {
		t.Errorf("last entry = %s, want gamma", archive[2].HumanNick)
	}
}
