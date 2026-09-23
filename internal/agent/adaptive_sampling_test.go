package agent

import (
	"testing"
)

func TestAdaptiveSampling_PhaseClassification(t *testing.T) {
	tests := []struct {
		name      string
		entries   []effortEntry
		wantPhase samplingPhase
	}{
		{
			name:      "empty window → phaseNone",
			entries:   []effortEntry{},
			wantPhase: phaseNone,
		},
		{
			name: "all reads → exploration",
			entries: []effortEntry{
				{toolName: "read_file", isError: false},
				{toolName: "grep", isError: false},
				{toolName: "list_directory", isError: false},
				{toolName: "search_files", isError: false},
			},
			wantPhase: phaseExploration,
		},
		{
			name: "edits → codeEdit",
			entries: []effortEntry{
				{toolName: "read_file", isError: false},
				{toolName: "edit_file", isError: false},
				{toolName: "edit_file", isError: false},
			},
			wantPhase: phaseCodeEdit,
		},
		{
			name: "edit-family errors → errorRecovery",
			entries: []effortEntry{
				{toolName: "edit_file", isError: true},
				{toolName: "write_file", isError: true},
				{toolName: "read_file", isError: false},
			},
			wantPhase: phaseErrorRecovery,
		},
		{
			// #2636: read-only exploration misses (grep no-match, file not
			// found, LSP not ready) are routine exploration results, not
			// recovery puzzles - they must NOT pin temperature at 0.0.
			name: "read-only errors → exploration, not errorRecovery",
			entries: []effortEntry{
				{toolName: "grep", isError: true},
				{toolName: "grep", isError: true},
				{toolName: "read_file", isError: false},
				{toolName: "read_file", isError: false},
			},
			wantPhase: phaseExploration,
		},
		{
			// #2636: build/test failures via run_command are excluded from
			// error-recovery here too, mirroring effort-side #1436-A.
			name: "run_command errors alone → not errorRecovery",
			entries: []effortEntry{
				{toolName: "run_command", isError: true},
				{toolName: "run_command", isError: true},
				{toolName: "run_command", isError: false},
			},
			wantPhase: phaseNone,
		},
		{
			name: "creative → creative",
			entries: []effortEntry{
				{toolName: "git_commit", isError: false},
				{toolName: "git_commit", isError: false},
				{toolName: "read_file", isError: false},
				{toolName: "read_file", isError: false},
			},
			wantPhase: phaseCreative,
		},
		{
			name: "errors dominate edits",
			entries: []effortEntry{
				{toolName: "edit_file", isError: true},
				{toolName: "edit_file", isError: true},
				{toolName: "edit_file", isError: false},
			},
			wantPhase: phaseErrorRecovery,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newAdaptiveSamplingState()
			s.entries = tt.entries
			got := s.classifyPhase()
			if got != tt.wantPhase {
				t.Errorf("classifyPhase() = %v, want %v", got, tt.wantPhase)
			}
		})
	}
}

func TestAdaptiveSampling_RecommendedTemperature(t *testing.T) {
	tests := []struct {
		name     string
		entries  []effortEntry
		wantTemp float64
	}{
		{"empty → no adjustment", []effortEntry{}, -1},
		{"exploration", []effortEntry{
			{toolName: "read_file", isError: false},
			{toolName: "grep", isError: false},
			{toolName: "list_directory", isError: false},
			{toolName: "code_search", isError: false},
		}, tempExploration},
		{"code edit", []effortEntry{
			{toolName: "read_file", isError: false},
			{toolName: "edit_file", isError: false},
			{toolName: "edit_file", isError: false},
		}, tempCodeEdit},
		{"error recovery", []effortEntry{
			{toolName: "edit_file", isError: true},
			{toolName: "write_file", isError: true},
		}, tempErrorRecover},
		{"creative", []effortEntry{
			{toolName: "git_commit", isError: false},
			{toolName: "git_commit", isError: false},
			{toolName: "read_file", isError: false},
			{toolName: "read_file", isError: false},
		}, tempCreative},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newAdaptiveSamplingState()
			s.entries = tt.entries
			got := s.recommendedTemperature()
			if got != tt.wantTemp {
				t.Errorf("recommendedTemperature() = %.2f, want %.2f", got, tt.wantTemp)
			}
		})
	}
}

func TestAdaptiveSampling_UserOverride(t *testing.T) {
	s := newAdaptiveSamplingState()
	s.entries = []effortEntry{
		{toolName: "read_file", isError: false},
		{toolName: "read_file", isError: false},
		{toolName: "read_file", isError: false},
		{toolName: "read_file", isError: false},
	}

	// Without override, should recommend exploration temperature.
	if temp := s.recommendedTemperature(); temp != tempExploration {
		t.Fatalf("expected temp %.2f without override, got %.2f", tempExploration, temp)
	}

	// With override, should not recommend.
	s.setUserOverride(true)
	if temp := s.recommendedTemperature(); temp != -1 {
		t.Fatalf("expected -1 with user override, got %.2f", temp)
	}

	// Remove override, should recommend again.
	s.setUserOverride(false)
	if temp := s.recommendedTemperature(); temp != tempExploration {
		t.Fatalf("expected temp %.2f after removing override, got %.2f", tempExploration, temp)
	}
}

func TestAdaptiveSampling_SlidingWindow(t *testing.T) {
	s := newAdaptiveSamplingState()

	// Fill beyond window size.
	for i := 0; i < adaptiveSamplingWindow+5; i++ {
		s.recordToolResultErr("read_file", false, "")
	}

	if len(s.entries) != adaptiveSamplingWindow {
		t.Errorf("expected window size %d, got %d", adaptiveSamplingWindow, len(s.entries))
	}
}

func TestAdaptiveSampling_Reset(t *testing.T) {
	s := newAdaptiveSamplingState()
	s.recordToolResultErr("edit_file", false, "")
	s.recordToolResultErr("read_file", false, "")

	s.reset()
	if len(s.entries) != 0 {
		t.Errorf("expected empty entries after reset, got %d", len(s.entries))
	}
}

// mockSamplingProvider is a minimal provider that satisfies
// SamplingConfigProvider for testing applyAdaptiveSampling.
type mockSamplingProvider struct {
	temp float64
}

func (m *mockSamplingProvider) SetTemperature(temp float64) { m.temp = temp }
func (m *mockSamplingProvider) Temperature() float64        { return m.temp }
func (m *mockSamplingProvider) SetTopP(topP float64)        {}
func (m *mockSamplingProvider) TopP() float64               { return 0 }

func TestApplyAdaptiveSampling_NoProvider(t *testing.T) {
	a := &Agent{}
	applied, prev := a.applyAdaptiveSampling()
	if applied != -1 {
		t.Errorf("expected -1 when no adaptiveSampling state, got %.2f", applied)
	}
	if prev != 0 {
		t.Errorf("expected prev 0, got %.2f", prev)
	}
}
