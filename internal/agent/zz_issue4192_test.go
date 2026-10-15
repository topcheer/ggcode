package agent

import (
	"strings"
	"testing"
)

// #4192: an edit and the grep that verifies it share one iteration number
// in the tool loop. Staleness must key on record order (seq), not the
// iteration integer - "edit then grep, no matches" is the observation
// ALREADY reflecting the post-edit state and must not be declared stale.
func TestReverifySameIterationEditThenGrepNotStale(t *testing.T) {
	s := newReverifyState()
	// One iteration, sequential calls: edit first, then the verifying grep.
	s.recordToolResult("edit_file", `{"path":"a.go"}`, "ok", false, 5)
	s.recordToolResult("grep", `{"pattern":"old"}`, "No matches found", false, 5)
	if msg := s.checkStaleTrap("Removed all deprecated calls, done, everything passes.", 11); msg != "" {
		t.Fatalf("edit-then-grep same-iter must not be stale, got: %s", msg)
	}
}

// Mirror order, same iteration: grep records no-match FIRST, then the edit
// changes the match surface - the observation IS stale and must fire.
func TestReverifySameIterationGrepThenEditStillStale(t *testing.T) {
	s := newReverifyState()
	s.recordToolResult("grep", `{"pattern":"old"}`, "No matches found", false, 5)
	s.recordToolResult("edit_file", `{"path":"a.go"}`, "ok", false, 5)
	msg := s.checkStaleTrap("Removed all deprecated calls, done, everything passes.", 11)
	if msg == "" {
		t.Fatal("grep-then-edit same-iter observation is invalidated by the edit, must fire")
	}
	for _, want := range []string{"[stale-observation-reverify]", "grep"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message missing %q: %s", want, msg)
		}
	}
}

// Cross-iteration mutation after the observation keeps firing (old
// behavior preserved; guards against the strict comparison over-tightening
// the common different-iteration case).
func TestReverifyCrossIterationMutationStillStale(t *testing.T) {
	s := newReverifyState()
	s.recordToolResult("grep", `{"pattern":"foo"}`, "No matches found", false, 1)
	s.recordToolResult("edit_file", `{"path":"a.go"}`, "ok", false, 3)
	if msg := s.checkStaleTrap("The fix is complete, everything works now.", 8); msg == "" {
		t.Fatal("later-iteration mutation must still stale the observation")
	}
}
