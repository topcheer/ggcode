package main

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

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

func TestContainsLeftSquareRightRounded(t *testing.T) {
	const (
		width  = 40.0
		height = 20.0
		radius = 8.0
	)
	tests := []struct {
		name string
		x    float64
		y    float64
		want bool
	}{
		{name: "left top corner stays square", x: 0.5, y: 0.5, want: true},
		{name: "left bottom corner stays square", x: 0.5, y: 19.5, want: true},
		{name: "right top outside rounded corner", x: 39.5, y: 0.5, want: false},
		{name: "right edge center inside", x: 39.5, y: 10.0, want: true},
	}
	for _, tt := range tests {
		if got := containsLeftSquareRightRounded(tt.x, tt.y, width, height, radius); got != tt.want {
			t.Fatalf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestSurfaceRectRefreshesWithThemeChange(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	app.Settings().SetTheme(newThemeForScheme("light"))
	rect := surfaceRect(theme.ColorNamePrimary)
	lightFill := toNRGBA(rect.FillColor)
	lightStroke := toNRGBA(rect.StrokeColor)

	app.Settings().SetTheme(newThemeForScheme("forest"))
	rect.Refresh()
	forestFill := toNRGBA(rect.FillColor)
	forestStroke := toNRGBA(rect.StrokeColor)

	if lightFill == forestFill {
		t.Fatalf("expected fill color to change across themes, got %v", lightFill)
	}
	if lightStroke == forestStroke {
		t.Fatalf("expected stroke color to change across themes, got %v", lightStroke)
	}
}
