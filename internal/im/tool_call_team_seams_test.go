package im

import (
	"strings"
	"testing"
)

// Pins for the r183 formatCallTeam sub-domain decomposition (behavior-preserving).

func TestFormatCallTeamHidden_Pins(t *testing.T) {
	hidden := []string{
		"teammate_list", "swarm_task_list", "swarm_task_claim",
		"a2a_discover", "a2a_list_tasks", "a2a_cancel_task", "a2a_get_task",
	}
	for _, name := range hidden {
		out, ok := formatCallTeamHidden(name)
		if !ok || out != "" {
			t.Fatalf("%s must stay hidden, got (%q, %v)", name, out, ok)
		}
	}
	if _, ok := formatCallTeamHidden("not_a_tool"); ok {
		t.Fatal("unknown tool must not be claimed by hidden set")
	}
}

func TestArgOrDetail_Fallback(t *testing.T) {
	tc := &ToolCallInfo{Detail: "dflt"}
	if got := argOrDetail(`{"name":"n1"}`, "name", tc); got != "n1" {
		t.Fatalf("arg value must win, got %q", got)
	}
	if got := argOrDetail(`{}`, "name", tc); got != "dflt" {
		t.Fatalf("missing arg must fall back to Detail, got %q", got)
	}
	if got := argOrDetail(`{"name":""}`, "name", tc); got != "dflt" {
		t.Fatalf("empty arg must fall back to Detail, got %q", got)
	}
	if got := argOrDetail("not json", "name", tc); got != "dflt" {
		t.Fatalf("unparseable args must fall back to Detail, got %q", got)
	}
}

func TestFormatCallTeam_DispatchPins(t *testing.T) {
	if out, ok := formatCallTeam(ToolLangEn, &ToolCallInfo{ToolName: "teammate_list"}); !ok || out != "" {
		t.Fatalf("teammate_list hidden via orchestrator, got (%q, %v)", out, ok)
	}

	out, ok := formatCallTeam(ToolLangEn, &ToolCallInfo{ToolName: "team_create", Detail: "myteam"})
	if !ok || !strings.Contains(out, "myteam") || !strings.HasPrefix(out, "👥") {
		t.Fatalf("team_create Detail fallback pin, got (%q, %v)", out, ok)
	}

	out, ok = formatCallTeam(ToolLangEn, &ToolCallInfo{ToolName: "a2a_send_task", Args: `{"target":"agent-9"}`})
	if !ok || !strings.Contains(out, "agent-9") {
		t.Fatalf("a2a_send_task target pin, got (%q, %v)", out, ok)
	}

	out, ok = formatCallTeam(ToolLangEn, &ToolCallInfo{ToolName: "cancel_agent", Detail: "tid-1"})
	if !ok || !strings.Contains(out, "tid-1") || !strings.HasPrefix(out, "❌") {
		t.Fatalf("cancel_agent agent_id fallback pin, got (%q, %v)", out, ok)
	}

	longTask := strings.Repeat("x", 100)
	out, ok = formatCallTeam(ToolLangEn, &ToolCallInfo{ToolName: "spawn_agent", Args: `{"task":"` + longTask + `"}`})
	if !ok || !strings.Contains(out, strings.Repeat("x", 57)+"...") || strings.Contains(out, strings.Repeat("x", 58)) {
		t.Fatalf("spawn_agent 60-rune truncation pin, got (%q, %v)", out, ok)
	}

	out, ok = formatCallTeam(ToolLangEn, &ToolCallInfo{ToolName: "spawn_agent", Detail: ""})
	if !ok || out != "🚀 "+imLabel(ToolLangEn, "spawn_agent") {
		t.Fatalf("spawn_agent empty-task short form pin, got (%q, %v)", out, ok)
	}

	if _, ok := formatCallTeam(ToolLangEn, &ToolCallInfo{ToolName: "no_such_team_tool"}); ok {
		t.Fatal("unknown tool must pass through to next domain")
	}
}

func TestFormatCallTeamSwarm_BytePin(t *testing.T) {
	out, ok := formatCallTeamSwarm(ToolLangZhCN, "", &ToolCallInfo{ToolName: "team_delete"})
	want := "👥 " + imLabel(ToolLangZhCN, "team_delete")
	if !ok || out != want {
		t.Fatalf("team_delete byte pin:\n got %q\nwant %q", out, want)
	}
}
