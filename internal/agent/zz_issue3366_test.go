package agent

// #3366 probes: a FAILED mutating call must never be sidecar-recorded -
// post-crash restore seeds from the sidecar, so a recorded failure would
// suppress the legitimate retry after the crash. The call-site guard
// (!res.IsError, mirroring record()'s discipline) keeps failures out of
// the file; these probes pin the crash-recovery property at the seed/
// suppress seam the guard feeds.

import (
	"strings"
	"testing"
	"time"
)

func TestIssue3366_FailedCallAbsentFromSidecarDoesNotSuppressRetry(t *testing.T) {
	withCrashSidecarDir(t)
	sid := "crash-3366"
	// The sidecar after a run where the mutating call FAILED: empty (the
	// pre-fix code wrote the failed call here, and the post-crash retry of
	// the same call was wrongly suppressed as a replay).
	writeSidecar(t, sid, nil)
	l := newToolDedupLedger()
	if n := SeedCrashDedup(&Agent{sessionID: sid, toolDedup: l}, sid); n != 0 {
		t.Fatalf("seeded = %d, want 0 (empty sidecar)", n)
	}
	if res := l.suppressDuplicate("run_command", `{"command":"make build"}`); res != nil {
		t.Fatalf("retry after a failed call must not be suppressed, got: %s", res.Content)
	}
}

// The contrast case that documents WHY failures must stay out: a recorded
// call (successful semantics) is suppressed within the crash window - the
// pre-fix bug made failed calls land on this path too.
func TestIssue3366_RecordedSuccessfulCallStillSuppressed(t *testing.T) {
	withCrashSidecarDir(t)
	sid := "crash-3366-ok"
	writeSidecar(t, sid, []crashMutatingCall{
		{Name: "run_command", Args: `{"command":"make build"}`, At: time.Now().Add(-2 * time.Minute)},
	})
	l := newToolDedupLedger()
	if n := SeedCrashDedup(&Agent{sessionID: sid, toolDedup: l}, sid); n != 1 {
		t.Fatalf("seeded = %d, want 1", n)
	}
	res := l.suppressDuplicate("run_command", `{"command":"make build"}`)
	if res == nil || !strings.Contains(res.Content, "[crash-window dedup]") {
		t.Fatalf("successful call replay must stay suppressed, got: %v", res)
	}
}
