package tui

import "testing"

func TestFirstLine(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello world", "hello world"},
		{"first line\nsecond line", "first line"},
		{"only one line", "only one line"},
		{"", ""},
		{"line1\nline2\nline3", "line1"},
	}
	for _, tt := range tests {
		got := firstLine(tt.input)
		if got != tt.want {
			t.Errorf("firstLine(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDescribeToolSwarmTaskCreateUsesSubject(t *testing.T) {
	got := describeTool(LangEnglish, "swarm_task_create", `{"team_id":"team-1","subject":"Fix replay gaps","description":"## details\n- not for header"}`)
	if got.DisplayName != "Fix replay gaps" {
		t.Fatalf("expected subject display name, got %q", got.DisplayName)
	}
	if got.Detail != "" {
		t.Fatalf("expected empty detail, got %q", got.Detail)
	}
}
