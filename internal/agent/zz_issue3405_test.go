package agent

import (
	"strings"
	"testing"
)

// #3405: recordSuccess must decay per-hint, not wipe the whole tool's set.
// A one-off mistake (FailCount=1) still clears on the first success, but a
// repeated failure pattern survives isolated successes so the
// persist-across-sessions contract holds for high-frequency tools.

// singleFailureHint returns a store with one FailCount=1 hint for tool.
func singleFailureHint(t *testing.T, tool string) *toolUsageHintStore {
	t.Helper()
	s := newToolUsageHintStore()
	s.attach(t.TempDir())
	s.recordFailure(tool, `missing required parameter "pattern" for grep`)
	return s
}

func TestIssue3405SingleFailureClearsOnFirstSuccess(t *testing.T) {
	s := singleFailureHint(t, "grep")
	s.recordSuccess("grep")
	if s.OverlayFor("grep") != "" {
		t.Fatal("one-off hint (FailCount=1) must clear on first success - same-run correction stays cheap")
	}
}

func TestIssue3405RepeatedFailureSurvivesSingleSuccess(t *testing.T) {
	s := newToolUsageHintStore()
	s.attach(t.TempDir())
	for i := 0; i < 3; i++ {
		s.recordFailure("browser", `parameter "selector" must be string in /tmp/x.html`)
	}
	// One unrelated success (e.g. a successful navigate) must NOT wipe the set.
	s.recordSuccess("browser")
	if s.OverlayFor("browser") == "" {
		t.Fatal("single success must not wipe a FailCount=3 hint set")
	}
	list := s.hints["browser"]
	if len(list) != 1 || list[0].FailCount != 2 {
		t.Fatalf("expected FailCount decremented 3->2, got %+v", list)
	}
	// Two more successes bring it to zero and remove it.
	s.recordSuccess("browser")
	s.recordSuccess("browser")
	if s.OverlayFor("browser") != "" {
		t.Fatal("successes down to zero must decay the hint away")
	}
}

func TestIssue3405HintsDecayIndependently(t *testing.T) {
	s := newToolUsageHintStore()
	s.attach(t.TempDir())
	s.recordFailure("edit_file", `missing required parameter "old_text" in /tmp/a.go`) // count 1
	for i := 0; i < 3; i++ {
		s.recordFailure("edit_file", `parameter "file_path" must be string in /tmp/b.go`) // count 3
	}
	s.recordSuccess("edit_file") // weak hint dies, strong hint survives
	ov := s.OverlayFor("edit_file")
	if !strings.Contains(ov, "must be string") {
		t.Fatalf("strong hint must survive one success: %q", ov)
	}
	if strings.Contains(ov, "missing required parameter") {
		t.Fatalf("weak hint (FailCount=1) must clear on first success: %q", ov)
	}
	if got := len(s.hints["edit_file"]); got != 1 {
		t.Fatalf("expected 1 surviving hint, got %d", got)
	}
}

func TestIssue3405PersistAcrossStoresAfterIsolatedSuccess(t *testing.T) {
	dir := t.TempDir()
	s := newToolUsageHintStore()
	s.attach(dir)
	s.recordFailure("run_command", `unexpected parameter "timeout" for run_command`)
	s.recordFailure("run_command", `unexpected parameter "timeout" for run_command`)
	s.recordSuccess("run_command") // isolated success between failures

	// A fresh session must still see the hint - the cross-session
	// persistence contract that per-tool wipe used to void.
	s2 := newToolUsageHintStore()
	s2.attach(dir)
	if s2.OverlayFor("run_command") == "" {
		t.Fatal("hint with remaining FailCount must persist across stores after an isolated success")
	}
}
