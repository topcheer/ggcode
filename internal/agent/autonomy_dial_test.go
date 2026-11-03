package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestAutonomyDial_PromotionSuggestion(t *testing.T) {
	s := newAutonomyDialState()
	for i := 0; i < autonomyPromoteStreak-1; i++ {
		s.record(false, "ok")
	}
	if got := s.suggest(); got != "" {
		t.Fatalf("no suggestion before the streak threshold, got %q", got)
	}
	s.record(false, "ok")
	got := s.suggest()
	if !strings.Contains(got, "autonomy-dial") || !strings.Contains(got, "switch_mode") {
		t.Fatalf("expected promotion suggestion, got %q", got)
	}

	if again := s.suggest(); again != "" {
		t.Fatalf("promotion suggestion must be one-shot, got %q", again)
	}
}

func TestAutonomyDial_DenialsBlockPromotionAndSugggerstDemotion(t *testing.T) {
	s := newAutonomyDialState()
	for i := 0; i < autonomyPromoteStreak+5; i++ {
		s.record(false, "ok")
	}

	s.record(true, "Error: permission denied by policy for git push")
	if s.consecutiveOK != 0 {
		t.Fatalf("denial must reset consecutiveOK, got %d", s.consecutiveOK)
	}
	if s.denyCount != 1 {
		t.Fatalf("denial must be counted, got %d", s.denyCount)
	}

	for i := 1; i < autonomyDemoteDenies; i++ {
		s.record(true, "user rejected the approval request")
	}
	got := s.suggest()
	if !strings.Contains(got, "denials") {
		t.Fatalf("expected demotion suggestion after %d denials, got %q", autonomyDemoteDenies, got)
	}
	if again := s.suggest(); again != "" {
		t.Fatalf("demotion suggestion must be one-shot, got %q", again)
	}
}

func TestAutonomyDial_ErrStreakSuggestsDemotion(t *testing.T) {
	s := newAutonomyDialState()
	for i := 0; i < autonomyDemoteErrStreak; i++ {
		s.record(true, "exit status 1: build failed")
	}
	got := s.suggest()
	if !strings.Contains(got, "consecutive failed tool calls") {
		t.Fatalf("expected err-streak demotion, got %q", got)
	}
}

func TestAutonomyDial_CleanSuccessWithDenialWordIsNotDenial(t *testing.T) {
	s := newAutonomyDialState()

	s.record(false, "internal/agent/permission_guard.go: permission denied messages live here")
	if s.denyCount != 0 {
		t.Fatalf("clean success must not count as denial, denyCount=%d", s.denyCount)
	}
	if s.consecutiveOK != 1 {
		t.Fatalf("clean success must extend streak, got %d", s.consecutiveOK)
	}
}

func TestAutonomyDial_ResetReopensWindow(t *testing.T) {
	s := newAutonomyDialState()
	for i := 0; i < autonomyPromoteStreak; i++ {
		s.record(false, "ok")
	}
	s.reset()
	if s.consecutiveOK != 0 || s.denyCount != 0 || s.errStreak != 0 {
		t.Fatalf("reset must clear counters: %+v", s)
	}

	s.record(true, "permission denied")
	s.record(true, "permission denied")
	s.record(true, "permission denied")
	if got := s.suggest(); !strings.Contains(got, "denials") {
		t.Fatalf("fresh window must be able to re-suggest, got %q", got)
	}
}

func TestAutonomyDial_ErrorInterruptsStreak(t *testing.T) {
	s := newAutonomyDialState()
	for i := 0; i < autonomyPromoteStreak-1; i++ {
		s.record(false, "ok")
	}
	s.record(true, "file not found")
	if s.consecutiveOK != 0 || s.errStreak != 1 {
		t.Fatalf("error must reset streak and count errStreak: %+v", s)
	}

	if got := s.suggest(); got != "" {
		t.Fatalf("broken streak must not suggest promotion, got %q", got)
	}
}

func TestAutonomyDial_ModeTransitionResetsWindow(t *testing.T) {
	mp := &mockProvider{}
	a := NewAgent(mp, tool.NewRegistry(), "", 1)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutoMode))

	for i := 0; i < autonomyPromoteStreak; i++ {
		a.autonomyDial.record(false, "ok")
	}
	if a.autonomyDial.consecutiveOK != autonomyPromoteStreak {
		t.Fatalf("setup: streak not recorded, got %d", a.autonomyDial.consecutiveOK)
	}

	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.SupervisedMode))
	if a.autonomyDial.consecutiveOK != 0 || a.autonomyDial.suggestedUp {
		t.Fatalf("mode transition must reset the dial window: %+v", a.autonomyDial)
	}
}
