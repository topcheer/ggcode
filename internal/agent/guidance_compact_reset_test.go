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

	// Burn the once-per-run quota of verification_debt (verifyDebt was
	// consolidated into verifDebt in sa-34; no mutex - single-goroutine).
	a.verifDebt.warningsIssued = 1
	a.errorRush.warnCount = 1
	a.queryConverge.mu.Lock()
	a.queryConverge.warnCount = 1
	a.queryConverge.warned = true
	a.queryConverge.mu.Unlock()

	a.resetGuidanceCounters()

	got := a.verifDebt.warningsIssued
	if got != 0 {
		t.Errorf("verifDebt.warningsIssued = %d after reset, want 0", got)
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
	a.verifDebt.debt = 7

	a.resetGuidanceCounters()

	debt, warns := a.verifDebt.debt, a.verifDebt.warningsIssued
	if debt != 7 {
		t.Errorf("debt = %d after reset, want 7 (behavioral window must survive)", debt)
	}
	if warns != 0 {
		t.Errorf("warningsIssued = %d after reset, want 0", warns)
	}
}

func TestBClassDetectorsOncePerRun(t *testing.T) {
	// verification_debt (sa-34 consolidation): warnings cap at 2/run.
	s := newVerificationDebtState()
	for i := 0; i < 10; i++ {
		s.recordToolCall("edit_file", `{"path":"f.go"}`, false)
	}
	if msg := s.maybeWarn(); msg == "" {
		t.Fatal("expected first verification_debt warning")
	}
	if msg := s.maybeWarn(); msg == "" {
		t.Fatal("expected second verification_debt warning")
	}
	if msg := s.maybeWarn(); msg != "" {
		t.Fatalf("expected third verification_debt warning to be suppressed (2/run), got: %s", msg)
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

// #1651: the enumerative gap closed - sample pins across both groups
// (int counter, quota map, quota bools) must all reset.
func TestResetGuidanceCounters1651Extension(t *testing.T) {
	a := &Agent{
		trajectoryHealth:   &trajectoryHealthState{warnings: 2},
		planAbandon:        &planAbandonState{warnings: 1},
		delegationOrch:     &delegationState{orphanWarnCount: 1, serialWarnCount: 2, overDelWarned: true},
		fixAmnesia:         newFixAmnesiaState(),
		heterogeneousModel: &heterogeneousModelState{warnsIssued: 1},
		driftRecurrence:    &driftRecurrenceState{fired: true, warned: true},
		crossFileImpact:    &crossFileImpactState{fired: true},
		serialRead:         &serialReadState{fired: true},
		diskSpace:          &diskSpaceState{fired: true},
	}
	a.fixAmnesia.mu.Lock()
	a.fixAmnesia.warned["build"] = true
	a.fixAmnesia.mu.Unlock()
	a.resetGuidanceCounters()
	if a.trajectoryHealth.warnings != 0 || a.planAbandon.warnings != 0 {
		t.Fatal("int counters must reset")
	}
	if a.delegationOrch.orphanWarnCount != 0 || a.delegationOrch.serialWarnCount != 0 || a.delegationOrch.overDelWarned {
		t.Fatal("delegation quotas must reset")
	}
	if len(a.fixAmnesia.warned) != 0 {
		t.Fatal("fixAmnesia warned map must clear")
	}
	if a.heterogeneousModel.warnsIssued != 0 {
		t.Fatal("heterogeneousModel quota must reset")
	}
	if a.driftRecurrence.fired || a.driftRecurrence.warned || a.crossFileImpact.fired || a.serialRead.fired || a.diskSpace.fired {
		t.Fatal("quota bools must reset")
	}
}

// #1646: the compaction reset must reopen the RIGHT debt detector -
// verifDebt (maxWarn=2, the SAUP model the #1605 commit message named)
// and overcorrection_cascade (maxWarn=2), not a byte-identical duplicate
// of the verifyDebt (max=1) block above.
func TestResetGuidanceCounters1646RightTracker(t *testing.T) {
	a := &Agent{
		verifDebt:      &verificationDebtState{warningsIssued: 2},
		overcorrection: &overcorrectionState{warnCount: 2},
	}
	a.resetGuidanceCounters()
	if a.verifDebt.warningsIssued != 0 {
		t.Fatal("verifDebt.warningsIssued must reset (the #1605-A no-op fixed)")
	}
	if a.overcorrection.warnCount != 0 {
		t.Fatal("overcorrection warnCount must reset (#1646-2)")
	}
}
