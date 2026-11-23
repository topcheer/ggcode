package swarm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDescribePersistedTeamsUnder_RosterAndSummary(t *testing.T) {
	root := t.TempDir()
	write := func(teamID string, m TeamManifest) {
		dir := filepath.Join(root, "swarm", "teams", teamID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(m)
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	older := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	write("t-old", TeamManifest{TeamID: "t-old", Name: "old-squad", LeaderID: "leader", CreatedAt: older,
		Teammates: []ManifestTeammate{{ID: "tm-1", Name: "coder", LastActiveAt: older}}})
	write("t-new", TeamManifest{TeamID: "t-new", Name: "new-squad", LeaderID: "leader", CreatedAt: older,
		Teammates: []ManifestTeammate{
			{ID: "tm-2", Name: "researcher", Role: "read-only", LastActiveAt: newer},
			{ID: "tm-3", Name: "writer", LastActiveAt: older},
		}})

	details := describePersistedTeamsUnder(root)
	if len(details) != 2 {
		t.Fatalf("want 2 persisted teams, got %d", len(details))
	}
	// newest activity first
	if details[0].TeamID != "t-new" {
		t.Fatalf("newest team must come first, got %q", details[0].TeamID)
	}
	newSquad := details[0]
	if newSquad.PersistedTeamInfo.Members != 2 || len(newSquad.Members) != 2 {
		t.Fatalf("count and roster must both be 2, got %d / %d", newSquad.PersistedTeamInfo.Members, len(newSquad.Members))
	}
	if !newSquad.LastActive.Equal(newer) {
		t.Fatalf("LastActive must be the max member activity, got %v", newSquad.LastActive)
	}
	if newSquad.Members[0].Name != "researcher" || newSquad.Members[0].Role != "read-only" || newSquad.Members[0].ID != "tm-2" {
		t.Fatalf("roster entry mismatch: %+v", newSquad.Members[0])
	}

	// the prompt-facing summary keeps its old shape
	summary := summarizePersistedTeamsUnder(root)
	if len(summary) != 2 || summary[0].Name != "new-squad" || summary[0].Members != 2 {
		t.Fatalf("summary mismatch: %+v", summary)
	}
}
