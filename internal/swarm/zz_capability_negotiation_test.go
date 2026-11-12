package swarm

import (
	"reflect"
	"testing"
)

func TestNegotiateCapabilityAllowAll(t *testing.T) {
	m, _ := testManager(t)
	team := m.CreateTeam("neg-team", "leader-1")
	snap, err := m.SpawnTeammate(team.ID, "all-rounder", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	missing, err := m.NegotiateCapability(team.ID, snap.ID, []string{"run_command", "edit_file"})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("empty allowlist inherits all tools, missing=%v", missing)
	}
}

func TestNegotiateCapabilityDetectsMissing(t *testing.T) {
	m, _ := testManager(t)
	team := m.CreateTeam("neg-team2", "leader-1")
	snap, err := m.SpawnTeammate(team.ID, "reader", "", []string{"read_file", "grep", "search_files"})
	if err != nil {
		t.Fatal(err)
	}
	missing, err := m.NegotiateCapability(team.ID, snap.ID, []string{"edit_file", "grep", "run_command"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"edit_file", "run_command"}
	if !reflect.DeepEqual(missing, want) {
		t.Fatalf("missing must be sorted gap list, got %v want %v", missing, want)
	}

	// Subset within the allowlist is fine.
	if missing, _ := m.NegotiateCapability(team.ID, snap.ID, []string{"grep", "read_file"}); len(missing) != 0 {
		t.Fatalf("covered subset must pass, got %v", missing)
	}
}

func TestNegotiateCapabilityErrorsOnUnknown(t *testing.T) {
	m, _ := testManager(t)
	team := m.CreateTeam("neg-team3", "leader-1")
	if _, err := m.NegotiateCapability(team.ID, "tm-999", []string{"edit_file"}); err == nil {
		t.Fatal("unknown teammate must error")
	}
	if _, err := m.NegotiateCapability("nope", "tm-1", nil); err == nil {
		t.Fatal("unknown team must error")
	}
}

func TestSpawnRecordsSortedAllowedTools(t *testing.T) {
	m, _ := testManager(t)
	team := m.CreateTeam("neg-team4", "leader-1")
	if _, err := m.SpawnTeammate(team.ID, "scoped", "", []string{"write_file", "edit_file", "grep"}); err != nil {
		t.Fatal(err)
	}
	teamGot, _ := m.GetTeam(team.ID)
	info := teamGot.Teammates[0]
	want := []string{"edit_file", "grep", "write_file"}
	if !reflect.DeepEqual(info.AllowedTools, want) {
		t.Fatalf("allowed tools must be recorded sorted, got %v", info.AllowedTools)
	}
}
