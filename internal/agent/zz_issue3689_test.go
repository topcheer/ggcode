package agent

import "testing"
import "time"

func TestIssue3689_CooldownOutlierStaysOutOfWindow(t *testing.T) {
	lt := NewLatencyTracker()
	for i := 0; i < latencyMinSamples; i++ {
		lt.RecordAndCheck("edit_file", 100*time.Millisecond)
	}
	before := lt.sampleCount("edit_file")

	if w := lt.RecordAndCheck("edit_file", 60*time.Second); w == "" {
		t.Fatal("first outlier must warn")
	}

	if w := lt.RecordAndCheck("edit_file", 90*time.Second); w != "" {
		t.Fatalf("cooldown must suppress the second warning, got: %s", w)
	}
	if after := lt.sampleCount("edit_file"); after != before {
		t.Fatalf("cooldown-suppressed outlier entered the window: %d -> %d", before, after)
	}
	if m := lt.meanLatency("edit_file"); m > time.Second {
		t.Fatalf("baseline polluted by cooldown path: mean=%v", m)
	}
}
