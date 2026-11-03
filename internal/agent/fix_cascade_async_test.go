package agent

import (
	"strings"
	"testing"
)

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

func TestFixCascadeAsyncStillRunningIgnored(t *testing.T) {
	a := newCascadeTestAgent()
	registerVerifyJob(a, "j1", "go test ./...")
	a.fixCascade.recordEdit()
	a.fixCascade.recordVerify(true, true)
	a.fixCascade.recordEdit()
	if g := a.fixCascadeCheckCommand("wait_command", []byte(`{"job_id":"j1"}`), false, "Status: running"); g != "" {
		t.Fatalf("still-running poll must not fire guidance, got %q", g)
	}
	if a.fixCascade.failedVerifyCount != 1 {
		t.Fatalf("still-running poll must not change the counter, got %d", a.fixCascade.failedVerifyCount)
	}
}

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

func TestFixCascadeAsyncBackgroundDetourCountsOnce(t *testing.T) {
	a := newCascadeTestAgent()
	registerVerifyJob(a, "j9", "go test ./...")
	a.fixCascade.recordEdit()

	if g := a.fixCascadeCheckCommand("run_command", []byte(`{"command":"go test ./..."}`), false, "Command is still running after 30m0s. Automatically moved to background (job j9).\nUse `read_command_output` to check progress."); g != "" {
		t.Fatalf("background handoff must not fire guidance, got %q", g)
	}
	if a.fixCascade.failedVerifyCount != 0 {
		t.Fatalf("background handoff must not count (IsError=false), got %d", a.fixCascade.failedVerifyCount)
	}

	if g := a.fixCascadeCheckCommand("read_command_output", []byte(`{"job_id":"j9"}`), false, "Status: failed"); g != "" {
		t.Fatalf("single async cycle must not fire yet, got %q", g)
	}
	if a.fixCascade.failedVerifyCount != 1 {
		t.Fatalf("async terminal failure must count exactly once, got %d", a.fixCascade.failedVerifyCount)
	}
}
