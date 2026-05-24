package tui

import "testing"

func TestSuppressToolResultFormatsTeammateSpawn(t *testing.T) {
	result := `{"ID":"tm-1","Name":"researcher","Status":"idle"}`
	got := suppressToolResult("teammate_spawn", "", result, false)
	if got != "Teammate researcher Created" {
		t.Fatalf("expected formatted teammate spawn result, got %q", got)
	}
}

func TestSuppressToolResultFormatsTeamCreate(t *testing.T) {
	result := `{"ID":"team-1","Name":"research-squad"}`
	got := suppressToolResult("team_create", "", result, false)
	if got != "Team research-squad Created" {
		t.Fatalf("expected formatted team_create result, got %q", got)
	}
}

func TestSuppressToolResultFormatsSwarmTaskCreate(t *testing.T) {
	result := `{"ID":"task-1","Subject":"Fix tunnel replay","Description":"## Plan\n1. Repair replay\n2. Reseed snapshot"}`
	got := suppressToolResult("swarm_task_create", "", result, false)
	if got != "## Plan\n1. Repair replay\n2. Reseed snapshot" {
		t.Fatalf("expected extracted swarm task markdown, got %q", got)
	}
}

func TestSuppressToolResultFormatsStartCommand(t *testing.T) {
	if got := suppressToolResult("start_command", "", "Job ID: cmd-1\nStatus: running\nDuration: 1s", false); got != "Started" {
		t.Fatalf("expected Started, got %q", got)
	}
	if got := suppressToolResult("start_command", "", "permission denied", true); got != "Failed" {
		t.Fatalf("expected Failed, got %q", got)
	}
}

func TestSuppressToolResultFormatsTaskSummary(t *testing.T) {
	rawArgs := `{"taskId":"task-1","status":"in_progress"}`
	result := `{"id":"task-1","subject":"Fix tunnel parity","status":"in_progress"}`
	got := suppressToolResult("task_update", rawArgs, result, false)
	if got != "Updated Fix tunnel parity [in progress] — task-1 (status)" {
		t.Fatalf("unexpected task summary: %q", got)
	}
}
