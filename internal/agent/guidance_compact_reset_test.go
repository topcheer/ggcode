package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

// Batch 2 of the guidance-noise cleanup: B-class detectors inject at most
// once per run, and a successful context compaction resets the injection
// counters (the injected guidance text was compacted away with the rest of
// the context). These tests pin both halves of that contract.

func TestGuidanceCountersResetAfterCompaction(t *testing.T) {
	a := NewAgent(nil, tool.NewRegistry(), "", 5)

	// Burn the once-per-run quota of verify_debt.
	a.verifyDebt.mu.Lock()
	a.verifyDebt.warningsIssued = 1
	a.verifyDebt.mu.Unlock()
	a.errorRush.warnCount = 1
	a.queryConverge.mu.Lock()
	a.queryConverge.warnCount = 1
	a.queryConverge.warned = true
	a.queryConverge.mu.Unlock()

	a.resetGuidanceCounters()

	a.verifyDebt.mu.Lock()
	got := a.verifyDebt.warningsIssued
	a.verifyDebt.mu.Unlock()
	if got != 0 {
		t.Errorf("verifyDebt.warningsIssued = %d after reset, want 0", got)
	}
	if a.errorRush.warnCount != 0 {
		t.Errorf("errorRush.warnCount = %d after reset, want 0", a.errorRush.warnCount)
	}
	a.queryConverge.mu.Lock()
	qc, qw := a.queryConverge.warnCount, a.queryConverge.warned
	a.queryConverge.mu.Unlock()
	if qc != 0 || qw {
		t.Errorf("queryConverge = (%d, %t) after reset, want (0, false)", qc, qw)
	}
}

func TestGuidanceCountersResetKeepsBehavioralWindows(t *testing.T) {
	a := NewAgent(nil, tool.NewRegistry(), "", 5)

	// Behavioral state that must survive the counter reset.
	a.verifyDebt.mu.Lock()
	a.verifyDebt.editsSinceGreen = 7
	a.verifyDebt.mu.Unlock()

	a.resetGuidanceCounters()

	a.verifyDebt.mu.Lock()
	debt, warns := a.verifyDebt.editsSinceGreen, a.verifyDebt.warningsIssued
	a.verifyDebt.mu.Unlock()
	if debt != 7 {
		t.Errorf("editsSinceGreen = %d after reset, want 7 (behavioral window must survive)", debt)
	}
	if warns != 0 {
		t.Errorf("warningsIssued = %d after reset, want 0", warns)
	}
}

func TestBClassDetectorsOncePerRun(t *testing.T) {
	// verify_debt: first warning fires, second is suppressed.
	s := newVerifyDebtState()
	for i := 0; i < verifyDebtWarn1; i++ {
		s.recordSourceEdit()
	}
	if msg := s.maybeWarn(1); msg == "" {
		t.Fatal("expected first verify_debt warning")
	}
	for i := 0; i < 5; i++ {
		s.recordSourceEdit()
	}
	if msg := s.maybeWarn(2); msg != "" {
		t.Fatalf("expected second verify_debt warning to be suppressed (1/run), got: %s", msg)
	}

	// attention_fragment: same contract via analyze().
	af := newAttentionFragmentState()
	// 10 unique dirs, 100% switch density (mirrors the canonical test).
	for i := 0; i < 10; i++ {
		d := "/proj/internal/d" + string(rune('0'+i)) + "/file.go"
		af.recordToolCall("read_file", map[string]interface{}{"file_path": d})
	}
	first := af.analyze()
	if !strings.Contains(first, "[attention-fragment") {
		t.Fatalf("expected attention_fragment warning, got: %q", first)
	}
	for i := 0; i < 4; i++ {
		d := "/proj/internal/x" + string(rune('0'+i)) + "/file.go"
		af.recordToolCall("read_file", map[string]interface{}{"file_path": d})
	}
	if msg := af.analyze(); msg != "" {
		t.Fatalf("expected second attention_fragment warning to be suppressed (1/run), got: %s", msg)
	}
}
