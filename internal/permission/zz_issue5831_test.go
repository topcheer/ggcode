package permission

import (
	"runtime"
	"testing"
)

// mallocsPerDetectorCheck measures allocation events per
// NewDangerousDetector()+Check(cmd) iteration, after warmup.
func mallocsPerDetectorCheck(cmd string, iters int) uint64 {
	d := NewDangerousDetector()
	for i := 0; i < 10; i++ {
		_ = d.Check(cmd)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < iters; i++ {
		det := NewDangerousDetector()
		_ = det.Check(cmd)
	}
	runtime.ReadMemStats(&after)
	return (after.Mallocs - before.Mallocs) / uint64(iters)
}

// TestIssue5831DangerousDetectorDoesNotRecompilePerCall: the detector used
// to recompile its ~70 deny-list regexes on every construction; owners
// include long-lived AllowedRiskLedgers and the verify path. The table now
// compiles once per process.
func TestIssue5831DangerousDetectorDoesNotRecompilePerCall(t *testing.T) {
	for _, cmd := range []string{
		"echo hello",
		"rm -rf /",
		"sudo mkfs.ext4 /dev/sda1",
	} {
		if per := mallocsPerDetectorCheck(cmd, 200); per > 400 {
			t.Errorf("cmd %q: %d mallocs per NewDangerousDetector+Check (budget 400) - per-call regex compilation is back", cmd, per)
		}
	}
}

// TestIssue5831DangerousDetectorBehaviorUnchanged pins a few decisions.
func TestIssue5831DangerousDetectorBehaviorUnchanged(t *testing.T) {
	d := NewDangerousDetector()
	if got := d.Check("rm -rf /"); got.Level != DangerCritical {
		t.Errorf("rm -rf / must stay DangerCritical, got %v (%s)", got.Level, got.Reason)
	}
	if got := d.Check("sudo rm -rf /tmp/x"); got.Level < DangerMedium {
		t.Errorf("sudo rm must stay at least medium, got %v", got.Level)
	}
	if got := d.Check("echo hello"); got.Level != DangerNone {
		t.Errorf("echo hello must stay DangerNone, got %v", got.Level)
	}
}
