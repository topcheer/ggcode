package agent

import "testing"

func TestIssue4069ParallelBatchCountsOneIteration(t *testing.T) {
	o := &overseerState{fileReadsSinceEdit: map[string]int{}}

	for i := 0; i < 5; i++ {
		o.recordToolCall("read_file", false, "f.go", "", 1)
	}
	if o.itersSinceProductive != 1 {
		t.Fatalf("5 unproductive calls in ONE iteration must advance the counter once, got %d", o.itersSinceProductive)
	}

	for i := 0; i < 5; i++ {
		o.recordToolCall("grep", false, "", "", 2)
	}
	for i := 0; i < 5; i++ {
		o.recordToolCall("glob", false, "", "", 3)
	}
	if o.itersSinceProductive != 3 {
		t.Fatalf("3 iterations of batches must count 3, got %d", o.itersSinceProductive)
	}

	o.recordToolCall("edit_file", false, "f.go", "ok", 3)
	if o.itersSinceProductive != 0 {
		t.Fatalf("productive call must reset the counter, got %d", o.itersSinceProductive)
	}

	o2 := &overseerState{fileReadsSinceEdit: map[string]int{}}
	for i := 0; i < 4; i++ {
		o2.recordToolCall("read_file", false, "f.go", "", 1)
	}
	if o2.fileReadsSinceEdit["f.go"] != 4 {
		t.Fatalf("file reads must stay call-unit (4), got %d", o2.fileReadsSinceEdit["f.go"])
	}
}
