package agent

// zz_issue4224_test.go pins the delivered-gated quota semantics for the five
// guidanceEmit-family detectors identified in #4224: a quota consumed by
// maybeWarn/maybeBrief/endTurn must roll back via markUndelivered when the
// guidance budget suppressed the message, so the detector re-fires on a later,
// less saturated iteration instead of going permanently dark (mirrors the
// #3380 pattern: queryConverge/reasoningRedund/bareEditStreak/futileCycle).

import (
	"strings"
	"testing"
)

func TestIssue4224SubgoalTrackMarkUndelivered(t *testing.T) {
	s := newSubgoalState()
	s.planIter = 0
	for i := 1; i <= sgMinSubgoals; i++ {
		s.subgoals = append(s.subgoals, subgoalEntry{
			number:   i,
			text:     "do step " + string(rune('a'+i)),
			keywords: []string{"step"},
		})
	}
	// 3/3 unaddressed (ratio 1.0 >= sgUnaddressedThresh), iter gap satisfied.
	if msg := s.maybeWarn(sgMinIterGap + 1); msg == "" {
		t.Fatal("expected first fire")
	}
	if !s.fired {
		t.Fatal("fired flag must be set after fire")
	}
	if again := s.maybeWarn(sgMinIterGap + 2); again != "" {
		t.Fatalf("one-shot quota must suppress re-fire, got %q", again)
	}
	// Budget suppressed delivery: rollback must restore the one-shot chance.
	s.markUndelivered()
	if s.fired {
		t.Fatal("markUndelivered must clear fired")
	}
	if msg := s.maybeWarn(sgMinIterGap + 3); msg == "" {
		t.Fatal("expected re-fire after rollback")
	}
	// Delivered fire: the caller contract never calls markUndelivered again,
	// so the fired flag stays consumed for the rest of the run.
	if !s.fired {
		t.Fatal("delivered fire must keep the quota consumed")
	}
}

func TestIssue4224SerialReadMarkUndelivered(t *testing.T) {
	s := newSerialReadState()
	var firedAt int
	for turn := 1; turn <= 3; turn++ {
		s.recordToolCall("read_file")
		if msg := s.endTurn(turn); msg != "" && turn < 3 {
			t.Fatalf("unexpected early fire at turn %d", turn)
		} else if msg != "" && turn == 3 {
			firedAt = turn
		}
	}
	if firedAt != 3 || !s.fired {
		t.Fatalf("expected fire at 3rd consecutive single-read turn (fired=%v)", s.fired)
	}
	// Budget suppressed: rollback restores the once-per-run chance.
	s.markUndelivered()
	if s.fired {
		t.Fatal("markUndelivered must clear fired")
	}
	s.recordToolCall("read_file")
	if msg := s.endTurn(4); msg == "" {
		t.Fatal("expected re-fire after rollback on 4th single-read turn")
	}
}

func TestIssue4224CriteriaDriftMarkUndelivered(t *testing.T) {
	c := newCriteriaDriftState()
	c.indicators = []cdIndicator{{pattern: "p1", iter: 4}, {pattern: "p2", iter: 4}}
	if msg := c.maybeWarn(5); msg == "" {
		t.Fatal("expected fire with 2 indicators >= cdThreshold")
	}
	if c.warnCount != 1 || c.indicators != nil {
		t.Fatalf("fire must consume quota+indicators, got warnCount=%d indicators=%v", c.warnCount, c.indicators)
	}
	if again := c.maybeWarn(6); again != "" {
		t.Fatalf("quota must suppress immediate re-fire, got %q", again)
	}
	// Budget suppressed: rollback restores warnCount AND the consumed batch.
	c.markUndelivered()
	if c.warnCount != 0 {
		t.Fatalf("warnCount must roll back to 0, got %d", c.warnCount)
	}
	if len(c.indicators) != 2 {
		t.Fatalf("consumed indicator batch must be restored, got %v", c.indicators)
	}
	// iter=4 stays within cdIndicatorWindowTurns of iter 6, so it re-fires.
	if msg := c.maybeWarn(6); msg == "" {
		t.Fatal("expected re-fire after rollback")
	}
}

func TestIssue4224AttemptBriefMarkUndelivered(t *testing.T) {
	s := newAttemptBriefState()
	s.recordOutcome("run_command", "t1", false, 1, "boom A")
	s.recordOutcome("edit_file", "t2", false, 2, "boom B")
	s.recordOutcome("run_command", "t3", false, 3, "boom C")
	if msg := s.maybeBrief(10); msg == "" {
		t.Fatal("expected first brief")
	}
	if s.firedCount != 1 || s.lastFireIter != 10 {
		t.Fatalf("fire must consume quota (firedCount=%d lastFireIter=%d)", s.firedCount, s.lastFireIter)
	}
	// Without rollback: next call is both in cooldown and quota-held.
	if again := s.maybeBrief(11); again != "" {
		t.Fatalf("cooldown+quota must suppress re-fire, got %q", again)
	}
	// Budget suppressed: rollback restores firedCount and the watermark.
	s.markUndelivered()
	if s.firedCount != 0 || s.lastFireIter != 0 {
		t.Fatalf("rollback must restore quota+watermark to constructor baseline (0/0), got firedCount=%d lastFireIter=%d", s.firedCount, s.lastFireIter)
	}
	if msg := s.maybeBrief(11); msg == "" {
		t.Fatal("expected re-fire after rollback")
	}
}

func TestIssue4224SuccessDeclareMarkUndelivered(t *testing.T) {
	s := newSuccessDeclareState()
	// Drive a fire: declaration at iter 2, enough actions after, later iter.
	s.declarationIter = 2
	s.declarationTxt = "task complete"
	s.actionsSince = sdMinActionsAfterDecl
	if msg := s.maybeWarn(10); msg == "" {
		t.Fatal("expected first fire")
	}
	if !s.fired || s.warnCount != 1 {
		t.Fatalf("fire must consume quota (fired=%v warnCount=%d)", s.fired, s.warnCount)
	}
	if again := s.maybeWarn(11); again != "" {
		t.Fatalf("one-shot quota must suppress re-fire, got %q", again)
	}
	s.markUndelivered()
	if s.fired || s.warnCount != 0 {
		t.Fatalf("rollback must restore quota (fired=%v warnCount=%d)", s.fired, s.warnCount)
	}
	if msg := s.maybeWarn(12); msg == "" {
		t.Fatal("expected re-fire after rollback")
	}
}

func TestIssue4224ConstraintAmnesiaMarkUndelivered(t *testing.T) {
	s := newConstraintAmnesiaState()
	s.constraints = []constraintEntry{{excerpt: "never push to main", messageIdx: 0}}
	first := s.maybeWarn(constraintReminderMinIterations + 1)
	if !strings.Contains(first, "constraint-reminder") {
		t.Fatalf("expected reminder fire, got %q", first)
	}
	if s.warnings != constraintMaxWarnings {
		t.Fatalf("fire must consume the max-warnings quota, warnings=%d", s.warnings)
	}
	if again := s.maybeWarn(constraintReminderMinIterations + 5); again != "" {
		t.Fatalf("quota must suppress re-fire, got %q", again)
	}
	// Budget suppressed: rollback restores the single warning slot.
	s.markUndelivered()
	if s.warnings != 0 {
		t.Fatalf("warnings must roll back to 0, got %d", s.warnings)
	}
	if msg := s.maybeWarn(constraintReminderMinIterations + 6); msg == "" {
		t.Fatal("expected re-fire after rollback")
	}
}
