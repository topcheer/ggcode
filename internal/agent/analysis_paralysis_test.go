package agent

import (
	"strings"
	"testing"
)

func TestAnalysisParalysis_NoWarningEarlyOn(t *testing.T) {
	s := newAnalysisParalysisState()
	for i := 0; i < 7; i++ {
		s.recordCall("read_file")
	}
	if msg := s.check(); msg != "" {
		t.Errorf("expected no warning with only 7 calls, got: %s", msg)
	}
}

func TestAnalysisParalysis_ZeroModifications(t *testing.T) {
	s := newAnalysisParalysisState()
	for i := 0; i < 10; i++ {
		s.recordCall("read_file")
	}
	msg := s.check()
	if msg == "" {
		t.Fatal("expected warning with 10 explore calls and 0 modifications")
	}
	if !strings.Contains(msg, "[Analysis Paralysis]") {
		t.Errorf("expected [Analysis Paralysis] prefix, got: %s", msg)
	}
}

func TestAnalysisParalysis_AllExploreWindow(t *testing.T) {
	s := newAnalysisParalysisState()
	// Mix: 4 modify calls early on, then 12 explore-only
	for i := 0; i < 4; i++ {
		s.recordCall("edit_file")
	}
	for i := 0; i < 12; i++ {
		s.recordCall("grep")
	}
	msg := s.check()
	if msg == "" {
		t.Fatal("expected warning when recent window is all-explore")
	}
}

func TestAnalysisParalysis_ExtremeRatio(t *testing.T) {
	s := newAnalysisParalysisState()
	// 1 modify, 13 explore = 13x ratio
	s.recordCall("edit_file")
	for i := 0; i < 13; i++ {
		s.recordCall("read_file")
	}
	msg := s.check()
	if msg == "" {
		t.Fatal("expected warning for 13x explore-to-modify ratio")
	}
}

func TestAnalysisParalysis_NoFalsePositiveWithBalancedUse(t *testing.T) {
	s := newAnalysisParalysisState()
	for i := 0; i < 6; i++ {
		s.recordCall("read_file")
		s.recordCall("edit_file")
	}
	msg := s.check()
	if msg != "" {
		t.Errorf("expected no warning with balanced usage, got: %s", msg)
	}
}

func TestAnalysisParalysis_MaxWarnings(t *testing.T) {
	s := newAnalysisParalysisState()
	for i := 0; i < 10; i++ {
		s.recordCall("read_file")
	}
	first := s.check()
	if first == "" {
		t.Fatal("expected first warning")
	}
	// After 2 warnings (maxWarnings=2), no more should fire
	for i := 0; i < 10; i++ {
		s.recordCall("grep")
	}
	_ = s.check() // second warning
	// Third check should be suppressed
	for i := 0; i < 10; i++ {
		s.recordCall("grep")
	}
	third := s.check()
	if third != "" {
		t.Errorf("expected no more warnings after maxWarnings reached, got: %s", third)
	}
}

func TestAnalysisParalysis_Reset(t *testing.T) {
	s := newAnalysisParalysisState()
	for i := 0; i < 10; i++ {
		s.recordCall("read_file")
	}
	_ = s.check()
	s.reset()
	if s.totalCalls != 0 || s.exploreCalls != 0 || s.warnCount != 0 {
		t.Errorf("reset did not clear state: %s", s.summary())
	}
}

func TestAnalysisParalysis_VerifyToolsNotFlagged(t *testing.T) {
	s := newAnalysisParalysisState()
	for i := 0; i < 10; i++ {
		s.recordCall("run_command")
	}
	// run_command is classified as verify, not explore
	// verify-only shouldn't trigger (it's action, just not edits)
	msg := s.check()
	// With 10 verify calls and 0 explore calls, the zero-modify + exploreCount>=8
	// condition won't fire because exploreCalls is 0
	if msg != "" {
		t.Errorf("expected no warning for verify-only calls, got: %s", msg)
	}
}

func TestAnalysisParalysis_Summary(t *testing.T) {
	s := newAnalysisParalysisState()
	s.recordCall("read_file")
	s.recordCall("edit_file")
	s.recordCall("run_command")
	summary := s.summary()
	if !strings.Contains(summary, "total=3") {
		t.Errorf("summary should show total=3, got: %s", summary)
	}
	if !strings.Contains(summary, "explore=1") {
		t.Errorf("summary should show explore=1, got: %s", summary)
	}
	if !strings.Contains(summary, "modify=1") {
		t.Errorf("summary should show modify=1, got: %s", summary)
	}
}
