package agent

import "testing"

// #1843 case 1: foresightCalib quota must be per-RUN (user turn), not
// per-Agent-lifetime - reset() re-opens the mismatch/warn quotas.
func TestForesightResetReopensQuota1843(t *testing.T) {
	s := newForesightCalibrateState()
	s.mismatches = 5
	s.warnCount = 2 // both lifetime warnings burned
	s.reset()
	if s.mismatches != 0 || s.warnCount != 0 {
		t.Fatalf("reset must clear mismatches/warnCount, got %d/%d", s.mismatches, s.warnCount)
	}
}

// #1843 case 2: attention fragment warnCount resets per user turn -
// afMaxWarnings=1 was one warning per Agent LIFETIME before.
func TestAttentionFragmentWarnQuotaPerTurn1843(t *testing.T) {
	s := newAttentionFragmentState()
	s.warnCount = 1
	s.reset()
	if s.warnCount != 0 {
		t.Fatalf("warnCount must reset per user turn, got %d", s.warnCount)
	}
}

// #1843 case 3: compaction must re-open the editOscillation quota
// (max=1 burned pre-compaction left it silent for the whole run).
func TestEditOscillationCompactionReopen1843(t *testing.T) {
	var a Agent
	a.editOscillation = newOscillationState()
	a.editOscillation.fired = 1
	a.resetGuidanceCounters()
	if a.editOscillation.fired != 0 {
		t.Fatalf("compaction must reopen editOscillation quota, fired=%d", a.editOscillation.fired)
	}
}
