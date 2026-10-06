package tool

import (
	"context"
	"strings"
	"testing"
	"time"
)

// r71 lifecycle governance tests: concurrent running-job cap, memory-pressure
// admission refusal, and ShutdownAll reaping at session/process exit.

func TestShutdownAllReapsRunningJob(t *testing.T) {
	mgr := NewCommandJobManager(t.TempDir())
	started, err := mgr.Start(context.Background(), "sleep 5", false, 30*time.Second)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !started.Running {
		t.Fatalf("job should be running right after start, got %s", started.Status)
	}

	reaped := mgr.ShutdownAll(2 * time.Second)
	if reaped != 1 {
		t.Fatalf("ShutdownAll reaped %d jobs, want 1", reaped)
	}

	snap, err := mgr.Read(started.ID, 5, 0)
	if err != nil {
		t.Fatalf("read after shutdown: %v", err)
	}
	// The cancelled job stays as a terminal entry so late polling still
	// explains why it stopped.
	if snap.Running {
		t.Fatalf("job still running after ShutdownAll")
	}
	if snap.Status != CommandJobCancelled {
		t.Fatalf("status after ShutdownAll = %s, want %s", snap.Status, CommandJobCancelled)
	}
}

func TestShutdownAllWithNoJobsReturnsZero(t *testing.T) {
	mgr := NewCommandJobManager(t.TempDir())
	if got := mgr.ShutdownAll(time.Second); got != 0 {
		t.Fatalf("ShutdownAll on empty manager = %d, want 0", got)
	}
}

func TestRunningJobCapRefusesNewJob(t *testing.T) {
	mgr := NewCommandJobManager(t.TempDir())
	mgr.maxRunningJobs = 1 // test-scoped cap; production default is maxRunningJobsDefault

	first, err := mgr.Start(context.Background(), "sleep 5", false, 30*time.Second)
	if err != nil {
		t.Fatalf("first start: %v", err)
	}

	_, err = mgr.Start(context.Background(), "sleep 5", false, 30*time.Second)
	if err == nil {
		t.Fatalf("second start should be refused at cap 1")
	}
	if !strings.Contains(err.Error(), "at cap") || !strings.Contains(err.Error(), "stop_command") {
		t.Fatalf("refusal message not actionable: %v", err)
	}

	// A refused start must NOT leave a job entry behind (ownership never
	// transferred), otherwise the cap would count phantom jobs.
	if n := mgr.countRunningForTest(); n != 1 {
		t.Fatalf("running jobs after refusal = %d, want 1 (no phantom entries)", n)
	}

	if _, err := mgr.Stop(first.ID); err != nil {
		t.Fatalf("stop first: %v", err)
	}
	if _, err := mgr.Start(context.Background(), "printf 'ok\\n'", false, 5*time.Second); err != nil {
		t.Fatalf("start after freeing a slot: %v", err)
	}
	mgr.ShutdownAll(2 * time.Second)
}

func TestMemoryPressureRefusesNewJob(t *testing.T) {
	orig := jobMemSampleFn
	defer func() { jobMemSampleFn = orig }()
	jobMemSampleFn = func() uint64 { return 10 << 30 } // 10 GiB

	mgr := NewCommandJobManager(t.TempDir())
	mgr.maxJobMemBytes = 3 << 30

	_, err := mgr.Start(context.Background(), "printf 'ok\\n'", false, 5*time.Second)
	if err == nil {
		t.Fatalf("start should be refused under memory pressure")
	}
	if !strings.Contains(err.Error(), "memory pressure") || !strings.Contains(err.Error(), "GGCODE_JOB_MEM_LIMIT") {
		t.Fatalf("pressure message not actionable: %v", err)
	}
}

func TestResolveJobMemLimitEnvOverride(t *testing.T) {
	t.Setenv("GGCODE_JOB_MEM_LIMIT", "1073741824") // 1 GiB
	if got := resolveJobMemLimit(); got != 1<<30 {
		t.Fatalf("resolveJobMemLimit = %d, want %d", got, uint64(1)<<30)
	}

	t.Setenv("GGCODE_JOB_MEM_LIMIT", "not-a-number")
	if got := resolveJobMemLimit(); got != jobMemLimitDefault {
		t.Fatalf("invalid env should fall back to default %d, got %d", jobMemLimitDefault, got)
	}
}

func (m *CommandJobManager) countRunningForTest() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, j := range m.jobs {
		if !j.isTerminal() {
			n++
		}
	}
	return n
}
