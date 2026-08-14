package agent

import (
	"strings"
	"testing"
)

func TestShouldRunDetector(t *testing.T) {
	tests := []struct {
		name      string
		tier      int
		iteration int
		want      bool
	}{
		// Tier 0 (critical) - always runs
		{"critical iter 1", detectorTierCritical, 1, true},
		{"critical iter 2", detectorTierCritical, 2, true},
		{"critical iter 10", detectorTierCritical, 10, true},

		// Tier 2 (routine) - every 3 iterations
		{"routine iter 1", detectorTierRoutine, 1, true},
		{"routine iter 2", detectorTierRoutine, 2, false},
		{"routine iter 3", detectorTierRoutine, 3, false},
		{"routine iter 4", detectorTierRoutine, 4, true},
		{"routine iter 7", detectorTierRoutine, 7, true},
		{"routine iter 10", detectorTierRoutine, 10, true},

		// Unknown tier - safe default (always run)
		{"unknown tier", 99, 2, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldRunDetector(tt.tier, tt.iteration)
			if got != tt.want {
				t.Errorf("shouldRunDetector(%d, %d) = %v, want %v",
					tt.tier, tt.iteration, got, tt.want)
			}
		})
	}
}

// TestSamplingReduction verifies that tiered sampling actually reduces
// execution frequency over a representative run.
func TestSamplingReduction(t *testing.T) {
	const totalIters = 20

	// Count how many times each tier would execute over 20 iterations
	var criticalCount, routineCount int
	for i := 1; i <= totalIters; i++ {
		if shouldRunDetector(detectorTierCritical, i) {
			criticalCount++
		}
		if shouldRunDetector(detectorTierRoutine, i) {
			routineCount++
		}
	}

	// Critical: 100% (20/20)
	if criticalCount != totalIters {
		t.Errorf("critical tier: expected %d executions, got %d", totalIters, criticalCount)
	}

	// Routine: ~33% (7/20 for i%3==1: 1,4,7,10,13,16,19)
	if routineCount != 7 {
		t.Errorf("routine tier: expected 7 executions, got %d", routineCount)
	}
}

// TestSamplingDoesNotMissWindowedBursts (issue #312): a burst of 10
// same-category tool calls landing entirely on non-sampled iterations must
// still trigger guidance at the next sampled check, even after the sliding
// window has been flushed by subsequent diverse calls.
func TestSamplingDoesNotMissWindowedBursts(t *testing.T) {
	d := newDiversityState()

	// Simulate the agent loop: recordCall runs every iteration; check is
	// gated to sampled iterations (i%3 == 1).
	var fired string
	for i := 1; i <= 20 && fired == ""; i++ {
		if i <= 10 {
			// Burst of 10 edit_file calls (iterations 1-10).
			d.recordCall("edit_file")
		} else {
			// Window flushed with diverse calls so any non-sticky
			// implementation would lose the burst.
			if i%2 == 0 {
				d.recordCall("read_file")
			} else {
				d.recordCall("grep")
			}
		}
		if shouldRunDetector(detectorTierRoutine, i) {
			fired = d.check()
		}
	}
	if fired == "" {
		t.Fatal("burst of 10 edit calls on non-sampled phases was never reported")
	}
	if !strings.Contains(fired, "edit") {
		t.Errorf("guidance should mention edit category: %s", fired)
	}
}
