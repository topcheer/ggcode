package agent

import "testing"

// #3568: failed edits must not count as productive (they used to reset
// itersSinceProductive, clear fileReadsSinceEdit, and zero driftLevel on
// every failed edit_file, letting read→edit-fail loops escape the drift
// and file-stuck detectors). partial_success (IsError=true but
// written_paths present) DOES count as productive, matching #1762.
func TestIssue3568FailedEditNotProductive(t *testing.T) {
	o := newOverseerState()

	// Prime state that a failed edit must NOT clear.
	o.itersSinceProductive = 7
	o.driftLevel = 2
	o.fileReadsSinceEdit["a.go"] = 3

	// Failed edit_file with no files reaching disk (old_text mismatch).
	o.recordToolCall("edit_file", true, "a.go", "error: old_text not found in file")

	if o.itersSinceProductive != 8 {
		t.Fatalf("failed edit_file must increment itersSinceProductive, got %d", o.itersSinceProductive)
	}
	if o.driftLevel != 2 {
		t.Fatalf("failed edit_file must not reset driftLevel, got %d", o.driftLevel)
	}
	if o.fileReadsSinceEdit["a.go"] != 3 {
		t.Fatalf("failed edit_file must not clear fileReadsSinceEdit, got %d", o.fileReadsSinceEdit["a.go"])
	}

	// Failed run_command keeps the historical behavior: not productive.
	o.itersSinceProductive = 0
	o.recordToolCall("run_command", true, "", "exit status 1")
	if o.itersSinceProductive != 1 {
		t.Fatalf("failed run_command must not be productive, got itersSinceProductive=%d", o.itersSinceProductive)
	}
}

func TestIssue3568PartialSuccessIsProductive(t *testing.T) {
	o := newOverseerState()

	// A partial_success multi_file_edit: IsError=true overall, but two
	// files actually reached disk (written_paths is real).
	o.itersSinceProductive = 5
	o.fileReadsSinceEdit["a.go"] = 2

	result := `{"status":"partial_success","written_paths":["a.go","b.go"],"failed_files":1,"failed_paths":["c.go"]}`
	o.recordToolCall("multi_file_edit", true, "a.go", result)

	if o.itersSinceProductive != 0 {
		t.Fatalf("partial_success with real written_paths must be productive (reset itersSinceProductive), got %d", o.itersSinceProductive)
	}
	if len(o.fileReadsSinceEdit) != 0 {
		t.Fatalf("partial_success must clear fileReadsSinceEdit, got %v", o.fileReadsSinceEdit)
	}
}

func TestIssue3568SuccessfulEditStillProductive(t *testing.T) {
	o := newOverseerState()

	o.itersSinceProductive = 4
	o.driftLevel = 1
	o.recordToolCall("edit_file", false, "a.go", "Replaced 1 occurrence")

	if o.itersSinceProductive != 0 {
		t.Fatalf("successful edit_file must reset itersSinceProductive, got %d", o.itersSinceProductive)
	}
	if o.driftLevel != 0 {
		t.Fatalf("successful edit_file must reset driftLevel, got %d", o.driftLevel)
	}
}

// End-to-end: a read→failed-edit loop must now trip the file-stuck
// detector (previously the failed edit cleared the read counter every
// cycle, so it never reached fileStuckThreshold).
func TestIssue3568ReadEditFailLoopTripsFileStuck(t *testing.T) {
	o := newOverseerState()
	o.researchMode = false

	// Each cycle adds 2 entries (read + failed edit). The analyze gate
	// requires len(trajectory) >= overseerInterval (12), so the detector
	// can only fire from cycle 6 onward.
	for cycle := 0; cycle < 8; cycle++ {
		o.recordToolCall("read_file", false, "loop.go", "file content")
		o.recordToolCall("edit_file", true, "loop.go", "error: old_text not found")

		// Analyze with enough trajectory to pass the interval gate.
		iter := (cycle + 1) * overseerInterval
		msg := o.analyze(iter)
		if o.fired["file_stuck"] {
			if msg == "" {
				t.Fatalf("cycle %d: file_stuck fired but analyze returned empty guidance", cycle)
			}
			return // detector fired as intended
		}
	}
	t.Fatalf("read→failed-edit loop never tripped file_stuck detector after 5 cycles (reads=%d)",
		o.fileReadsSinceEdit["loop.go"])
}
