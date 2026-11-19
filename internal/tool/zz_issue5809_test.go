package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zz_issue5809_test.go pins the arm-report half of the fix: the OK receipt
// must hand the operator RUNTIME verification anchors — the CURRENT
// revision this (old) process is running plus a fresh memstats line
// (numGC resets on execve) — and must explicitly demote the two false
// anchors the fleet incident relied on: the arm-time DISK target revision
// (a later disk sync or the apply-at-exec update flow changes it without
// proving anything about the running process) and process start time
// (unchanged across execve). The file only uses symbols that exist at the
// pre-fix ref, so it runs there and turns red on the missing guidance.

type issue5809Requester struct {
	called   bool
	execPath string
}

func (f *issue5809Requester) RequestRestart(reason string, debugMode bool) {
	f.called = true
}

func (f *issue5809Requester) ExecTargetPath() string     { return f.execPath }
func (f *issue5809Requester) ExecTargetRevision() string { return "411da6abf9e0" }

func TestIssue5809ArmReportNamesRuntimeAnchorsNotDiskState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GGCODE_RESTART_ARM_DIR", dir)
	target := filepath.Join(t.TempDir(), "ggcode-new")
	if err := os.WriteFile(target, []byte("staged new binary for #5809"), 0o755); err != nil {
		t.Fatal(err)
	}

	req := &issue5809Requester{execPath: target}
	rt := &RestartTool{Requester: req}
	res, err := rt.Execute(context.Background(), json.RawMessage(`{"reason":"verify report anchors"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if !req.called {
		t.Error("RequestRestart was not called")
	}

	// The runtime anchor: the arming (old) process's CURRENT revision,
	// verbatim, so a landed image-change restart can be diffed against it.
	if !strings.Contains(res.Content, "current revision 411da6abf9e0") {
		t.Errorf("report must carry this process's current revision as the post-restart diff anchor: %s", res.Content)
	}
	// The memstats anchor: numGC resets on execve.
	for _, want := range []string{"fresh memstats line", "numGC resets on exec"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("report missing runtime verification signal %q: %s", want, res.Content)
		}
	}
	// Process start time must be explicitly demoted: it does not change
	// across execve, so a same-pid exec looks like a same-pid non-restart.
	if !strings.Contains(res.Content, "process start time is NOT a verification signal") {
		t.Errorf("report must demote process start time as a signal: %s", res.Content)
	}
	// The disk target revision stays as CONTEXT but must be labeled as
	// arm-time disk state, not as the verification anchor.
	if !strings.Contains(res.Content, "target revision") || !strings.Contains(res.Content, "on disk at arm time") {
		t.Errorf("report must label the disk target revision as arm-time disk context: %s", res.Content)
	}
}
