package agent

import (
	"strings"
	"testing"
	"time"
)

func TestLatencyTrackerNoBaselineNoWarning(t *testing.T) {
	lt := NewLatencyTracker()
	// First call: no baseline exists, so no warning even if slow.
	warn := lt.RecordAndCheck("read_file", 30*time.Second)
	if warn != "" {
		t.Errorf("expected no warning with no baseline, got: %q", warn)
	}
}

func TestLatencyTrackerBelowMinSamplesNoWarning(t *testing.T) {
	lt := NewLatencyTracker()
	// Record 2 fast calls (below minSamples=3).
	lt.RecordAndCheck("read_file", 50*time.Millisecond)
	lt.RecordAndCheck("read_file", 60*time.Millisecond)
	// Third call is slow but we only have 2 prior samples.
	warn := lt.RecordAndCheck("read_file", 10*time.Second)
	if warn != "" {
		t.Errorf("expected no warning below minSamples, got: %q", warn)
	}
}

func TestLatencyTrackerDetectsOutlier(t *testing.T) {
	lt := NewLatencyTracker()
	// Establish a fast baseline (3 calls).
	lt.RecordAndCheck("read_file", 50*time.Millisecond)
	lt.RecordAndCheck("read_file", 60*time.Millisecond)
	lt.RecordAndCheck("read_file", 55*time.Millisecond)
	// Now a call that's 200x slower and above the 2s floor.
	warn := lt.RecordAndCheck("read_file", 5*time.Second)
	if warn == "" {
		t.Fatal("expected outlier warning, got empty")
	}
	if !strings.Contains(warn, "read_file") {
		t.Errorf("warning should mention tool name: %q", warn)
	}
	if !strings.Contains(warn, "slower") {
		t.Errorf("warning should mention slowness: %q", warn)
	}
	if !strings.Contains(warn, "offset/limit") {
		t.Errorf("warning should suggest offset/limit for read tools: %q", warn)
	}
}

func TestLatencyTrackerBelowAbsoluteFloorNoWarning(t *testing.T) {
	lt := NewLatencyTracker()
	// Establish a very fast baseline.
	lt.RecordAndCheck("grep", 1*time.Millisecond)
	lt.RecordAndCheck("grep", 2*time.Millisecond)
	lt.RecordAndCheck("grep", 1*time.Millisecond)
	// Call that's 100x slower but still below the 2s floor.
	warn := lt.RecordAndCheck("grep", 100*time.Millisecond)
	if warn != "" {
		t.Errorf("expected no warning below absolute floor, got: %q", warn)
	}
}

func TestLatencyTrackerNormalCallNoWarning(t *testing.T) {
	lt := NewLatencyTracker()
	// Establish baseline.
	lt.RecordAndCheck("read_file", 100*time.Millisecond)
	lt.RecordAndCheck("read_file", 120*time.Millisecond)
	lt.RecordAndCheck("read_file", 110*time.Millisecond)
	// Normal call within range.
	warn := lt.RecordAndCheck("read_file", 130*time.Millisecond)
	if warn != "" {
		t.Errorf("expected no warning for normal call, got: %q", warn)
	}
}

func TestLatencyTrackerUnmonitoredToolNoWarning(t *testing.T) {
	lt := NewLatencyTracker()
	// run_command is not in the monitored set.
	lt.RecordAndCheck("run_command", 50*time.Millisecond)
	lt.RecordAndCheck("run_command", 60*time.Millisecond)
	lt.RecordAndCheck("run_command", 55*time.Millisecond)
	warn := lt.RecordAndCheck("run_command", 10*time.Second)
	if warn != "" {
		t.Errorf("expected no warning for unmonitored tool, got: %q", warn)
	}
}

func TestLatencyTrackerCooldownPreventsSpam(t *testing.T) {
	lt := NewLatencyTracker()
	// Establish baseline.
	lt.RecordAndCheck("read_file", 50*time.Millisecond)
	lt.RecordAndCheck("read_file", 60*time.Millisecond)
	lt.RecordAndCheck("read_file", 55*time.Millisecond)
	// First slow call triggers warning.
	warn1 := lt.RecordAndCheck("read_file", 5*time.Second)
	if warn1 == "" {
		t.Fatal("expected first outlier warning")
	}
	// Second slow call within cooldown — should be suppressed.
	warn2 := lt.RecordAndCheck("read_file", 5*time.Second)
	if warn2 != "" {
		t.Errorf("expected cooldown to suppress second warning, got: %q", warn2)
	}
}

func TestLatencyTrackerSearchToolHint(t *testing.T) {
	lt := NewLatencyTracker()
	lt.RecordAndCheck("grep", 50*time.Millisecond)
	lt.RecordAndCheck("grep", 60*time.Millisecond)
	lt.RecordAndCheck("grep", 55*time.Millisecond)
	warn := lt.RecordAndCheck("grep", 5*time.Second)
	if warn == "" {
		t.Fatal("expected outlier warning")
	}
	if !strings.Contains(warn, "narrowing") {
		t.Errorf("warning should suggest narrowing search: %q", warn)
	}
}

func TestLatencyTrackerMeanLatency(t *testing.T) {
	lt := NewLatencyTracker()
	lt.RecordAndCheck("read_file", 100*time.Millisecond)
	lt.RecordAndCheck("read_file", 200*time.Millisecond)
	lt.RecordAndCheck("read_file", 300*time.Millisecond)
	mean := lt.meanLatency("read_file")
	if mean != 200*time.Millisecond {
		t.Errorf("expected mean 200ms, got %v", mean)
	}
}

func TestLatencyTrackerNilSafe(t *testing.T) {
	var lt *LatencyTracker
	// Should not panic on nil receiver.
	warn := lt.RecordAndCheck("read_file", 5*time.Second)
	if warn != "" {
		t.Errorf("expected no warning from nil tracker, got: %q", warn)
	}
	mean := lt.meanLatency("read_file")
	if mean != 0 {
		t.Errorf("expected 0 mean from nil tracker, got: %v", mean)
	}
}

func TestLatencyTrackerRollingWindowCaps(t *testing.T) {
	lt := NewLatencyTracker()
	// Record more than maxLatencySamples calls.
	for i := 0; i < maxLatencySamples+10; i++ {
		lt.RecordAndCheck("read_file", 50*time.Millisecond)
	}
	// Should not panic or grow unboundedly.
	mean := lt.meanLatency("read_file")
	if mean != 50*time.Millisecond {
		t.Errorf("expected mean 50ms, got %v", mean)
	}
}

func TestLatencyTrackerAdaptsBaseline(t *testing.T) {
	lt := NewLatencyTracker()
	// Start with fast baseline.
	lt.RecordAndCheck("read_file", 50*time.Millisecond)
	lt.RecordAndCheck("read_file", 60*time.Millisecond)
	lt.RecordAndCheck("read_file", 55*time.Millisecond)

	// Simulate consistently slow environment (network mount).
	// After cooldown, new slow samples shift the baseline upward.
	for i := 0; i < maxLatencySamples; i++ {
		lt.RecordAndCheck("read_file", 3*time.Second)
	}

	// Now the mean should be ~3s, so a 3s call is NOT an outlier.
	mean := lt.meanLatency("read_file")
	if mean < 2*time.Second {
		t.Errorf("expected adapted mean ~3s after slow samples, got %v", mean)
	}
}
