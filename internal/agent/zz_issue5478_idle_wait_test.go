package agent

import (
	"strings"
	"testing"
)

// #5478 residual: idle-wait actions must not dilute the grounding window.
// Field evidence (watchdog seat, 8 patrol rounds): every round was a
// sleep -> read-only sweep; each sleep appended `false` to the window,
// evicted the round's read credit and fired the "0 grounding" banner
// (100% false positives).

// 1. Watchdog patrol shape: sleep rounds never evict read credit and
// never occupy a window slot.
func TestIssue5478IdleWaitDoesNotEvictGroundingCredit(t *testing.T) {
	s := newGateForTest()
	s.recordAction("read_file", `{}`)
	s.recordAction("grep", `{"pattern":"x"}`)
	for i := 0; i < 6; i++ { // overflow the 5-slot window with sleeps
		s.recordAction("sleep", `{"seconds":60}`)
	}
	if len(s.grounding) != 2 {
		t.Fatalf("sleep must not append to the window, len=%d", len(s.grounding))
	}
	// The two read credits must still satisfy the MEDIUM threshold.
	if w := s.recordAction("git_commit", `{"message":"x"}`); w != "" {
		t.Fatalf("sleep must not evict grounding credit, got gate: %q", w)
	}
}

// 2. The observed false positive: sleep-only history then a medium
// action used to gate with the "0 grounding" banner. Post-fix the window
// stays empty, so the #5129 first-action exemption applies (git_commit
// is a non-shell medium tool).
func TestIssue5478SleepOnlyHistoryNoZeroGroundingBanner(t *testing.T) {
	s := newGateForTest()
	for i := 0; i < 3; i++ {
		s.recordAction("sleep", `{"seconds":60}`)
	}
	if w := s.recordAction("git_commit", `{"message":"x"}`); w != "" {
		t.Fatalf("expected first-action exemption (window untouched by sleep), got gate: %q", w)
	}
}

// 3. The full wait family shares the exemption.
func TestIssue5478WaitFamilyExempt(t *testing.T) {
	for _, tool := range []string{"wait_agent", "wait_command"} {
		s := newGateForTest()
		s.recordAction("read_file", `{}`)
		s.recordAction(tool, `{"agent_id":"x","wait_seconds":30}`)
		if len(s.grounding) != 1 {
			t.Fatalf("%s must not append, len=%d", tool, len(s.grounding))
		}
		if w := s.recordAction("git_commit", `{"message":"x"}`); w != "" {
			t.Fatalf("%s must not evict grounding credit, got: %q", tool, w)
		}
	}
}

// 4. Guard against over-broad exemption: non-idle, non-grounding
// actions (LOW-tier edits) still dilute the window and gate a follow-up
// medium action exactly as before.
func TestIssue5478NonIdleStillDilutes(t *testing.T) {
	s := newGateForTest()
	for i := 0; i < 5; i++ {
		s.recordAction("edit_file", `{"file_path":"/tmp/f.go","old_text":"a","new_text":"b"}`) // LOW tier, non-grounding
	}
	w := s.recordAction("git_commit", `{"message":"x"}`)
	if w == "" || !strings.Contains(w, "grounding") {
		t.Fatalf("non-idle actions must still dilute the window and gate, got: %q", w)
	}
}
