package agent

// #3780 companion tests.
// A: absolute artifact_glob must match absolute walked paths in the disk
//    probe (comment contract "used as-is"; write path already matched).
// B: recentAttempts must return the NEWEST n attempts, not the oldest 3.
// C: a command matching several steps' OnCommands attributes an attempt
//    to EVERY matching step (no first-match-break exclusivity).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

func TestIssue3780_AbsoluteGlobProbe(t *testing.T) {
	e := newWFEngineWithSpec(t, `{"steps":[{"id":"gen","artifact_glob":"**/cov.out"}]}`)
	dir := filepath.Dir(e.loadDir)
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	e.startedAt = time.Now().Add(-2 * time.Second) // anchor before the write
	fresh := filepath.Join(out, "cov.out")
	if err := os.WriteFile(fresh, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fresh, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	// Relative pattern still grounds (regression).
	if !e.globFreshOnDisk("**/cov.out") {
		t.Error("relative ** glob must still probe fresh artifact")
	}
	// Absolute pattern, same file: used as-is (pre-fix never matched).
	if !e.globFreshOnDisk(filepath.ToSlash(fresh)) {
		t.Error("absolute pattern must match the absolute walked path")
	}
	absSuper := filepath.ToSlash(filepath.Join(dir, "**", "cov.out"))
	if !e.globFreshOnDisk(absSuper) {
		t.Error("absolute superglob pattern must probe fresh artifact")
	}
	// Non-existent absolute target stays ungrounded.
	if e.globFreshOnDisk(filepath.ToSlash(filepath.Join(dir, "nope.out"))) {
		t.Error("non-existent absolute path must not ground")
	}
}

func TestIssue3780_RecentAttemptsReturnsNewest(t *testing.T) {
	e := &workflowEngine{}
	base := time.Now()
	e.traceMu.Lock()
	for i := 1; i <= 5; i++ {
		e.trace = append(e.trace, StepAttempt{
			StepID:  "s",
			Command: "cmd-" + string(rune('0'+i)),
			At:      base.Add(time.Duration(i) * time.Second),
		})
		// noise from another step in between
		e.trace = append(e.trace, StepAttempt{StepID: "other", Command: "x", At: base})
	}
	e.traceMu.Unlock()
	att := e.recentAttempts("s", 3)
	if len(att) != 3 {
		t.Fatalf("want 3 attempts, got %d", len(att))
	}
	// Oldest-first order preserved...
	for i := 1; i < len(att); i++ {
		if att[i].At.Before(att[i-1].At) {
			t.Fatal("recentAttempts must return oldest-first")
		}
	}
	// ...but they must be the NEWEST three (3,4,5), and the last one is
	// the truly latest (pre-fix this was the 3rd-oldest: cmd-3 while 5 existed).
	if want := "cmd-5"; att[len(att)-1].Command != want {
		t.Errorf("LastAttempt candidate = %q, want %q", att[len(att)-1].Command, want)
	}
	if att[0].Command != "cmd-3" {
		t.Errorf("window start = %q, want cmd-3 (newest-3 window)", att[0].Command)
	}
}

const spec3780overlap = `{
  "steps": [
    {"id":"all-tests","artifact_glob":"","on_commands":["go test*"]},
    {"id":"build-tests","artifact_glob":"","requires":[],"mode":"warn","on_commands":["go test -run Build*"]}
  ]
}`

func TestIssue3780_OverlappingOnCommandsMultiAttribute(t *testing.T) {
	e := newWFEngineWithSpec(t, spec3780overlap)
	args := json.RawMessage(`{"command":"go test -run Build ./..."}`)
	e.recordAttempt("run_command", args, tool.Result{Content: "ok"})
	// BOTH steps must carry the attempt (pre-fix only all-tests, the first
	// declared match, did - and attribution for build-tests claimed
	// "never executed" about the command that had just run).
	for _, id := range []string{"all-tests", "build-tests"} {
		att := e.recentAttempts(id, 5)
		if len(att) != 1 {
			t.Errorf("step %s: want 1 attempt, got %d", id, len(att))
			continue
		}
		if att[0].Command != "go test -run Build ./..." {
			t.Errorf("step %s: command = %q", id, att[0].Command)
		}
	}
	// Unrelated command attributes nothing.
	e.recordAttempt("run_command", json.RawMessage(`{"command":"ls"}`), tool.Result{Content: ""})
	if got := len(e.recentAttempts("all-tests", 5)); got != 1 {
		t.Errorf("unrelated command must not add attempts, got %d", got)
	}
}
