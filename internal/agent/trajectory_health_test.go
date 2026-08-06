package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestTrajectoryHealth_Reset(t *testing.T) {
	s := newTrajectoryHealthState()
	s.recordIteration(3, 1, 5, 2, 2)
	s.warnings = 1
	s.reset()
	if s.iterations != 0 || s.totalEdits != 0 || s.warnings != 0 {
		t.Fatalf("reset did not clear state: %+v", s)
	}
}

func TestTrajectoryHealth_HealthyTrajectory(t *testing.T) {
	s := newTrajectoryHealthState()
	// Simulate a healthy run: 8 iterations, 2 edits, 0 errors, 10 tools, 4 reads, 0 assumptions
	for i := 0; i < 8; i++ {
		s.recordIteration(0, 0, 1, 0, 0)
	}
	s.totalEdits = 2
	s.totalReadTools = 4
	dims, total := s.assess()
	if total >= trajectoryHealthThreshold {
		t.Fatalf("healthy trajectory should not trigger: score=%d dims=%v", total, dims)
	}
}

func TestTrajectoryHealth_MultipleDegradedDimensions(t *testing.T) {
	s := newTrajectoryHealthState()
	// Simulate a degrading trajectory: 6 iterations with sub-threshold
	// signals in multiple dimensions
	// 6 iterations x 3 edits each = 18 edits (ratio 3.0 = score 2)
	// 6 iterations x 1 error each = 6 errors (ratio 1.0 = score 1)
	// 18 tools total, 6 errors = 33% fail rate (score 2)
	// 12/18 read tools = 66% read ratio (score 1)
	// 6 iterations x 1 assumption each = 6 (ratio 1.0 = score 2)
	// Total: 2+1+2+1+2 = 8 >= 5
	for i := 0; i < 6; i++ {
		s.recordIteration(3, 1, 3, 2, 1)
	}
	dims, total := s.assess()
	if total < trajectoryHealthThreshold {
		t.Fatalf("degraded trajectory should trigger: score=%d dims=%d", total, len(dims))
	}
	if len(dims) < 3 {
		t.Fatalf("expected at least 3 degraded dimensions, got %d", len(dims))
	}
}

func TestTrajectoryHealth_AssessEditVolume(t *testing.T) {
	s := newTrajectoryHealthState()
	// 4 iterations x 4 edits = 16 edits, ratio 4.0 = score 2
	for i := 0; i < 4; i++ {
		s.recordIteration(4, 0, 4, 0, 0)
	}
	dims, total := s.assess()
	found := false
	for _, d := range dims {
		if d.name == "edit-volume" && d.score == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected edit-volume score 2, dims=%v total=%d", dims, total)
	}
}

func TestTrajectoryHealth_AssessErrorLoad(t *testing.T) {
	s := newTrajectoryHealthState()
	// 4 iterations x 2 errors = 8 errors, ratio 2.0 = score 2
	for i := 0; i < 4; i++ {
		s.recordIteration(0, 2, 3, 0, 0)
	}
	dims, _ := s.assess()
	found := false
	for _, d := range dims {
		if d.name == "error-load" && d.score == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error-load score 2, dims=%v", dims)
	}
}

func TestTrajectoryHealth_AssessToolFailureRate(t *testing.T) {
	s := newTrajectoryHealthState()
	// 10 tools, 4 errors = 40% fail rate = score 2
	s.iterations = 6
	s.totalTools = 10
	s.totalErrors = 4
	dims, _ := s.assess()
	found := false
	for _, d := range dims {
		if d.name == "tool-failure" && d.score == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected tool-failure score 2, dims=%v", dims)
	}
}

func TestTrajectoryHealth_AssessExploreHeavy(t *testing.T) {
	s := newTrajectoryHealthState()
	// 10 tools, 8 reads = 80% read = score 2
	s.iterations = 6
	s.totalTools = 10
	s.totalReadTools = 8
	dims, _ := s.assess()
	found := false
	for _, d := range dims {
		if d.name == "explore-heavy" && d.score == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected explore-heavy score 2, dims=%v", dims)
	}
}

func TestTrajectoryHealth_AssessAssumptionDensity(t *testing.T) {
	s := newTrajectoryHealthState()
	// 4 iterations x 2 assumptions = 8, ratio 2.0 = score 2
	for i := 0; i < 4; i++ {
		s.recordIteration(0, 0, 1, 0, 2)
	}
	dims, _ := s.assess()
	found := false
	for _, d := range dims {
		if d.name == "assumption-load" && d.score == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected assumption-load score 2, dims=%v", dims)
	}
}

func TestTrajectoryHealth_InsufficientIterations(t *testing.T) {
	s := newTrajectoryHealthState()
	s.recordIteration(10, 10, 10, 10, 10)
	// Only 1 iteration, all dimensions require >= 3 iterations
	dims, total := s.assess()
	if total > 0 {
		t.Fatalf("should not score with insufficient iterations: dims=%v total=%d", dims, total)
	}
}

func TestTrajectoryHealth_InsufficientTools(t *testing.T) {
	s := newTrajectoryHealthState()
	// 5 iterations, but only 5 tools total (< 6 threshold for tool-failure and explore)
	s.iterations = 5
	s.totalTools = 5
	s.totalErrors = 5
	s.totalReadTools = 5
	dims, _ := s.assess()
	for _, d := range dims {
		if d.name == "tool-failure" || d.name == "explore-heavy" {
			t.Fatalf("should not score tool-failure or explore-heavy with < 6 tools: %v", d)
		}
	}
}

func TestMaybeWarnTrajectoryHealth_NoState(t *testing.T) {
	a := &Agent{}
	// trajectoryHealth is nil
	if hint := a.maybeWarnTrajectoryHealth(); hint != "" {
		t.Fatalf("expected empty hint with nil state, got: %s", hint)
	}
}

func TestMaybeWarnTrajectoryHealth_TooFewIterations(t *testing.T) {
	a := &Agent{trajectoryHealth: newTrajectoryHealthState()}
	a.trajectoryHealth.recordIteration(5, 5, 10, 8, 5)
	// Only 1 iteration, min is 5
	if hint := a.maybeWarnTrajectoryHealth(); hint != "" {
		t.Fatalf("expected empty hint with too few iterations, got: %s", hint)
	}
}

func TestMaybeWarnTrajectoryHealth_BelowThreshold(t *testing.T) {
	a := &Agent{trajectoryHealth: newTrajectoryHealthState()}
	// Healthy run
	for i := 0; i < 10; i++ {
		a.trajectoryHealth.recordIteration(0, 0, 2, 0, 0)
	}
	if hint := a.maybeWarnTrajectoryHealth(); hint != "" {
		t.Fatalf("expected empty hint for healthy trajectory, got: %s", hint)
	}
}

func TestMaybeWarnTrajectoryHealth_AboveThreshold(t *testing.T) {
	a := &Agent{trajectoryHealth: newTrajectoryHealthState()}
	// Degrading run: 6 iter x 3 edits/1 error/3 tools/2 reads/1 assumption
	for i := 0; i < 6; i++ {
		a.trajectoryHealth.recordIteration(3, 1, 3, 2, 1)
	}
	hint := a.maybeWarnTrajectoryHealth()
	if hint == "" {
		t.Fatal("expected non-empty hint for degraded trajectory")
	}
	if !strings.Contains(hint, "[trajectory-health]") {
		t.Fatalf("hint should contain tag, got: %s", hint)
	}
	if !strings.Contains(hint, "Composite health score") {
		t.Fatalf("hint should mention composite score, got: %s", hint)
	}
}

func TestMaybeWarnTrajectoryHealth_MaxWarnings(t *testing.T) {
	a := &Agent{trajectoryHealth: newTrajectoryHealthState()}
	a.trajectoryHealth.warnings = trajectoryHealthMaxWarnings
	// Already at max warnings
	for i := 0; i < 6; i++ {
		a.trajectoryHealth.recordIteration(3, 1, 4, 2, 1)
	}
	if hint := a.maybeWarnTrajectoryHealth(); hint != "" {
		t.Fatalf("expected empty hint at max warnings, got: %s", hint)
	}
}

func TestMaybeWarnTrajectoryHealth_FiresTwiceMax(t *testing.T) {
	a := &Agent{trajectoryHealth: newTrajectoryHealthState()}
	// Degrading run: 6 iter x 3 edits/1 error/3 tools/2 reads/1 assumption
	for i := 0; i < 6; i++ {
		a.trajectoryHealth.recordIteration(3, 1, 3, 2, 1)
	}
	// First call fires
	hint1 := a.maybeWarnTrajectoryHealth()
	if hint1 == "" {
		t.Fatal("expected first hint to fire")
	}
	// Second call fires (warnings incremented to 1)
	hint2 := a.maybeWarnTrajectoryHealth()
	if hint2 == "" {
		t.Fatal("expected second hint to fire")
	}
	// Third call should be suppressed (warnings now at 2 = max)
	hint3 := a.maybeWarnTrajectoryHealth()
	if hint3 != "" {
		t.Fatal("expected third hint to be suppressed")
	}
}

func TestCountToolTypes(t *testing.T) {
	calls := []provider.ToolCallDelta{
		{Name: "edit_file"},
		{Name: "read_file"},
		{Name: "grep"},
		{Name: "write_file"},
		{Name: "run_command"},
		{Name: "multi_file_edit"},
		{Name: "list_directory"},
	}
	editCount, readCount := countToolTypes(calls)
	if editCount != 3 {
		t.Fatalf("expected 3 edit tools, got %d", editCount)
	}
	if readCount != 3 {
		t.Fatalf("expected 3 read tools, got %d", readCount)
	}
}

func TestCountToolTypes_Empty(t *testing.T) {
	editCount, readCount := countToolTypes(nil)
	if editCount != 0 || readCount != 0 {
		t.Fatalf("expected 0,0 for nil input, got %d,%d", editCount, readCount)
	}
}
