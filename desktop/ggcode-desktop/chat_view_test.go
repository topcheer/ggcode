package main

import "testing"

func TestFormatTeammateSpawnResult(t *testing.T) {
	result := `{"ID":"tm-1","Name":"researcher","Status":"idle"}`
	got := formatTeammateSpawnResult(result)
	if got != "Teammate researcher Created" {
		t.Fatalf("expected formatted teammate spawn result, got %q", got)
	}
}

func TestFormatTeamCreateResult(t *testing.T) {
	result := `{"ID":"team-1","Name":"research-squad"}`
	cv := &ChatView{}
	got := cv.formatToolResult("team_create", result)
	if got != "Team research-squad Created" {
		t.Fatalf("expected formatted team_create result, got %q", got)
	}
}

func TestFormatSwarmTaskCreateResult(t *testing.T) {
	result := `{"ID":"task-1","Subject":"Fix replay gaps","Description":"## Plan\n- keep markdown"}`
	cv := &ChatView{}
	got := cv.formatToolResult("swarm_task_create", result)
	if got != "## Plan\n- keep markdown" {
		t.Fatalf("expected extracted swarm task markdown, got %q", got)
	}
}
