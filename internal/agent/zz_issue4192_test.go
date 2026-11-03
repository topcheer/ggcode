package agent

import (
	"strings"
	"testing"
)

func TestReverifySameIterationEditThenGrepNotStale(t *testing.T) {
	s := newReverifyState()

	s.recordToolResult("edit_file", `{"path":"a.go"}`, "ok", false, 5)
	s.recordToolResult("grep", `{"pattern":"old"}`, "No matches found", false, 5)
	if msg := s.checkStaleTrap("Removed all deprecated calls, done, everything passes.", 11); msg != "" {
		t.Fatalf("edit-then-grep same-iter must not be stale, got: %s", msg)
	}
}

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

func TestReverifyCrossIterationMutationStillStale(t *testing.T) {
	s := newReverifyState()
	s.recordToolResult("grep", `{"pattern":"foo"}`, "No matches found", false, 1)
	s.recordToolResult("edit_file", `{"path":"a.go"}`, "ok", false, 3)
	if msg := s.checkStaleTrap("The fix is complete, everything works now.", 8); msg == "" {
		t.Fatal("later-iteration mutation must still stale the observation")
	}
}
