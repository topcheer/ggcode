package agent

import (
	"strings"
	"testing"
)

func TestTruncClaimRecordAndDetect(t *testing.T) {
	s := newTruncClaimState()

	s.recordTruncation("grep", 2)

	hint := s.maybeWarnTruncClaim("I found only 5 matches for the pattern", 2)
	if hint == "" {
		t.Fatal("expected warning when completeness claim follows truncation")
	}
	if !strings.Contains(hint, "grep") {
		t.Errorf("warning should mention the truncated tool name, got: %s", hint)
	}
	if !strings.Contains(hint, "truncated") {
		t.Errorf("warning should mention truncation, got: %s", hint)
	}
}

func TestTruncClaimNoTruncation(t *testing.T) {
	s := newTruncClaimState()

	hint := s.maybeWarnTruncClaim("These are all the files that match", 0)
	if hint != "" {
		t.Errorf("should not warn when no truncation occurred, got: %s", hint)
	}
}

func TestTruncClaimAcknowledgedNoWarn(t *testing.T) {
	s := newTruncClaimState()
	s.recordTruncation("grep", 0)

	hint := s.maybeWarnTruncClaim(
		"The search results may be incomplete due to truncation. Let me narrow the search.", 0)
	if hint != "" {
		t.Errorf("should not warn when agent acknowledges truncation, got: %s", hint)
	}
}

func TestTruncClaimNoCompletenessClaim(t *testing.T) {
	s := newTruncClaimState()
	s.recordTruncation("grep", 0)

	hint := s.maybeWarnTruncClaim("Let me read the file to understand the pattern.", 0)
	if hint != "" {
		t.Errorf("should not warn when no completeness claim present, got: %s", hint)
	}
}

func TestTruncClaimMaxWarnings(t *testing.T) {
	s := newTruncClaimState()

	for iter := 0; iter < truncClaimMaxWarnings; iter++ {
		s.recordTruncation("grep", iter)
		hint := s.maybeWarnTruncClaim("There are no other references to this function", iter)
		if hint == "" {
			t.Fatalf("expected warning at iteration %d", iter)
		}
	}

	s.recordTruncation("grep", 10)
	hint := s.maybeWarnTruncClaim("There are no other references", 10)
	if hint != "" {
		t.Errorf("warning should be suppressed after max, got: %s", hint)
	}
}

func TestTruncClaimExhaustiveCountPatterns(t *testing.T) {
	s := newTruncClaimState()
	s.recordTruncation("search_files", 0)

	claims := []string{
		"only 3 files match the pattern",
		"5 matches in total",
		"total of 10 occurrences",
		"these are all the matches",
		"that's everything we found",
		"all occurrences of this pattern",
		"no other matches found",
		"nothing else matches",
		"there are no further references",
		"couldn't find any other instances",
		"found all the references",
		"grep found every match",
		"here's the complete list",
	}

	for _, claim := range claims {
		s2 := newTruncClaimState()
		s2.recordTruncation("grep", 0)
		hint := s2.maybeWarnTruncClaim(claim, 0)
		if hint == "" {
			t.Errorf("expected warning for claim: %q", claim)
		}
	}
}

func TestTruncClaimNegativePatterns(t *testing.T) {
	s := newTruncClaimState()
	s.recordTruncation("grep", 0)

	nonClaims := []string{
		"Let me check the test results",
		"The build passed successfully",
		"I'll update the function signature",
		"Here is a summary of changes",
	}

	for _, text := range nonClaims {
		hint := s.maybeWarnTruncClaim(text, 0)
		if hint != "" {
			t.Errorf("should not warn for non-claim text: %q, got: %s", text, hint)
		}
	}
}

func TestTruncClaimReset(t *testing.T) {
	s := newTruncClaimState()
	s.recordTruncation("grep", 0)
	s.warnings = 2
	s.reset()

	if len(s.events) != 0 {
		t.Errorf("events should be cleared after reset")
	}
	if s.warnings != 0 {
		t.Errorf("warnings should be cleared after reset")
	}
	if s.lastWarnIter != -1 {
		t.Errorf("lastWarnIter should be reset to -1")
	}
}

func TestTruncClaimRingBuffer(t *testing.T) {
	s := newTruncClaimState()

	for i := 0; i < truncClaimMaxEvents+3; i++ {
		s.recordTruncation("grep", i)
	}

	if len(s.events) > truncClaimMaxEvents {
		t.Errorf("events should be capped at %d, got %d", truncClaimMaxEvents, len(s.events))
	}
}

func TestTruncClaimTemporalLocality(t *testing.T) {
	s := newTruncClaimState()
	s.recordTruncation("grep", 5)

	hint := s.maybeWarnTruncClaim("only 5 matches found", 7)
	if hint != "" {
		t.Errorf("should not warn when truncation is outside temporal window, got: %s", hint)
	}

	hint2 := s.maybeWarnTruncClaim("only 5 matches found", 6)
	if hint2 == "" {
		t.Errorf("should warn when truncation is within temporal window (1 iter behind)")
	}
}

func TestTruncClaimSameIterationDoubleWarn(t *testing.T) {
	s := newTruncClaimState()
	s.recordTruncation("grep", 0)

	hint1 := s.maybeWarnTruncClaim("only 5 matches found", 0)
	if hint1 == "" {
		t.Fatal("expected first warning")
	}

	hint2 := s.maybeWarnTruncClaim("no other references", 0)
	if hint2 != "" {
		t.Errorf("should not warn twice at same iteration, got: %s", hint2)
	}
}

func TestTruncClaimAcknowledgedPhrases(t *testing.T) {
	s := newTruncClaimState()
	s.recordTruncation("grep", 0)

	ackTexts := []string{
		"The output was truncated so I need to re-search",
		"Results may be incomplete",
		"These may not be exhaustive",
		"Partial results, let me narrow further",
		"There might be more results",
	}

	for _, text := range ackTexts {
		hint := s.maybeWarnTruncClaim(text, 0)
		if hint != "" {
			t.Errorf("should not warn for acknowledged text: %q", text)
		}
	}
}
