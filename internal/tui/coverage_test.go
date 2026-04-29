package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/subagent"
)

func TestIsCommandTool(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"start_command", true},
		{"run_command", true},
		{"edit_file", false},
		{"read_file", false},
	}
	for _, tt := range tests {
		got := isCommandTool(tt.name)
		if got != tt.want {
			t.Errorf("isCommandTool(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestNormalizeRemoteAnswerToken(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"  Hello World  ", "hello world"},
		{"YES", "yes"},
		{"  Choice A ", "choice a"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeRemoteAnswerToken(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeRemoteAnswerToken(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestQuestionnaireActionHint(t *testing.T) {
	got := questionnaireActionHint("en")
	if got == "" {
		t.Error("expected non-empty hint")
	}
}

func TestQuestionnaireSubmitTitle(t *testing.T) {
	got := questionnaireSubmitTitle("en")
	if got == "" {
		t.Error("expected non-empty title")
	}
}

func TestQuestionnaireSubmitBody(t *testing.T) {
	got := questionnaireSubmitBody("en", 3, 5)
	_ = got
}

func TestQuestionnaireCancelTitle(t *testing.T) {
	got := questionnaireCancelTitle("en")
	if got == "" {
		t.Error("expected non-empty title")
	}
}

func TestQuestionnaireCancelBody(t *testing.T) {
	got := questionnaireCancelBody("en")
	_ = got
}

func TestQuestionnaireCompletionLabel(t *testing.T) {
	got := questionnaireCompletionLabel("en", "submitted")
	_ = got
}

func TestLocalizeSubAgentStatus(t *testing.T) {
	statuses := []subagent.Status{
		subagent.StatusPending,
		subagent.StatusRunning,
		subagent.StatusCompleted,
		subagent.StatusFailed,
		subagent.StatusCancelled,
	}
	for _, s := range statuses {
		got := localizeSubAgentStatus("en", s)
		_ = got
	}
}

func TestStripAnsiForChat(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"\x1b[31mred\x1b[0m", "red"},
		{"no ansi", "no ansi"},
	}
	for _, tt := range tests {
		got := stripAnsiForChat(tt.input)
		if got != tt.expected {
			t.Errorf("stripAnsiForChat(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestExtractAgentID(t *testing.T) {
	got := extractAgentID("  [agent-123] doing work")
	// May return empty if format doesn't match, just verify no panic
	_ = got
}

func TestTitleizeHarnessText(t *testing.T) {
	got := titleizeHarnessText("some text here")
	_ = got // just verify no panic
}
