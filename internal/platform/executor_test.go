package platform

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// waitForStatus polls the store until the job reaches want.
func waitForStatus(t *testing.T, store *JobStore, id string, want JobStatus) *Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := store.Get(id)
		if err == nil {
			if job.Status == want {
				return job
			}
			if job.Status.Terminal() && job.Status != want {
				t.Fatalf("job %s reached terminal %s, want %s (err=%q)", id, job.Status, want, job.Err)
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for job %s to reach %s", id, want)
	return nil
}

func newTestStore(t *testing.T) *JobStore {
	t.Helper()
	s, err := NewJobStore(filepath.Join(t.TempDir(), "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestExecutorSuccess(t *testing.T) {
	store := newTestStore(t)
	exec := NewExecutor(store, func(_ context.Context, _, _ string) (int, []byte, error) {
		return 0, []byte("done"), nil
	})
	job, err := store.Create("u", "/tmp/w", "hi")
	if err != nil {
		t.Fatal(err)
	}
	exec.Submit(job)
	got := waitForStatus(t, store, job.ID, StatusSucceeded)
	if got.Output != "done" || got.ExitCode != 0 {
		t.Fatalf("job = %+v", got)
	}
}

func TestExecutorNonZeroExitIsFailed(t *testing.T) {
	store := newTestStore(t)
	exec := NewExecutor(store, func(_ context.Context, _, _ string) (int, []byte, error) {
		return 3, []byte("boom"), nil
	})
	job, _ := store.Create("u", "/tmp/w", "hi")
	exec.Submit(job)
	got := waitForStatus(t, store, job.ID, StatusFailed)
	if got.ExitCode != 3 || !strings.Contains(got.Err, "code 3") {
		t.Fatalf("job = %+v", got)
	}
}

func TestExecutorInfraErrorIsFailed(t *testing.T) {
	store := newTestStore(t)
	exec := NewExecutor(store, func(_ context.Context, _, _ string) (int, []byte, error) {
		return -1, nil, errors.New("spawn failed")
	})
	job, _ := store.Create("u", "/tmp/w", "hi")
	exec.Submit(job)
	got := waitForStatus(t, store, job.ID, StatusFailed)
	if got.Err != "spawn failed" {
		t.Fatalf("Err = %q", got.Err)
	}
}

func TestExecutorCancelRunning(t *testing.T) {
	store := newTestStore(t)
	release := make(chan struct{})
	exec := NewExecutor(store, func(ctx context.Context, _, _ string) (int, []byte, error) {
		select {
		case <-release:
			return 0, nil, nil
		case <-ctx.Done():
			return -1, nil, ctx.Err()
		}
	})
	job, _ := store.Create("u", "/tmp/w", "hi")
	exec.Submit(job)
	// wait until running before cancelling
	waitForStatus(t, store, job.ID, StatusRunning)
	if !exec.Cancel(job.ID) {
		t.Fatal("Cancel(running) = false")
	}
	close(release)
	got := waitForStatus(t, store, job.ID, StatusCancelled)
	_ = got
}

func TestExecutorCancelQueued(t *testing.T) {
	store := newTestStore(t)
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	exec := NewExecutor(store, func(_ context.Context, _, _ string) (int, []byte, error) {
		started <- struct{}{}
		<-release
		return 0, nil, nil
	})
	first, _ := store.Create("u", "/tmp/w", "1")
	second, _ := store.Create("u", "/tmp/w", "2")
	exec.Submit(first)
	exec.Submit(second)
	<-started // first holds the slot
	if !exec.Cancel(second.ID) {
		t.Fatal("Cancel(queued) = false")
	}
	close(release)
	waitForStatus(t, store, second.ID, StatusCancelled)
}

func TestExecutorOutputTruncation(t *testing.T) {
	big := strings.Repeat("x", MaxOutputBytes+10)
	got := tailOutput([]byte(big))
	if !strings.HasPrefix(got, "...[truncated]") || len(got) > MaxOutputBytes+20 {
		t.Fatalf("tailOutput length = %d", len(got))
	}
}
