package agent

import "testing"

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

		// Tier 1 (high) - every 2 iterations (odd)
		{"high iter 1", detectorTierHigh, 1, true},
		{"high iter 2", detectorTierHigh, 2, false},
		{"high iter 3", detectorTierHigh, 3, true},
		{"high iter 4", detectorTierHigh, 4, false},
		{"high iter 5", detectorTierHigh, 5, true},

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
	var criticalCount, highCount, routineCount int
	for i := 1; i <= totalIters; i++ {
		if shouldRunDetector(detectorTierCritical, i) {
			criticalCount++
		}
		if shouldRunDetector(detectorTierHigh, i) {
			highCount++
		}
		if shouldRunDetector(detectorTierRoutine, i) {
			routineCount++
		}
	}

	// Critical: 100% (20/20)
	if criticalCount != totalIters {
		t.Errorf("critical tier: expected %d executions, got %d", totalIters, criticalCount)
	}

	// High: 50% (10/20)
	if highCount != totalIters/2 {
		t.Errorf("high tier: expected %d executions, got %d", totalIters/2, highCount)
	}

	// Routine: ~33% (7/20 for i%3==1: 1,4,7,10,13,16,19)
	if routineCount != 7 {
		t.Errorf("routine tier: expected 7 executions, got %d", routineCount)
	}
}
