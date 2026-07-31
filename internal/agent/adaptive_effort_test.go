package agent

import (
	"testing"
)

func TestAdaptiveEffortRecommendedEffort(t *testing.T) {
	tests := []struct {
		name     string
		entries  []effortEntry
		override bool
		want     string
	}{
		{
			name:    "empty window returns empty",
			entries: nil,
			want:    "",
		},
		{
			name: "user override disables adaptation",
			entries: []effortEntry{
				{toolName: "read_file", isError: false},
			},
			override: true,
			want:     "",
		},
		{
			name: "only read-only tools → low",
			entries: []effortEntry{
				{toolName: "read_file", isError: false},
				{toolName: "grep", isError: false},
				{toolName: "list_directory", isError: false},
			},
			want: "low",
		},
		{
			name: "edit tools → medium",
			entries: []effortEntry{
				{toolName: "read_file", isError: false},
				{toolName: "edit_file", isError: false},
			},
			want: "medium",
		},
		{
			name: "error → high",
			entries: []effortEntry{
				{toolName: "edit_file", isError: true},
			},
			want: "high",
		},
		{
			name: "error takes priority over edits",
			entries: []effortEntry{
				{toolName: "edit_file", isError: false},
				{toolName: "edit_file", isError: true},
				{toolName: "write_file", isError: false},
			},
			want: "high",
		},
		{
			name: "mixed unknown tools returns empty",
			entries: []effortEntry{
				{toolName: "some_unknown_tool", isError: false},
			},
			want: "",
		},
		{
			name: "unknown tool mixed with read-only returns empty (not all read-only)",
			entries: []effortEntry{
				{toolName: "read_file", isError: false},
				{toolName: "unknown_tool", isError: false},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newAdaptiveEffortState()
			s.setUserOverride(tt.override)
			for _, e := range tt.entries {
				s.recordToolResult(e.toolName, e.isError)
			}
			got := s.recommendedEffort()
			if got != tt.want {
				t.Errorf("recommendedEffort() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAdaptiveEffortWindowSlide(t *testing.T) {
	s := newAdaptiveEffortState()

	// Fill with read-only tools (would recommend "low")
	for i := 0; i < adaptiveEffortWindow; i++ {
		s.recordToolResult("read_file", false)
	}
	if got := s.recommendedEffort(); got != "low" {
		t.Fatalf("expected low effort for read-only window, got %q", got)
	}

	// Add an error — window slides, old read-only entries drop off
	for i := 0; i < adaptiveEffortWindow; i++ {
		s.recordToolResult("edit_file", true)
	}
	if got := s.recommendedEffort(); got != "high" {
		t.Fatalf("expected high effort after errors slide in, got %q", got)
	}
}

func TestAdaptiveEffortReset(t *testing.T) {
	s := newAdaptiveEffortState()
	s.recordToolResult("read_file", false)
	s.recordToolResult("edit_file", true)

	s.reset()

	if got := s.recommendedEffort(); got != "" {
		t.Errorf("after reset, recommendedEffort() = %q, want empty", got)
	}
}

func TestAdaptiveEffortUserOverrideClears(t *testing.T) {
	s := newAdaptiveEffortState()
	s.setUserOverride(true)
	s.recordToolResult("read_file", false)

	if got := s.recommendedEffort(); got != "" {
		t.Errorf("with override, recommendedEffort() = %q, want empty", got)
	}

	// Clearing override re-enables adaptation
	s.setUserOverride(false)
	if got := s.recommendedEffort(); got != "low" {
		t.Errorf("after clearing override, recommendedEffort() = %q, want low", got)
	}
}

func TestEffortLevelDisplayName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"low", "Low (fast)"},
		{"medium", "Medium (balanced)"},
		{"high", "High (thorough)"},
		{"", "Auto"},
		{"unknown", "Auto"},
	}
	for _, tt := range tests {
		got := effortLevelDisplayName(tt.input)
		if got != tt.want {
			t.Errorf("effortLevelDisplayName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
