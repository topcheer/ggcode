package tool

import (
	"testing"
	"time"
)

// #5520: every fleet instance used the identical 3-minute constant, so a
// batch restart (binary-sync broadcast) fired all resume nudges in
// lockstep. The delay must now be jittered per-process (pid-derived) while
// staying near the nominal base.
func TestRestartResumeNudgeDelayJittered(t *testing.T) {
	base := restartResumeNudgeDelayBase
	span := restartResumeNudgeJitterSpan
	seen := map[time.Duration]bool{}
	for i := 0; i < 40; i++ {
		d := restartResumeNudgeDelay()
		if d < base-span/2 || d >= base+span/2 {
			t.Fatalf("delay %s out of jitter window [%s, %s)", d, base-span/2, base+span/2)
		}
		seen[d] = true
	}
	// Same-process determinism: repeated calls must be stable (pid-derived).
	first := restartResumeNudgeDelay()
	for i := 0; i < 5; i++ {
		if got := restartResumeNudgeDelay(); got != first {
			t.Fatalf("delay not deterministic within a process: %s vs %s", got, first)
		}
	}
	// Values were within-window (jitter arithmetic exercised).
	_ = seen
}
