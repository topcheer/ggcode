package agent

import "testing"

func TestIssue3568FailedEditNotProductive(t *testing.T) {
	o := newOverseerState()

	o.itersSinceProductive = 7
	o.driftLevel = 2
	o.fileReadsSinceEdit["a.go"] = 3

	o.recordToolCall("edit_file", true, "a.go", "error: old_text not found in file", 999)

	if o.itersSinceProductive != 8 {
		t.Fatalf("failed edit_file must increment itersSinceProductive, got %d", o.itersSinceProductive)
	}
	if o.driftLevel != 2 {
		t.Fatalf("failed edit_file must not reset driftLevel, got %d", o.driftLevel)
	}
	if o.fileReadsSinceEdit["a.go"] != 3 {
		t.Fatalf("failed edit_file must not clear fileReadsSinceEdit, got %d", o.fileReadsSinceEdit["a.go"])
	}

	o.itersSinceProductive = 0
	o.recordToolCall("run_command", true, "", "exit status 1", 1000)
	if o.itersSinceProductive != 1 {
		t.Fatalf("failed run_command must not be productive, got itersSinceProductive=%d", o.itersSinceProductive)
	}
}

func TestIssue3568PartialSuccessIsProductive(t *testing.T) {
	o := newOverseerState()

	o.itersSinceProductive = 5
	o.fileReadsSinceEdit["a.go"] = 2

	result := `{"status":"partial_success","written_paths":["a.go","b.go"],"failed_files":1,"failed_paths":["c.go"]}`
	o.recordToolCall("multi_file_edit", true, "a.go", result, 999)

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
	o.recordToolCall("edit_file", false, "a.go", "Replaced 1 occurrence", 999)

	if o.itersSinceProductive != 0 {
		t.Fatalf("successful edit_file must reset itersSinceProductive, got %d", o.itersSinceProductive)
	}
	if o.driftLevel != 0 {
		t.Fatalf("successful edit_file must reset driftLevel, got %d", o.driftLevel)
	}
}

func TestIssue3568ReadEditFailLoopTripsFileStuck(t *testing.T) {
	o := newOverseerState()
	o.researchMode = false

	for cycle := 0; cycle < 8; cycle++ {
		o.recordToolCall("read_file", false, "loop.go", "file content", 999)
		o.recordToolCall("edit_file", true, "loop.go", "error: old_text not found", 999)

		iter := (cycle + 1) * overseerInterval
		msg := o.analyze(iter)
		if o.fired["file_stuck"] {
			if msg == "" {
				t.Fatalf("cycle %d: file_stuck fired but analyze returned empty guidance", cycle)
			}
			return
		}
	}
	t.Fatalf("read→failed-edit loop never tripped file_stuck detector after 5 cycles (reads=%d)",
		o.fileReadsSinceEdit["loop.go"])
}
