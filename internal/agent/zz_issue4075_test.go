package agent

import (
	"strings"
	"testing"
)

// #4075: checkReadOnlyStall promises an ITERATION-based threshold but used
// to window over per-call trajectory entries. A parallel batch of N
// read-only calls in one iteration counted as N iterations, so 3 real
// batch-read iterations filled the 15-iteration window and the message
// claimed "spent 15 iterations" at iteration 12. The window is now the
// last N distinct iterations (every entry of them).

// TestIssue4075ParallelBatchDoesNotTripStall: 3 iterations x 5 parallel
// read-only calls = 15 entries but only 3 distinct iterations - far below
// the 15-iteration threshold, must NOT fire (the old code fired here).
func TestIssue4075ParallelBatchDoesNotTripStall(t *testing.T) {
	o := newOverseerState()
	tools := []string{"read_file", "grep", "search_files", "glob", "list_directory"}
	for iter := 0; iter < 3; iter++ {
		for _, tool := range tools {
			o.recordToolCall(tool, false, "/path.go", "", iter)
		}
	}
	if msg := o.checkReadOnlyStall(o.trajectory); msg != "" {
		t.Fatalf("3 iterations (15 batched calls) must not trip a 15-iteration stall, got: %s", msg)
	}
	if o.fired["stall"] {
		t.Fatal("stall must not be marked fired")
	}
}

// TestIssue4075TrueIterationsStillTrip: one read-only call per iteration
// across 15 distinct iterations must still fire, and the reported count
// must equal the real iteration span.
func TestIssue4075TrueIterationsStillTrip(t *testing.T) {
	o := newOverseerState()
	for iter := 0; iter < stallThreshold; iter++ {
		tool := "read_file"
		if iter%2 == 1 {
			tool = "grep"
		}
		o.recordToolCall(tool, false, "/path.go", "", iter)
	}
	msg := o.checkReadOnlyStall(o.trajectory)
	if msg == "" {
		t.Fatal("15 truly read-only iterations must trip the stall hint")
	}
	if !strings.Contains(msg, "15 iterations") {
		t.Fatalf("message must report the real 15-iteration span, got: %s", msg)
	}
	if !o.fired["stall"] {
		t.Fatal("stall must be marked fired after triggering")
	}
}

// TestIssue4075MixedIterationInWindowBlocks: a productive call inside the
// last 15 iterations breaks the streak even when the entry count is far
// above threshold (batch reads around it).
func TestIssue4075MixedIterationInWindowBlocks(t *testing.T) {
	o := newOverseerState()
	// Iterations 0..9: batched read-only (4 calls each).
	for iter := 0; iter < 10; iter++ {
		for _, tool := range []string{"read_file", "grep", "glob", "search_files"} {
			o.recordToolCall(tool, false, "", "", iter)
		}
	}
	// Iteration 10: a productive edit.
	o.recordToolCall("edit_file", false, "/path.go", "", 10)
	// Iterations 11..13: batched read-only again (5 calls each).
	for iter := 11; iter < 14; iter++ {
		for _, tool := range []string{"read_file", "grep", "glob", "search_files", "list_directory"} {
			o.recordToolCall(tool, false, "", "", iter)
		}
	}
	// Window = iterations 0..13 (14 distinct < 15) AND contains the edit.
	if msg := o.checkReadOnlyStall(o.trajectory); msg != "" {
		t.Fatalf("window contains a productive edit and spans <15 iterations; must not fire, got: %s", msg)
	}
}

// TestIssue4075ResearchModeWindowIsIterationsToo: research threshold is 40
// iterations; 8 batch-read iterations x 5 calls = 40 entries used to fire
// in research mode as well, gutting #3990's tolerance. Must not fire.
func TestIssue4075ResearchModeWindowIsIterationsToo(t *testing.T) {
	o := newOverseerState()
	o.researchMode = true
	for iter := 0; iter < 8; iter++ {
		for _, tool := range []string{"read_file", "grep", "glob", "search_files", "web_search"} {
			o.recordToolCall(tool, false, "", "", iter)
		}
	}
	if msg := o.checkReadOnlyStall(o.trajectory); msg != "" {
		t.Fatalf("8 batched research iterations (40 calls) must not trip the 40-iteration research stall, got: %s", msg)
	}
}
