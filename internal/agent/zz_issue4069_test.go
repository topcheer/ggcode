package agent

import "testing"

// #4069: itersSinceProductive is calibrated in ITERATIONS but recordToolCall
// fires once per TOOL RESULT. A parallel batch of N read-only tools used to
// advance the counter N times (3 real iterations tripped the 15-iteration
// stall threshold). The counter now advances at most once per iteration.
func TestIssue4069ParallelBatchCountsOneIteration(t *testing.T) {
	o := &overseerState{fileReadsSinceEdit: map[string]int{}}

	// Iteration 1: a batch of 5 unproductive (read-only) tool calls.
	for i := 0; i < 5; i++ {
		o.recordToolCall("read_file", false, "f.go", "", 1)
	}
	if o.itersSinceProductive != 1 {
		t.Fatalf("5 unproductive calls in ONE iteration must advance the counter once, got %d", o.itersSinceProductive)
	}

	// Iterations 2 and 3: same batches - one advance each.
	for i := 0; i < 5; i++ {
		o.recordToolCall("grep", false, "", "", 2)
	}
	for i := 0; i < 5; i++ {
		o.recordToolCall("glob", false, "", "", 3)
	}
	if o.itersSinceProductive != 3 {
		t.Fatalf("3 iterations of batches must count 3, got %d", o.itersSinceProductive)
	}

	// A productive call mid-batch still resets immediately (batch member
	// edit succeeding clears the stall clock regardless of dedup).
	o.recordToolCall("edit_file", false, "f.go", "ok", 3)
	if o.itersSinceProductive != 0 {
		t.Fatalf("productive call must reset the counter, got %d", o.itersSinceProductive)
	}

	// Per-call counters stay per-call: 5 fileReadsSinceEdit entries is a
	// genuine call-unit signal consumed by the file-stuck detector.
	o2 := &overseerState{fileReadsSinceEdit: map[string]int{}}
	for i := 0; i < 4; i++ {
		o2.recordToolCall("read_file", false, "f.go", "", 1)
	}
	if o2.fileReadsSinceEdit["f.go"] != 4 {
		t.Fatalf("file reads must stay call-unit (4), got %d", o2.fileReadsSinceEdit["f.go"])
	}
}
