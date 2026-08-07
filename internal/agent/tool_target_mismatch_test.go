package agent

import (
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestExtractStatedTargets(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{
			name: "file read intent",
			text: "I'll read internal/agent/agent.go to understand the structure.",
			want: 1,
		},
		{
			name: "search intent with quotes",
			text: `Let me search for "authenticationHandler" in the codebase.`,
			want: 2, // quoted term regex + intent regex both capture it
		},
		{
			name: "multiple intents",
			text: "I'll read config.yaml and then I'll edit internal/agent/foo.go",
			want: 2,
		},
		{
			name: "no intent",
			text: "The file has been updated successfully.",
			want: 0,
		},
		{
			name: "grep intent",
			text: "Let me grep for TODO patterns in the source.",
			want: 1, // "TODO" is correctly extracted as search target
		},
		{
			name: "edit intent with path",
			text: "I'll edit src/main.go to fix the import.",
			want: 1,
		},
		{
			name: "check file intent",
			text: "Let me check internal/config/config.go",
			want: 1,
		},
		{
			name: "empty text",
			text: "",
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractStatedTargets(tt.text)
			if len(got) != tt.want {
				t.Errorf("extractStatedTargets(%q) got %d targets, want %d: %+v", tt.text, len(got), tt.want, got)
			}
		})
	}
}

func TestExtractActualToolTargets(t *testing.T) {
	tests := []struct {
		name      string
		toolCalls []provider.ToolCallDelta
		wantCount int
	}{
		{
			name: "read_file with path",
			toolCalls: []provider.ToolCallDelta{
				{Name: "read_file", Arguments: json.RawMessage(`{"path":"/internal/agent/agent.go"}`)},
			},
			wantCount: 1,
		},
		{
			name: "grep with pattern",
			toolCalls: []provider.ToolCallDelta{
				{Name: "grep", Arguments: json.RawMessage(`{"pattern":"TODO"}`)},
			},
			wantCount: 1,
		},
		{
			name: "edit_file with path and old_text",
			toolCalls: []provider.ToolCallDelta{
				{Name: "edit_file", Arguments: json.RawMessage(`{"file_path":"src/main.go","old_text":"package main\nfunc main() {"}`)},
			},
			wantCount: 1,
		},
		{
			name: "empty arguments",
			toolCalls: []provider.ToolCallDelta{
				{Name: "read_file", Arguments: json.RawMessage(`{}`)},
			},
			wantCount: 0,
		},
		{
			name:      "no tool calls",
			toolCalls: nil,
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractActualToolTargets(tt.toolCalls)
			if len(got) != tt.wantCount {
				t.Errorf("got %d tool targets, want %d", len(got), tt.wantCount)
			}
		})
	}
}

func TestCheckMismatch(t *testing.T) {
	s := &toolTargetState{}

	// Match: stated target matches actual target
	hint := s.checkMismatch(
		[]statedTarget{{intentType: "file", value: "internal/agent/agent.go"}},
		[]toolTarget{{toolName: "read_file", targets: []string{"/internal/agent/agent.go"}}},
	)
	if hint != "" {
		t.Errorf("expected no mismatch for matching targets, got: %s", hint)
	}

	// Mismatch: stated target differs from actual
	hint = s.checkMismatch(
		[]statedTarget{{intentType: "file", value: "internal/agent/agent.go"}},
		[]toolTarget{{toolName: "read_file", targets: []string{"config.yaml"}}},
	)
	if hint == "" {
		t.Error("expected mismatch for different targets, got empty hint")
	}

	// Basename match: "agent.go" matches "internal/agent/agent.go"
	hint = s.checkMismatch(
		[]statedTarget{{intentType: "file", value: "agent.go"}},
		[]toolTarget{{toolName: "read_file", targets: []string{"internal/agent/agent.go"}}},
	)
	if hint != "" {
		t.Errorf("expected no mismatch for basename match, got: %s", hint)
	}

	// Empty inputs
	hint = s.checkMismatch(nil, nil)
	if hint != "" {
		t.Errorf("expected empty hint for nil inputs, got: %s", hint)
	}

	// Empty actual targets
	hint = s.checkMismatch(
		[]statedTarget{{intentType: "file", value: "foo.go"}},
		nil,
	)
	if hint != "" {
		t.Errorf("expected empty hint for nil actual targets, got: %s", hint)
	}
}

func TestMaybeWarnToolTargetMismatch(t *testing.T) {
	a := &Agent{toolTargetMismatch: &toolTargetState{}}

	// Mismatch detected
	hint := a.maybeWarnToolTargetMismatch(
		"I'll read internal/agent/agent.go to understand the structure.",
		[]provider.ToolCallDelta{
			{Name: "read_file", Arguments: json.RawMessage(`{"path":"config.yaml"}`)},
		},
	)
	if hint == "" {
		t.Error("expected mismatch warning, got empty")
	}

	// No mismatch
	hint = a.maybeWarnToolTargetMismatch(
		"I'll read internal/agent/agent.go to understand the structure.",
		[]provider.ToolCallDelta{
			{Name: "read_file", Arguments: json.RawMessage(`{"path":"internal/agent/agent.go"}`)},
		},
	)
	if hint != "" {
		t.Errorf("expected no warning for matching targets, got: %s", hint)
	}

	// Nil state
	a2 := &Agent{}
	hint = a2.maybeWarnToolTargetMismatch("I'll read foo.go", nil)
	if hint != "" {
		t.Errorf("expected empty hint for nil state, got: %s", hint)
	}

	// Max warnings reached
	a3 := &Agent{toolTargetMismatch: &toolTargetState{warnings: toolTargetMaxWarnings}}
	hint = a3.maybeWarnToolTargetMismatch(
		"I'll read foo.go",
		[]provider.ToolCallDelta{
			{Name: "read_file", Arguments: json.RawMessage(`{"path":"bar.go"}`)},
		},
	)
	if hint != "" {
		t.Errorf("expected empty hint when max warnings reached, got: %s", hint)
	}
}

func TestTargetsMatch(t *testing.T) {
	tests := []struct {
		stated string
		actual string
		want   bool
	}{
		{"agent.go", "internal/agent/agent.go", true},
		{"internal/agent/agent.go", "internal/agent/agent.go", true},
		{"config.yaml", "config.yaml", true},
		{"agent.go", "config.yaml", false},
		{"internal/foo", "internal/agent/agent.go", false},
		{"agent", "internal/agent/agent.go", true}, // substring
	}

	for _, tt := range tests {
		got := targetsMatch(tt.stated, tt.actual)
		if got != tt.want {
			t.Errorf("targetsMatch(%q, %q) = %v, want %v", tt.stated, tt.actual, got, tt.want)
		}
	}
}

func TestIsFalsePositiveTarget(t *testing.T) {
	trueCases := []string{"the", "a", "an", "this", "that", "it"}
	for _, s := range trueCases {
		if !isFalsePositiveTarget(s) {
			t.Errorf("isFalsePositiveTarget(%q) = false, want true", s)
		}
	}
	falseCases := []string{"agent.go", "config.yaml", "internal", "foo"}
	for _, s := range falseCases {
		if isFalsePositiveTarget(s) {
			t.Errorf("isFalsePositiveTarget(%q) = true, want false", s)
		}
	}
}
