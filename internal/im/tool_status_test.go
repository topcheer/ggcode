package im

import (
	"strings"
	"testing"
)

func TestLocalizedCommandActivity(t *testing.T) {
	if got := localizedCommandActivity(ToolLangEn, "running tests"); got != "running tests" {
		t.Errorf("en: %q", got)
	}
	if got := localizedCommandActivity(ToolLangZhCN, "running tests"); got != "正在running tests" {
		t.Errorf("zh: %q", got)
	}
}

func TestLocalizedGenericToolName(t *testing.T) {
	if got := localizedGenericToolName(ToolLangEn, "read_file"); !strings.Contains(got, "Read File") {
		t.Errorf("en: %q", got)
	}
	if got := localizedGenericToolName(ToolLangZhCN, "read_file"); !strings.Contains(got, "read file") {
		t.Errorf("zh: %q", got)
	}
}

func TestAskUserToolTarget(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{
			name: "title present",
			args: map[string]any{"title": "Choose option"},
			want: "Choose option",
		},
		{
			name: "questions with title",
			args: map[string]any{
				"questions": []any{
					map[string]any{"title": "Pick one", "prompt": "Select an option"},
				},
			},
			want: "Pick one",
		},
		{
			name: "questions with prompt fallback",
			args: map[string]any{
				"questions": []any{
					map[string]any{"prompt": "Enter your name"},
				},
			},
			want: "Enter your name",
		},
		{
			name: "multiple questions",
			args: map[string]any{
				"title": "Survey",
				"questions": []any{
					map[string]any{"title": "Q1"},
					map[string]any{"title": "Q2"},
					map[string]any{"title": "Q3"},
				},
			},
			want: "Survey",
		},
		{
			name: "multiple questions no title",
			args: map[string]any{
				"questions": []any{
					map[string]any{"title": "First question"},
					map[string]any{"title": "Second question"},
				},
			},
			want: "First question +1",
		},
		{
			name: "empty questions",
			args: map[string]any{
				"questions": []any{},
			},
			want: "",
		},
		{
			name: "no title or questions",
			args: map[string]any{},
			want: "",
		},
		{
			name: "invalid questions type",
			args: map[string]any{
				"questions": "invalid",
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := askUserToolTarget(tt.args)
			if got != tt.want {
				t.Errorf("askUserToolTarget() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestArgAnyString(t *testing.T) {
	m := map[string]any{"key": "value", "num": 42, "empty": ""}
	if got := argAnyString(m, "key"); got != "value" {
		t.Errorf("existing string: %q", got)
	}
	if got := argAnyString(m, "num"); got != "" {
		t.Errorf("non-string: %q", got)
	}
	if got := argAnyString(m, "missing"); got != "" {
		t.Errorf("missing key: %q", got)
	}
	if got := argAnyString(m, "empty"); got != "" {
		t.Errorf("empty string: %q", got)
	}
}

func TestPrettifyToolName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"read_file", "Read File"},
		{"run-command", "Run Command"},
		{"git_status", "Git Status"},
		{"simple", "Simple"},
		{"", ""},
	}
	for _, tt := range tests {
		got := prettifyToolName(tt.input)
		if got != tt.want {
			t.Errorf("prettifyToolName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
