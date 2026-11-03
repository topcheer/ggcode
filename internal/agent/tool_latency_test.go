package agent

import (
	"strings"
	"testing"
	"time"
)

func TestLatencyTrackerNoBaselineNoWarning(t *testing.T) {
	lt := NewLatencyTracker()

	warn := lt.RecordAndCheck("read_file", 30*time.Second)
	if warn != "" {
		t.Errorf("expected no warning with no baseline, got: %q", warn)
	}
}

func TestLatencyTrackerBelowMinSamplesNoWarning(t *testing.T) {
	lt := NewLatencyTracker()

	lt.RecordAndCheck("read_file", 50*time.Millisecond)
	lt.RecordAndCheck("read_file", 60*time.Millisecond)

	warn := lt.RecordAndCheck("read_file", 10*time.Second)
	if warn != "" {
		t.Errorf("expected no warning below minSamples, got: %q", warn)
	}
}

func TestLatencyTrackerDetectsOutlier(t *testing.T) {
	lt := NewLatencyTracker()

	lt.RecordAndCheck("read_file", 50*time.Millisecond)
	lt.RecordAndCheck("read_file", 60*time.Millisecond)
	lt.RecordAndCheck("read_file", 55*time.Millisecond)

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

	lt.RecordAndCheck("grep", 1*time.Millisecond)
	lt.RecordAndCheck("grep", 2*time.Millisecond)
	lt.RecordAndCheck("grep", 1*time.Millisecond)

	warn := lt.RecordAndCheck("grep", 100*time.Millisecond)
	if warn != "" {
		t.Errorf("expected no warning below absolute floor, got: %q", warn)
	}
}

func TestLatencyTrackerNormalCallNoWarning(t *testing.T) {
	lt := NewLatencyTracker()

	lt.RecordAndCheck("read_file", 100*time.Millisecond)
	lt.RecordAndCheck("read_file", 120*time.Millisecond)
	lt.RecordAndCheck("read_file", 110*time.Millisecond)

	warn := lt.RecordAndCheck("read_file", 130*time.Millisecond)
	if warn != "" {
		t.Errorf("expected no warning for normal call, got: %q", warn)
	}
}

func TestLatencyTrackerUnmonitoredToolNoWarning(t *testing.T) {
	lt := NewLatencyTracker()

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

	lt.RecordAndCheck("read_file", 50*time.Millisecond)
	lt.RecordAndCheck("read_file", 60*time.Millisecond)
	lt.RecordAndCheck("read_file", 55*time.Millisecond)

	warn1 := lt.RecordAndCheck("read_file", 5*time.Second)
	if warn1 == "" {
		t.Fatal("expected first outlier warning")
	}

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

	for i := 0; i < maxLatencySamples+10; i++ {
		lt.RecordAndCheck("read_file", 50*time.Millisecond)
	}

	mean := lt.meanLatency("read_file")
	if mean != 50*time.Millisecond {
		t.Errorf("expected mean 50ms, got %v", mean)
	}
}

func TestLatencyTrackerAdaptsBaseline(t *testing.T) {
	lt := NewLatencyTracker()

	lt.RecordAndCheck("read_file", 50*time.Millisecond)
	lt.RecordAndCheck("read_file", 60*time.Millisecond)
	lt.RecordAndCheck("read_file", 55*time.Millisecond)

	for i := 0; i < maxLatencySamples; i++ {
		lt.RecordAndCheck("read_file", 3*time.Second)
	}

	mean := lt.meanLatency("read_file")
	if mean < 2*time.Second {
		t.Errorf("expected adapted mean ~3s after slow samples, got %v", mean)
	}
}
