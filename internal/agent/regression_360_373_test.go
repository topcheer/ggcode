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

// --- #381: word-boundary ambiguity matching ---

func TestAmbiguityWordBoundary_RecentlyNotRecent(t *testing.T) {
	if ambContainsPhrase("i recently rebased, now fix the failing tests", "recent") {
		t.Error("'recent' must not match inside 'recently'")
	}
	if !ambContainsPhrase("show me the recent changes", "recent") {
		t.Error("bare 'recent' should still match")
	}
}

func TestAmbiguityWordBoundary_CleanupIdentifierNotPhrase(t *testing.T) {
	// The bare "cleanup" pattern was removed from the table (it collided
	// with identifiers like cleanupConn); "clean up some/the" phrasings
	// carry the vague-scope intent instead (#381).
	for _, pat := range ambiguityPatterns {
		if pat.phrase == "cleanup" {
			t.Error("bare 'cleanup' should no longer be a pattern entry")
		}
	}
	if !ambContainsPhrase("please clean up the old files", "clean up the") {
		t.Error("'clean up the' should match")
	}
}

func TestAmbiguityWordBoundary_PluralTolerated(t *testing.T) {
	if !ambContainsPhrase("remove duplicates from the list", "remove duplicate") {
		t.Error("plural 'duplicates' should match 'remove duplicate' pattern")
	}
}

// --- #379: causal attribution only for verify-looking output ---

func TestCausalAttribution_SourceReadNotFailure(t *testing.T) {
	s := newCausalAttributionState()
	s.recordEdit("edit_file", "internal/x/a.go", 1)
	// Successful read of source containing an error literal — used to be
	// misattributed as a build failure (#379).
	out := s.attributeFailure("func f() error {\n\treturn fmt.Errorf(\"failed to connect\")\n}\n")
	if out != "" {
		t.Errorf("source dump must not trigger failure attribution, got: %s", out)
	}
}

// --- #380: multi-edit anchor normalization ---

func TestAnchorErosion_MultiFileMixedNoDecay(t *testing.T) {
	// Issue scenario: 3 multi_file_edit calls (3 files x 5 lines = 15 summed
	// under the old code) followed by 4 plain edit_file (5 lines each).
	// Per-edit averaging means the window is [5,5,5,5,5] — zero decay.
	a := newAnchorErosionState()
	multiArgs := `{"files":[{"path":"a.go","edits":[{"old_text":"l1\nl2\nl3\nl4\nl5"}]},{"path":"b.go","edits":[{"old_text":"l1\nl2\nl3\nl4\nl5"}]},{"path":"c.go","edits":[{"old_text":"l1\nl2\nl3\nl4\nl5"}]}]}`
	for i := 0; i < 3; i++ {
		a.recordEditAnchor("multi_file_edit", multiArgs)
	}
	singleArgs := `{"file_path":"d.go","old_text":"l1\nl2\nl3\nl4\nl5"}`
	for i := 0; i < 4; i++ {
		if hint := a.recordEditAnchor("edit_file", singleArgs); hint != "" {
			t.Errorf("uniform 5-line anchors must not trigger decay warning, got: %s", hint)
		}
	}
}
