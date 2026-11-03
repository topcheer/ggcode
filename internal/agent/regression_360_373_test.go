package agent

import (
	"testing"
	"time"
)

func TestSuccessDeclare_NegatedCaveatPlusLeadingHedge_Vetoed(t *testing.T) {

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

func TestComputeAdaptiveTimeout_UnderMinSamplesUsesCategoryDefault(t *testing.T) {
	lt := NewLatencyTracker()

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

	if got != adaptiveTimeoutFloor {
		t.Errorf("after %d samples expected floor %v, got %v", adaptiveMinSamples, adaptiveTimeoutFloor, got)
	}
}

func TestAmbiguityWordBoundary_RecentlyNotRecent(t *testing.T) {
	if ambContainsPhrase("i recently rebased, now fix the failing tests", "recent") {
		t.Error("'recent' must not match inside 'recently'")
	}
	if !ambContainsPhrase("show me the recent changes", "recent") {
		t.Error("bare 'recent' should still match")
	}
}

func TestAmbiguityWordBoundary_CleanupIdentifierNotPhrase(t *testing.T) {

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

func TestCausalAttribution_SourceReadNotFailure(t *testing.T) {
	s := newCausalAttributionState()
	s.recordEdit("edit_file", "internal/x/a.go", 1)

	out := s.attributeFailure("func f() error {\n\treturn fmt.Errorf(\"failed to connect\")\n}\n")
	if out != "" {
		t.Errorf("source dump must not trigger failure attribution, got: %s", out)
	}
}
