package tui

import "testing"

func TestSuppressToolResultFormatsTeammateSpawn(t *testing.T) {
	result := `{"ID":"tm-1","Name":"researcher","Status":"idle"}`
	got := suppressToolResult("teammate_spawn", result)
	if got != "Teammate researcher Created" {
		t.Fatalf("expected formatted teammate spawn result, got %q", got)
	}
}

func TestSuppressToolResultFormatsTeamCreate(t *testing.T) {
	result := `{"ID":"team-1","Name":"research-squad"}`
	got := suppressToolResult("team_create", result)
	if got != "Team research-squad Created" {
		t.Fatalf("expected formatted team_create result, got %q", got)
	}
}

func TestSuppressToolResultFormatsSwarmTaskCreate(t *testing.T) {
	result := `{"ID":"task-1","Subject":"Fix tunnel replay","Description":"## Plan\n1. Repair replay\n2. Reseed snapshot"}`
	got := suppressToolResult("swarm_task_create", result)
	if got != "## Plan\n1. Repair replay\n2. Reseed snapshot" {
		t.Fatalf("expected extracted swarm task markdown, got %q", got)
	}
}
