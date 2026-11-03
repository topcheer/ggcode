package agent

import (
	"strings"
	"testing"
	"time"
)

func TestIssue3366_FailedCallAbsentFromSidecarDoesNotSuppressRetry(t *testing.T) {
	withCrashSidecarDir(t)
	sid := "crash-3366"

	writeSidecar(t, sid, nil)
	l := newToolDedupLedger()
	if n := SeedCrashDedup(&Agent{sessionID: sid, toolDedup: l}, sid); n != 0 {
		t.Fatalf("seeded = %d, want 0 (empty sidecar)", n)
	}
	if res := l.suppressDuplicate("run_command", `{"command":"make build"}`); res != nil {
		t.Fatalf("retry after a failed call must not be suppressed, got: %s", res.Content)
	}
}

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
