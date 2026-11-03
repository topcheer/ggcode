package agent

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

	for _, name := range []string{"read_file", "multi_file_read"} {
		w := formatLatencyWarning(name, 5*time.Second, 100*time.Millisecond)
		if !strings.Contains(w, "offset/limit") {
			t.Fatalf("%s must keep the read hint, got: %s", name, w)
		}
	}
}

func TestIssue3639_OutlierDoesNotPolluteBaseline(t *testing.T) {
	lt := NewLatencyTracker()

	for i := 0; i < latencyMinSamples; i++ {
		lt.RecordAndCheck("edit_file", 100*time.Millisecond)
	}
	before := lt.sampleCount("edit_file")

	if w := lt.RecordAndCheck("edit_file", 60*time.Second); w == "" {
		t.Fatal("first 60s outlier must warn")
	}

	if after := lt.sampleCount("edit_file"); after != before {
		t.Fatalf("outlier entered the rolling window: samples %d -> %d", before, after)
	}
	if m := lt.meanLatency("edit_file"); m > time.Second {
		t.Fatalf("baseline polluted by outlier: mean=%v (want ~100ms)", m)
	}
}
