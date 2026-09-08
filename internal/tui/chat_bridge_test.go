package tui

import (
	"strings"
	"testing"
)

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

func TestSuppressToolResultFormatsCronCreateSummary(t *testing.T) {
	got := suppressToolResult(
		"cron_create",
		`{"cron":"*/5 * * * *","prompt":"check status"}`,
		`{"ID":"job-1","CronExpr":"*/5 * * * *","Prompt":"check status","Recurring":true,"NextFire":"2026-05-24T17:30:00+08:00"}`,
		false,
	)
	if got != "Scheduled */5 * * * * — job-1" {
		t.Fatalf("unexpected cron_create summary: %q", got)
	}
}

func TestSuppressToolResultFormatsCronDeleteSummary(t *testing.T) {
	got := suppressToolResult("cron_delete", `{"jobId":"job-1"}`, `Job job-1 deleted`, false)
	if got != "Deleted job-1" {
		t.Fatalf("unexpected cron_delete summary: %q", got)
	}
}

func TestSuppressToolResultFormatsCronListSummary(t *testing.T) {
	got := suppressToolResult(
		"cron_list",
		`{}`,
		"- job-1 [recurring] */5 * * * * next=2026-05-24T17:30:00+08:00\n- job-2 [one-shot] 0 9 * * * next=2026-05-25T09:00:00+08:00\n",
		false,
	)
	if got != "2 scheduled jobs" {
		t.Fatalf("unexpected cron_list summary: %q", got)
	}
}

// TestCollapseGuidanceHints pins the display-layer guidance fold: trailing
// agent-guidance blocks (appended by Agent.appendGuidance as "\n\n[TAG] ...")
// collapse into a one-line summary, while real tool output survives - both
// mid-body [UPPERCASE] lines (logs legitimately contain those) and guidance
// that appears before genuine output (never happens, but must not fold).
func TestCollapseGuidanceHints(t *testing.T) {
	// Real output + trailing guidance blocks (the flooded-bash-error case).
	in := "exit status 1\nmake: *** [build] Error 1\n\n[ERROR-RUSH] Repeated identical failures detected.\n\n[RETRY-HINT] Consider a different strategy."
	out := collapseGuidanceHints(in)
	if !strings.Contains(out, "make: *** [build] Error 1") {
		t.Errorf("real output must survive: %q", out)
	}
	if strings.Contains(out, "Repeated identical failures") {
		t.Errorf("guidance body must be folded away: %q", out)
	}
	if !strings.Contains(out, "2 agent guidance block(s) folded") || !strings.Contains(out, "ERROR-RUSH, RETRY-HINT") {
		t.Errorf("summary must list tags: %q", out)
	}

	// Guidance-only single paragraph: cannot be distinguished from real
	// output that starts with a tagged line, so it stays visible (folding
	// only applies when a "\n\n" join proves injection onto a body).
	out = collapseGuidanceHints("[TIP] try reading the file first")
	if out != "[TIP] try reading the file first" {
		t.Errorf("single paragraph must stay visible: %q", out)
	}

	// Mid-body [UPPERCASE] lines are NOT folded (only trailing blocks).
	out = collapseGuidanceHints("[INFO] server started\nrequest completed\n200 OK")
	if out != "[INFO] server started\nrequest completed\n200 OK" {
		t.Errorf("mid-body tagged lines must not fold: %q", out)
	}

	// Plain output untouched.
	out = collapseGuidanceHints("just a normal result")
	if out != "just a normal result" {
		t.Errorf("plain result must be untouched: %q", out)
	}

	// Duplicate tags dedup in the summary.
	out = collapseGuidanceHints("out\n\n[WARN] a\n\n[WARN] b")
	if !strings.Contains(out, "WARN") || strings.Contains(out, "WARN, WARN") {
		t.Errorf("duplicate tags should dedup: %q", out)
	}
}
