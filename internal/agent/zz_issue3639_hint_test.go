package agent

// #3639 probe, defect 1: the edit hint must be reachable. edit_file,
// multi_edit_file, multi_file_edit all contain "file" and used to fall
// into the read branch (use offset/limit - meaningless for edits), leaving
// the edit branch dead code for every monitored tool.

import (
	"strings"
	"testing"
	"time"
)

func TestIssue3639_EditHintReachable(t *testing.T) {
	for _, name := range []string{"edit_file", "multi_edit_file", "multi_file_edit"} {
		w := formatLatencyWarning(name, 5*time.Second, 100*time.Millisecond)
		if !strings.Contains(w, "unusually large") {
			t.Fatalf("%s must get the edit hint, got: %s", name, w)
		}
		if strings.Contains(w, "offset/limit") {
			t.Fatalf("%s still routed to the meaningless read hint: %s", name, w)
		}
	}
	// read family keeps its hint.
	for _, name := range []string{"read_file", "multi_file_read"} {
		w := formatLatencyWarning(name, 5*time.Second, 100*time.Millisecond)
		if !strings.Contains(w, "offset/limit") {
			t.Fatalf("%s must keep the read hint, got: %s", name, w)
		}
	}
}

// #3639 probe, defect 2: an outlier sample must not pollute the rolling
// baseline. One 60s stall used to lift the mean to ~15s so the NEXT
// genuine 20s outlier no longer cleared the 5x bar (detection blind
// spot). The window must stay a healthy baseline; consecutive outliers
// keep warning.
func TestIssue3639_OutlierDoesNotPolluteBaseline(t *testing.T) {
	lt := NewLatencyTracker()
	// Healthy baseline.
	for i := 0; i < latencyMinSamples; i++ {
		lt.RecordAndCheck("edit_file", 100*time.Millisecond)
	}
	before := lt.sampleCount("edit_file")
	// First extreme outlier: warns AND must not enter the window.
	if w := lt.RecordAndCheck("edit_file", 60*time.Second); w == "" {
		t.Fatal("first 60s outlier must warn")
	}
	// Window purity: the outlier must NOT have been appended. (The next
	// outlier's warning cannot be asserted directly - the cooldown
	// legitimately suppresses it - but a clean mean is what makes it
	// detectable once the cooldown lapses; the old code's ~15s polluted
	// mean made 20s < 5x forever.)
	if after := lt.sampleCount("edit_file"); after != before {
		t.Fatalf("outlier entered the rolling window: samples %d -> %d", before, after)
	}
	if m := lt.meanLatency("edit_file"); m > time.Second {
		t.Fatalf("baseline polluted by outlier: mean=%v (want ~100ms)", m)
	}
}
