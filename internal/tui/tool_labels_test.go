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

func TestDescribeToolUnknownUsesCommandDetail(t *testing.T) {
	got := describeTool(LangEnglish, "Run", `{"command":"git diff --cached"}`)
	if got.DisplayName != "Run" {
		t.Fatalf("expected display name Run, got %q", got.DisplayName)
	}
	if got.Detail != "git diff --cached" {
		t.Fatalf("expected command detail, got %q", got.Detail)
	}
}

func TestDescribeToolUnknownUsesPromptDetail(t *testing.T) {
	got := describeTool(LangEnglish, "Delegate", `{"prompt":"review the current changes"}`)
	if got.DisplayName != "Delegate" {
		t.Fatalf("expected display name Delegate, got %q", got.DisplayName)
	}
	if got.Detail != "review the current changes" {
		t.Fatalf("expected prompt detail, got %q", got.Detail)
	}
}
