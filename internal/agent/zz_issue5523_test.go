package agent

import (
	"testing"
	"time"
)

// #5523: run-duration perf baselines must exclude deliberate wait windows
// (sleep/wait_agent/wait_command). A patrol round that sleeps 46s per loop
// previously recorded wall duration and tripped a phantom multi-x
// performance-regression banner.
func TestPerfBaselineExcludesWaitWindows(t *testing.T) {
	dir := t.TempDir()
	stats := newRunStats("perf wait-window test")
	for i := 0; i < 3; i++ {
		stats.recordToolCall("read_file") // pass the >=3 tool-call gate
	}
	stats.Duration = 120 * time.Second
	stats.AddWaitToolDuration(90 * time.Second) // 3x30s sleeps / sub-agent waits

	if got := stats.ActiveDuration(); got != 30*time.Second {
		t.Fatalf("active duration = %v, want 30s", got)
	}
	// Wait-only round: nothing left to compare across runs.
	stats2 := newRunStats("wait-only round")
	stats2.recordToolCall("sleep")
	stats2.Duration = 46 * time.Second
	stats2.AddWaitToolDuration(46 * time.Second)
	if got := stats2.ActiveDuration(); got != 0 {
		t.Fatalf("wait-only active duration = %v, want 0", got)
	}

	recordPerfBaseline(dir, stats)
	runs := loadPerfBaseline(dir)
	if len(runs) == 0 {
		t.Fatal("baseline entry must be recorded")
	}
	last := runs[len(runs)-1]
	if last.DurationSec != 30 {
		t.Fatalf("recorded DurationSec = %d, want 30 (wait window excluded)", last.DurationSec)
	}
}

func TestIsWaitToolName(t *testing.T) {
	for _, n := range []string{"sleep", "wait_agent", "wait_command"} {
		if !isWaitToolName(n) {
			t.Errorf("%q must be a wait tool", n)
		}
	}
	if isWaitToolName("run_command") || isWaitToolName("read_file") {
		t.Error("active tools must not be classified as wait tools")
	}
}
