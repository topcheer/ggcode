package agent

import (
	"strings"
	"testing"
)

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

func TestIssue4075MixedIterationInWindowBlocks(t *testing.T) {
	o := newOverseerState()

	for iter := 0; iter < 10; iter++ {
		for _, tool := range []string{"read_file", "grep", "glob", "search_files"} {
			o.recordToolCall(tool, false, "", "", iter)
		}
	}

	o.recordToolCall("edit_file", false, "/path.go", "", 10)

	for iter := 11; iter < 14; iter++ {
		for _, tool := range []string{"read_file", "grep", "glob", "search_files", "list_directory"} {
			o.recordToolCall(tool, false, "", "", iter)
		}
	}

	if msg := o.checkReadOnlyStall(o.trajectory); msg != "" {
		t.Fatalf("window contains a productive edit and spans <15 iterations; must not fire, got: %s", msg)
	}
}

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
