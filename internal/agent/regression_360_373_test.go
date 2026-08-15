package agent

import (
	"testing"
	"time"
)

// --- #364: caveat negation must not short-circuit the whole scan ---

func TestSuccessDeclare_NegatedCaveatPlusLeadingHedge_Vetoed(t *testing.T) {
	// "no remaining" negates the "remaining" caveat, but "but first" later in
	// the same sentence is still a hedge — the old return-false short-circuit
	// recorded this as an unhedged declaration (#364).
	s := newSuccessDeclareState()
	s.recordAssistantText("There are no remaining issues, but first we need to run the tests, then we're all done.", 0)
	s.recordToolCall()
	s.recordToolCall()

	if msg := s.maybeWarn(3); msg != "" {
		t.Errorf("negated caveat + trailing hedge should veto declaration, got warning: %s", msg)
	}
	if s.declarationIter != -1 {
		t.Errorf("declaration should not be recorded, got declarationIter=%d", s.declarationIter)
	}
}

// --- #364: bare "done" as a code identifier must not claim the slot ---

func TestSuccessDeclare_DoneIdentifierNotDeclaration(t *testing.T) {
	s := newSuccessDeclareState()
	s.recordAssistantText("I renamed the helper func done() to finish() and fixed wg.done() missing its defer.", 0)
	s.recordToolCall()
	s.recordToolCall()

	if s.declarationIter != -1 {
		t.Errorf("code identifier done()/wg.done() must not record a declaration, got declarationIter=%d", s.declarationIter)
	}
}

func TestSuccessDeclare_PlainDoneStillDeclares(t *testing.T) {
	s := newSuccessDeclareState()
	s.recordAssistantText("The build passes and tests are green. Done.", 1)
	s.recordToolCall()
	s.recordToolCall()

	if s.declarationIter != 1 {
		t.Errorf("plain 'done.' should record declaration at iter 1, got %d", s.declarationIter)
	}
}

// --- #366: adaptive timeout requires adaptiveMinSamples samples ---

func TestComputeAdaptiveTimeout_UnderMinSamplesUsesCategoryDefault(t *testing.T) {
	lt := NewLatencyTracker()
	// A single fast sample must NOT tighten the timeout — the trigger case
	// from the issue was one 50ms cache-hit grep clamping the next grep to
	// the 10s floor.
	lt.RecordAndCheck("grep", 50*time.Millisecond)
	got := lt.computeAdaptiveTimeout("grep")
	if got != categoryDefaultTimeout(classifyTool("grep")) {
		t.Errorf("single sample: expected category default, got %v", got)
	}
}

func TestComputeAdaptiveTimeout_MinSamplesThenTightens(t *testing.T) {
	lt := NewLatencyTracker()
	for i := 0; i < adaptiveMinSamples; i++ {
		lt.RecordAndCheck("grep", 50*time.Millisecond)
	}
	got := lt.computeAdaptiveTimeout("grep")
	// With enough samples the adaptive value clamps to the floor (10s),
	// which is below the grep category default.
	if got != adaptiveTimeoutFloor {
		t.Errorf("after %d samples expected floor %v, got %v", adaptiveMinSamples, adaptiveTimeoutFloor, got)
	}
}
