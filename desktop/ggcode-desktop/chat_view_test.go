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
	got := cv.formatToolResult("team_create", result, false)
	if got != "Team research-squad Created" {
		t.Fatalf("expected formatted team_create result, got %q", got)
	}
}

func TestFormatSwarmTaskCreateResult(t *testing.T) {
	result := `{"ID":"task-1","Subject":"Fix replay gaps","Description":"## Plan\n- keep markdown"}`
	cv := &ChatView{}
	got := cv.formatToolResult("swarm_task_create", result, false)
	if got != "## Plan\n- keep markdown" {
		t.Fatalf("expected extracted swarm task markdown, got %q", got)
	}
}

func TestFormatStartCommandResult(t *testing.T) {
	cv := &ChatView{}
	if got := cv.formatToolResult("start_command", "Job ID: cmd-1\nStatus: running\nDuration: 1s", false); got != "Started" {
		t.Fatalf("expected Started, got %q", got)
	}
	if got := cv.formatToolResult("start_command", "permission denied", true); got != "Failed" {
		t.Fatalf("expected Failed, got %q", got)
	}
}

func TestClassifyToolGUICronToolsAreNotSuppressed(t *testing.T) {
	for _, name := range []string{"cron_create", "cron_delete", "cron_list"} {
		if got := classifyToolGUI(name); got == tcSuppress {
			t.Fatalf("expected %s to render results, got tcSuppress", name)
		}
	}
}
