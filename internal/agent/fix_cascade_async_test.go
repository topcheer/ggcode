package agent

import (
	"strings"
	"testing"
)

// #3530: the async verification path (start_command + wait_command /
// read_command_output / task_output) must feed the fix-cascade counter
// exactly like synchronous run_command, gated on the #1153 verify-job
// registry and terminal job status.

func newCascadeTestAgent() *Agent {
	a := &Agent{}
	a.fixCascade = newFixCascadeState()
	a.prematureSuccess = newPrematureSuccessState()
	return a
}

func registerVerifyJob(a *Agent, jobID, cmd string) {
	a.prematureSuccess.mu.Lock()
	defer a.prematureSuccess.mu.Unlock()
	a.prematureSuccess.psRegisterJobLocked(jobID, cmd)
}

// Three async edit->verify->fail cycles must fire the lock-in guidance -
// previously the wait_command result was dropped and the counter stayed 0.
func TestFixCascadeAsyncVerifyFailuresCount(t *testing.T) {
	a := newCascadeTestAgent()
	registerVerifyJob(a, "j1", "go test ./...")
	waitArgs := []byte(`{"job_id":"j1"}`)
	for cycle := 1; cycle <= cascadeThreshold; cycle++ {
		a.fixCascade.recordEdit()
		g := a.fixCascadeCheckCommand("wait_command", waitArgs, false, "Status: failed\nexit 1")
		if cycle < cascadeThreshold {
			if g != "" {
				t.Fatalf("cycle %d: guidance fired early", cycle)
			}
			continue
		}
		if !strings.Contains(g, "HYPOTHESIS LOCK-IN") {
			t.Fatalf("cycle %d: expected cascade guidance on async terminal failure, got %q", cycle, g)
		}
	}
}

// A still-running poll is not an outcome: no increment, and (critically) no
// green reset that would blind the detector when the job later fails.
func TestFixCascadeAsyncStillRunningIgnored(t *testing.T) {
	a := newCascadeTestAgent()
	registerVerifyJob(a, "j1", "go test ./...") // "go test" prefix -> isVerify
	a.fixCascade.recordEdit()
	a.fixCascade.recordVerify(true, true) // seed one prior failed cycle via sync path
	a.fixCascade.recordEdit()
	if g := a.fixCascadeCheckCommand("wait_command", []byte(`{"job_id":"j1"}`), false, "Status: running"); g != "" {
		t.Fatalf("still-running poll must not fire guidance, got %q", g)
	}
	if a.fixCascade.failedVerifyCount != 1 {
		t.Fatalf("still-running poll must not change the counter, got %d", a.fixCascade.failedVerifyCount)
	}
}

// A terminal result for a job NOT registered as verification (long deploy,
// dev server) must not touch the cascade counter.
func TestFixCascadeAsyncNonVerifyJobIgnored(t *testing.T) {
	a := newCascadeTestAgent()
	registerVerifyJob(a, "j2", "make deploy")
	a.fixCascade.recordEdit()
	if g := a.fixCascadeCheckCommand("wait_command", []byte(`{"job_id":"j2"}`), false, "Status: failed"); g != "" {
		t.Fatalf("non-verify job failure must not fire guidance, got %q", g)
	}
	if a.fixCascade.failedVerifyCount != 0 {
		t.Fatalf("non-verify job failure must not count, got %d", a.fixCascade.failedVerifyCount)
	}
}

// A passing terminal async verification resets the cascade, mirroring the
// synchronous success path.
func TestFixCascadeAsyncSuccessResets(t *testing.T) {
	a := newCascadeTestAgent()
	registerVerifyJob(a, "j1", "go test ./...")
	a.fixCascade.recordEdit()
	a.fixCascade.recordVerify(true, true)
	a.fixCascade.recordEdit()
	a.fixCascade.recordVerify(true, true)
	a.fixCascade.recordEdit()
	if g := a.fixCascadeCheckCommand("read_command_output", []byte(`{"job_id":"j1"}`), false, "Status: completed"); g != "" {
		t.Fatalf("success must not fire guidance, got %q", g)
	}
	if a.fixCascade.failedVerifyCount != 0 {
		t.Fatalf("async success must reset the cascade counter, got %d", a.fixCascade.failedVerifyCount)
	}
	if a.fixCascade.editCount != 0 {
		t.Fatalf("async success must reset the edit counter, got %d", a.fixCascade.editCount)
	}
}

// The timeout-moved-to-background detour: run_command itself returns a
// non-error "moved to background" notice (no counting), and the real failure
// later arrives via read_command_output - only that terminal counts.
func TestFixCascadeAsyncBackgroundDetourCountsOnce(t *testing.T) {
	a := newCascadeTestAgent()
	registerVerifyJob(a, "j9", "go test ./...")
	a.fixCascade.recordEdit()
	// run_command times out: content is a handoff notice, not a verify result.
	if g := a.fixCascadeCheckCommand("run_command", []byte(`{"command":"go test ./..."}`), false, "Command is still running after 30m0s. Automatically moved to background (job j9).\nUse `read_command_output` to check progress."); g != "" {
		t.Fatalf("background handoff must not fire guidance, got %q", g)
	}
	if a.fixCascade.failedVerifyCount != 0 {
		t.Fatalf("background handoff must not count (IsError=false), got %d", a.fixCascade.failedVerifyCount)
	}
	// Terminal failure arrives via read_command_output.
	if g := a.fixCascadeCheckCommand("read_command_output", []byte(`{"job_id":"j9"}`), false, "Status: failed"); g != "" {
		t.Fatalf("single async cycle must not fire yet, got %q", g)
	}
	if a.fixCascade.failedVerifyCount != 1 {
		t.Fatalf("async terminal failure must count exactly once, got %d", a.fixCascade.failedVerifyCount)
	}
}
